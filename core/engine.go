// Package core provides the WhatsApp engine implementation.
// This file contains the main Engine struct and public API.
//
// ANTI-BAN WIRING (new in this version):
//   - RateLimiter:     throttles outgoing messages with human-like jitter
//   - PresenceManager: sends online/offline/typing signals like a real browser
//   - Backoff:         exponential reconnect delay to avoid server hammering
package core

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// EngineState represents the overall state of the engine.
type EngineState int32

const (
	EngineStateStopped  EngineState = iota
	EngineStateStarting
	EngineStateRunning
	EngineStateStopping
	EngineStatePairing
)

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
type Engine struct {
	mu          sync.RWMutex
	dataDir     string
	storage     *Storage
	client      *Client
	sender      *Sender
	eventQueue  *EventQueue
	state       int32 // atomic EngineState
	log         waLog.Logger
	initialized int32 // atomic bool

	// Anti-ban components
	limiter  *RateLimiter
	presence *PresenceManager
	backoff  *Backoff
}

// NewEngine creates a new WhatsApp engine instance.
// dataDir is the directory where session data will be stored.
func NewEngine(dataDir string) (*Engine, error) {
	if dataDir == "" {
		return nil, NewError(ErrCodeNotInitialized, "Data directory is required")
	}

	log := waLog.Stdout("Engine", "INFO", true)

	storage, err := NewStorage(dataDir, log)
	if err != nil {
		return nil, err
	}
	if err := storage.Initialize(); err != nil {
		return nil, err
	}

	eventQueue := NewEventQueue(1000)

	client, err := NewClient(storage, eventQueue, log)
	if err != nil {
		return nil, err
	}

	// Anti-ban: shared rate limiter
	limiter := NewRateLimiter(DefaultRateLimiterConfig())

	// Anti-ban: presence manager
	presence := NewPresenceManager(client, limiter, DefaultPresenceConfig())

	// Anti-ban: reconnection backoff
	backoff := NewBackoff(DefaultBackoffConfig())

	// Wire backoff and presence into the client event handling
	client.SetPresenceManager(presence)
	client.SetBackoff(backoff)

	// Build sender with anti-ban hooks
	sender := NewSenderWithAntiBlock(client, eventQueue, limiter, presence)

	engine := &Engine{
		dataDir:     dataDir,
		storage:     storage,
		client:      client,
		sender:      sender,
		eventQueue:  eventQueue,
		state:       int32(EngineStateStopped),
		log:         log,
		initialized: 1,
		limiter:     limiter,
		presence:    presence,
		backoff:     backoff,
	}

	return engine, nil
}

// Start initiates the WhatsApp connection. IDEMPOTENT.
func (e *Engine) Start() error {
	current := EngineState(atomic.LoadInt32(&e.state))
	if current == EngineStateRunning || current == EngineStateStarting || current == EngineStatePairing {
		return nil
	}
	if !atomic.CompareAndSwapInt32(&e.state, int32(current), int32(EngineStateStarting)) {
		return e.Start()
	}
	if !e.storage.IsPaired() {
		atomic.StoreInt32(&e.state, int32(EngineStateStopped))
		return ErrNotPaired
	}
	if err := e.client.Connect(); err != nil {
		atomic.StoreInt32(&e.state, int32(EngineStateStopped))
		return err
	}
	atomic.StoreInt32(&e.state, int32(EngineStateRunning))
	e.log.Infof("Engine started, connecting with existing session")
	return nil
}

// StartPairing initiates QR code based authentication.
func (e *Engine) StartPairing() error {
	current := EngineState(atomic.LoadInt32(&e.state))
	if current == EngineStatePairing {
		return nil
	}
	if current == EngineStateRunning {
		return NewError(ErrCodeAlreadyRunning, "Engine is already running")
	}
	if e.storage.IsPaired() {
		return NewError(ErrCodeAlreadyRunning, "Already paired, use Start() instead")
	}
	atomic.StoreInt32(&e.state, int32(EngineStatePairing))
	if err := e.client.StartPairing(); err != nil {
		atomic.StoreInt32(&e.state, int32(EngineStateStopped))
		return err
	}
	e.log.Infof("Pairing started, waiting for QR scan")
	return nil
}

// StartPhonePairing initiates code-based phone pairing.
// phone: full international phone number (with country code, no + prefix).
// Returns the 8-character pairing code (XXXX-XXXX format).
func (e *Engine) StartPhonePairing(phone string) (string, error) {
	current := EngineState(atomic.LoadInt32(&e.state))
	if current == EngineStatePairing {
		return "", NewError(ErrCodeAlreadyRunning, "Already pairing")
	}
	if current == EngineStateRunning {
		return "", NewError(ErrCodeAlreadyRunning, "Engine is already running")
	}
	if e.storage.IsPaired() {
		return "", NewError(ErrCodeAlreadyRunning, "Already paired, use Start() instead")
	}
	atomic.StoreInt32(&e.state, int32(EngineStatePairing))
	code, err := e.client.StartPhonePairing(phone)
	if err != nil {
		atomic.StoreInt32(&e.state, int32(EngineStateStopped))
		return "", err
	}
	e.log.Infof("Phone pairing started for %s", phone)
	return code, nil
}

// Stop closes the WhatsApp connection gracefully. IDEMPOTENT.
func (e *Engine) Stop() {
	current := EngineState(atomic.LoadInt32(&e.state))
	if current == EngineStateStopped || current == EngineStateStopping {
		return
	}
	atomic.StoreInt32(&e.state, int32(EngineStateStopping))

	// Go offline gracefully before cutting the connection
	if e.presence != nil {
		e.presence.Stop()
	}

	e.client.Disconnect()
	atomic.StoreInt32(&e.state, int32(EngineStateStopped))
	e.log.Infof("Engine stopped")
}

// ----- Status -----

func (e *Engine) IsPaired() bool             { return e.storage.IsPaired() }
func (e *Engine) IsConnected() bool          { return e.client.IsConnected() }
func (e *Engine) IsLoggedIn() bool           { return e.client.IsLoggedIn() }
func (e *Engine) GetQR() string              { return e.client.GetCurrentQR() }
func (e *Engine) GetPairingCode() string     { return e.client.GetCurrentPairingCode() }
func (e *Engine) HasSession() bool           { return e.IsPaired() }
func (e *Engine) GetConnectionState() string { return e.client.GetState().String() }
func (e *Engine) GetDataDir() string         { return e.dataDir }

func (e *Engine) GetState() string {
	return EngineState(atomic.LoadInt32(&e.state)).String()
}
func (e *Engine) GetJID() string {
	if jid := e.client.GetJID(); jid != nil {
		return jid.String()
	}
	return e.storage.GetJID()
}

// ----- Anti-ban Runtime Config -----

// SetRateLimiterConfig replaces the rate limiter config while running.
func (e *Engine) SetRateLimiterConfig(cfg RateLimiterConfig) {
	newLimiter := NewRateLimiter(cfg)
	e.mu.Lock()
	e.limiter = newLimiter
	e.sender.SetRateLimiter(newLimiter)
	if e.presence != nil {
		e.presence.limiter = newLimiter
	}
	e.mu.Unlock()
}

// SetPresenceConfig replaces the presence manager config while running.
func (e *Engine) SetPresenceConfig(cfg PresenceConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := NewPresenceManager(e.client, e.limiter, cfg)
	e.presence = p
	e.client.SetPresenceManager(p)
	e.sender.SetPresenceManager(p)
}

// MarkActive signals app is actively in use - keeps presence "online".
// Call from your polling loop / foreground activity.
func (e *Engine) MarkActive() {
	if e.presence != nil {
		e.presence.MarkActive()
	}
}

// ----- Messaging -----

func (e *Engine) SendText(jid string, text string) (string, error) {
	if !e.client.IsConnected() {
		return "", ErrNotConnected
	}
	return e.sender.SendText(jid, text)
}

func (e *Engine) SendTextReply(jid string, text string, quotedID string) (string, error) {
	if !e.client.IsConnected() {
		return "", ErrNotConnected
	}
	return e.sender.SendTextReply(jid, text, quotedID)
}

func (e *Engine) SendMessage(to, sessionName, message string) (string, error) {
	_ = sessionName
	return e.SendText(to, message)
}

func (e *Engine) SendImageWithCaption(to, sessionName, imageSource, caption string) (string, error) {
	_ = sessionName
	if !e.client.IsConnected() {
		return "", ErrNotConnected
	}
	return e.sender.SendImageWithCaption(to, imageSource, caption)
}

// ----- Events -----

func (e *Engine) PollEvent() string      { return e.eventQueue.PollJSON() }
func (e *Engine) GetEventQueueSize() int { return e.eventQueue.Size() }
func (e *Engine) ClearEvents()           { e.eventQueue.Clear() }

func (e *Engine) GetEventQueueStats() string {
	data, _ := json.Marshal(e.eventQueue.Stats())
	return string(data)
}

// ----- Lifecycle -----

func (e *Engine) Logout() error {
	e.Stop()
	if err := e.storage.ClearSession(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	client, err := NewClient(e.storage, e.eventQueue, e.log)
	if err != nil {
		return err
	}
	p := NewPresenceManager(client, e.limiter, DefaultPresenceConfig())
	client.SetPresenceManager(p)
	client.SetBackoff(e.backoff)
	e.client = client
	e.presence = p
	e.sender = NewSenderWithAntiBlock(client, e.eventQueue, e.limiter, p)
	return nil
}

// ----- Utilities -----

func (e *Engine) SetTyping(jid string, typing bool) error {
	if !e.client.IsConnected() {
		return ErrNotConnected
	}
	if typing {
		return e.sender.SendChatPresence(jid, "composing", "")
	}
	return e.sender.SendChatPresence(jid, "paused", "")
}

func (e *Engine) MarkRead(chatJID, senderJID, messageIDsJSON string) error {
	if !e.client.IsConnected() {
		return ErrNotConnected
	}
	var ids []string
	if err := json.Unmarshal([]byte(messageIDsJSON), &ids); err != nil {
		return WrapError(ErrCodeInternal, "Invalid message IDs JSON", err)
	}
	return e.sender.SendReadReceipt(chatJID, senderJID, ids)
}

func (e *Engine) FormatPhoneJID(phone string) string  { return FormatPhoneToJID(phone) }
func (e *Engine) FormatGroupJID(groupID string) string { return FormatGroupToJID(groupID) }
func (e *Engine) ValidateJID(jid string) bool          { return IsValidJID(jid) }
