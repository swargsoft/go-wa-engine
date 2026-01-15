// Package core provides the WhatsApp engine implementation.
// This file contains the main Engine struct and public API.
//
// ENGINE LIFECYCLE:
// - NewEngine() initializes storage and creates client
// - Start() connects to WhatsApp (auto-reconnects if session exists)
// - StartPairing() initiates QR-based pairing (first time only)
// - Stop() gracefully disconnects
// - All methods are IDEMPOTENT and THREAD-SAFE
//
// MOBILE LIFECYCLE HANDLING:
// - Engine survives process death via SQLite session persistence
// - On app restart: call NewEngine() then Start() to auto-reconnect
// - No re-pairing needed unless session is invalidated by WhatsApp
//
// THREAD SAFETY:
// - All public methods are safe for concurrent calls
// - Internal state protected by sync.RWMutex
// - Atomic operations for fast state checks
package core

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// EngineState represents the overall state of the engine.
type EngineState int32 // int32 for atomic operations

const (
	// EngineStateStopped indicates the engine is not running.
	EngineStateStopped EngineState = iota
	// EngineStateStarting indicates the engine is starting up.
	EngineStateStarting
	// EngineStateRunning indicates the engine is running.
	EngineStateRunning
	// EngineStateStopping indicates the engine is shutting down.
	EngineStateStopping
	// EngineStatePairing indicates QR pairing is in progress.
	EngineStatePairing
)

// String returns the string representation of the engine state.
func (s EngineState) String() string {
	switch s {
	case EngineStateStopped:
		return "stopped"
	case EngineStateStarting:
		return "starting"
	case EngineStateRunning:
		return "running"
	case EngineStateStopping:
		return "stopping"
	case EngineStatePairing:
		return "pairing"
	default:
		return "unknown"
	}
}

// Engine is the main WhatsApp engine that manages the connection lifecycle.
// This is the primary entry point for the SDK.
//
// USAGE:
//
//	engine, err := NewEngine("/path/to/data")
//	if engine.IsPaired() {
//	    engine.Start()  // Auto-reconnects
//	} else {
//	    engine.StartPairing()  // Emits QR events
//	}
type Engine struct {
	mu         sync.RWMutex
	dataDir    string
	storage    *Storage
	client     *Client
	sender     *Sender
	eventQueue *EventQueue
	state      int32 // atomic, use EngineState
	log        waLog.Logger
	
	// Initialization tracking
	initialized int32 // atomic bool
}

// NewEngine creates a new WhatsApp engine instance.
// dataDir is the directory where session data will be stored.
//
// This method is IDEMPOTENT when called with the same dataDir.
// On app restart, this will load the existing session from SQLite.
func NewEngine(dataDir string) (*Engine, error) {
	if dataDir == "" {
		return nil, NewError(ErrCodeNotInitialized, "Data directory is required")
	}

	// Create logger first
	log := waLog.Stdout("Engine", "INFO", true)

	// Create storage with logger
	storage, err := NewStorage(dataDir, log)
	if err != nil {
		return nil, err
	}

	// Initialize storage (creates DB and device, loads existing session)
	if err := storage.Initialize(); err != nil {
		return nil, err
	}

	// Create event queue with bounded size
	eventQueue := NewEventQueue(1000)

	// Create client
	client, err := NewClient(storage, eventQueue, log)
	if err != nil {
		return nil, err
	}

	// Create sender
	sender := NewSender(client, eventQueue)

	engine := &Engine{
		dataDir:     dataDir,
		storage:     storage,
		client:      client,
		sender:      sender,
		eventQueue:  eventQueue,
		state:       int32(EngineStateStopped),
		log:         log,
		initialized: 1,
	}

	return engine, nil
}

// Start initiates the WhatsApp connection.
// IDEMPOTENT: Safe to call multiple times.
//
// Behavior:
// - If already running, returns nil (no-op)
// - If session exists (IsPaired), auto-reconnects without QR
// - If no session, returns ErrNotPaired - use StartPairing() instead
//
// On app restart with existing session:
//
//	engine, _ := NewEngine(dataDir)
//	err := engine.Start()  // Auto-reconnects, no QR needed
func (e *Engine) Start() error {
	currentState := EngineState(atomic.LoadInt32(&e.state))
	
	// Idempotent - already running
	if currentState == EngineStateRunning || currentState == EngineStateStarting {
		return nil
	}
	
	// Pairing in progress
	if currentState == EngineStatePairing {
		return nil
	}

	// Try to transition to starting state
	if !atomic.CompareAndSwapInt32(&e.state, int32(currentState), int32(EngineStateStarting)) {
		// State changed concurrently, retry
		return e.Start()
	}

	// Check if we have a session
	if !e.storage.IsPaired() {
		atomic.StoreInt32(&e.state, int32(EngineStateStopped))
		return ErrNotPaired
	}

	// Connect using existing session
	err := e.client.Connect()
	if err != nil {
		atomic.StoreInt32(&e.state, int32(EngineStateStopped))
		return err
	}

	atomic.StoreInt32(&e.state, int32(EngineStateRunning))
	e.log.Infof("Engine started, connecting with existing session")
	return nil
}

// StartPairing initiates QR code based authentication.
// Use this when IsPaired() returns false.
//
// Flow:
// 1. Call StartPairing()
// 2. Poll PollEvent() for qr.updated events
// 3. Display QR code to user
// 4. Wait for pairing.success or pairing.failed
// 5. On success, call Start() for future connections
func (e *Engine) StartPairing() error {
	currentState := EngineState(atomic.LoadInt32(&e.state))
	
	// Already pairing
	if currentState == EngineStatePairing {
		return nil
	}
	
	// Already running
	if currentState == EngineStateRunning {
		return NewError(ErrCodeAlreadyRunning, "Engine is already running")
	}
	
	// Already paired
	if e.storage.IsPaired() {
		return NewError(ErrCodeAlreadyRunning, "Already paired, use Start() instead")
	}

	atomic.StoreInt32(&e.state, int32(EngineStatePairing))
	
	err := e.client.StartPairing()
	if err != nil {
		atomic.StoreInt32(&e.state, int32(EngineStateStopped))
		return err
	}

	e.log.Infof("Pairing started, waiting for QR scan")
	return nil
}

// Stop closes the WhatsApp connection gracefully.
// IDEMPOTENT: Safe to call multiple times.
func (e *Engine) Stop() {
	currentState := EngineState(atomic.LoadInt32(&e.state))
	
	// Already stopped
	if currentState == EngineStateStopped || currentState == EngineStateStopping {
		return
	}

	atomic.StoreInt32(&e.state, int32(EngineStateStopping))
	
	e.client.Disconnect()
	
	atomic.StoreInt32(&e.state, int32(EngineStateStopped))
	e.log.Infof("Engine stopped")
}

// ----- Status Methods -----

// IsPaired returns true if a valid session exists in SQLite.
// Works across app restarts - checks persistent storage.
func (e *Engine) IsPaired() bool {
	return e.storage.IsPaired()
}

// IsConnected returns true if currently connected to WhatsApp servers.
func (e *Engine) IsConnected() bool {
	return e.client.IsConnected()
}

// IsLoggedIn returns true if there's an active authenticated session.
// Alias for IsConnected() for backward compatibility.
func (e *Engine) IsLoggedIn() bool {
	return e.client.IsLoggedIn()
}

// GetQR returns the current QR code string for authentication.
// Returns an empty string if no QR code is currently available.
func (e *Engine) GetQR() string {
	return e.client.GetCurrentQR()
}

// GetState returns the current engine state as a string.
func (e *Engine) GetState() string {
	return EngineState(atomic.LoadInt32(&e.state)).String()
}

// GetConnectionState returns the current connection state as a string.
func (e *Engine) GetConnectionState() string {
	return e.client.GetState().String()
}

// GetJID returns the current user's JID if paired.
// Returns an empty string if not paired.
func (e *Engine) GetJID() string {
	jid := e.client.GetJID()
	if jid == nil {
		// Fallback to storage
		return e.storage.GetJID()
	}
	return jid.String()
}

// HasSession is an alias for IsPaired for backward compatibility.
func (e *Engine) HasSession() bool {
	return e.IsPaired()
}

// ----- Messaging Methods -----

// SendText sends a text message to the specified JID.
// Returns the message ID on success.
func (e *Engine) SendText(jid string, text string) (string, error) {
	if !e.client.IsConnected() {
		return "", ErrNotConnected
	}
	return e.sender.SendText(jid, text)
}

// SendTextReply sends a text message as a reply to another message.
func (e *Engine) SendTextReply(jid string, text string, quotedID string) (string, error) {
	if !e.client.IsConnected() {
		return "", ErrNotConnected
	}
	return e.sender.SendTextReply(jid, text, quotedID)
}

// SendMessage is a generic message sending method.
// to: recipient JID
// sessionName: logical session identifier (reserved for multi-session support)
// message: text message content
func (e *Engine) SendMessage(to string, sessionName string, message string) (string, error) {
	// sessionName is reserved for future multi-session support
	_ = sessionName
	return e.SendText(to, message)
}

// SendImageWithCaption sends an image with caption.
// to: recipient JID
// sessionName: logical session identifier
// imageSource: URL or base64-encoded image data
// caption: optional caption text
func (e *Engine) SendImageWithCaption(to string, sessionName string, imageSource string, caption string) (string, error) {
	_ = sessionName
	if !e.client.IsConnected() {
		return "", ErrNotConnected
	}
	return e.sender.SendImageWithCaption(to, imageSource, caption)
}

// ----- Event Methods -----

// PollEvent retrieves the next event from the queue as a JSON string.
// Returns an empty string if no events are available.
// This method is NON-BLOCKING and safe for frequent polling.
func (e *Engine) PollEvent() string {
	return e.eventQueue.PollJSON()
}

// GetEventQueueSize returns the number of pending events.
func (e *Engine) GetEventQueueSize() int {
	return e.eventQueue.Size()
}

// GetEventQueueStats returns queue statistics as JSON.
func (e *Engine) GetEventQueueStats() string {
	stats := e.eventQueue.Stats()
	data, _ := json.Marshal(stats)
	return string(data)
}

// ClearEvents removes all pending events from the queue.
func (e *Engine) ClearEvents() {
	e.eventQueue.Clear()
}

// ----- Lifecycle Methods -----

// Logout clears the session and forces re-authentication.
// After logout, StartPairing() must be called to pair again.
func (e *Engine) Logout() error {
	// Stop the connection first
	e.Stop()

	// Clear session data
	if err := e.storage.ClearSession(); err != nil {
		return err
	}

	// Reinitialize client with new device
	e.mu.Lock()
	defer e.mu.Unlock()
	
	client, err := NewClient(e.storage, e.eventQueue, e.log)
	if err != nil {
		return err
	}
	e.client = client
	e.sender = NewSender(client, e.eventQueue)

	e.log.Infof("Session cleared, re-pairing required")
	return nil
}

// GetDataDir returns the data directory path.
func (e *Engine) GetDataDir() string {
	return e.dataDir
}

// ----- Utility Methods -----

// SetTyping sends a typing indicator to the specified chat.
// Set typing=true to show "typing...", false to stop.
func (e *Engine) SetTyping(jid string, typing bool) error {
	if !e.client.IsConnected() {
		return ErrNotConnected
	}
	if typing {
		return e.sender.SendChatPresence(jid, "composing", "")
	}
	return e.sender.SendChatPresence(jid, "paused", "")
}

// MarkRead marks messages as read.
// messageIDsJSON is a JSON array of message IDs to mark as read.
func (e *Engine) MarkRead(chatJID string, senderJID string, messageIDsJSON string) error {
	if !e.client.IsConnected() {
		return ErrNotConnected
	}
	// Parse message IDs from JSON
	var messageIDs []string
	if err := json.Unmarshal([]byte(messageIDsJSON), &messageIDs); err != nil {
		return WrapError(ErrCodeInternal, "Invalid message IDs JSON", err)
	}
	return e.sender.SendReadReceipt(chatJID, senderJID, messageIDs)
}

// FormatPhoneJID converts a phone number to a WhatsApp JID.
func (e *Engine) FormatPhoneJID(phone string) string {
	return FormatPhoneToJID(phone)
}

// FormatGroupJID converts a group ID to a WhatsApp group JID.
func (e *Engine) FormatGroupJID(groupID string) string {
	return FormatGroupToJID(groupID)
}

// ValidateJID checks if a JID string is valid.
func (e *Engine) ValidateJID(jid string) bool {
	return IsValidJID(jid)
}
