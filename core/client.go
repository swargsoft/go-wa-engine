// Package core provides the WhatsApp engine implementation.
// This file handles the WhatsMeow client wrapper.
//
// CLIENT LIFECYCLE:
// - Connect() is idempotent - multiple calls are safe
// - Disconnect() is idempotent - multiple calls are safe
// - Auto-reconnects when session exists
// - Emits proper events for all state transitions
//
// PAIRING FLOW:
// 1. Call StartPairing() to initiate QR-based auth
// 2. Poll events for qr.updated to get QR codes
// 3. Wait for pairing.success or pairing.failed
// 4. On success, session is persisted to SQLite
//
// RECONNECT FLOW (app restart with existing session):
// 1. Call Connect() - detects existing session in SQLite
// 2. Auto-connects to WhatsApp servers
// 3. Emits connection.open and logged_in events
// 4. NO QR code needed - session is reused
package core

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// ClientState represents the current state of the WhatsApp client.
type ClientState int32 // int32 for atomic operations

const (
	// StateDisconnected indicates no connection.
	StateDisconnected ClientState = iota
	// StateConnecting indicates connection in progress.
	StateConnecting
	// StateConnected indicates active connection.
	StateConnected
	// StateReconnecting indicates reconnection in progress.
	StateReconnecting
	// StatePairing indicates QR pairing in progress.
	StatePairing
)

// String returns the string representation of the client state.
func (s ClientState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	case StatePairing:
		return "pairing"
	default:
		return "unknown"
	}
}

// Client wraps the WhatsMeow client with additional state management.
// Thread-safe and handles mobile lifecycle events properly.
type Client struct {
	mu          sync.RWMutex
	client      *whatsmeow.Client
	storage     *Storage
	eventQueue  *EventQueue
	state       int32  // atomic, use ClientState
	currentQR   string
	qrCtx       context.Context
	qrCancel    context.CancelFunc
	log         waLog.Logger
	
	// Pairing state
	pairingActive int32 // atomic bool
	
	// Connection tracking
	connectOnce   sync.Once
	disconnectMu  sync.Mutex
}

// NewClient creates a new WhatsApp client wrapper.
func NewClient(storage *Storage, eventQueue *EventQueue, log waLog.Logger) (*Client, error) {
	device := storage.GetDevice()
	if device == nil {
		return nil, NewError(ErrCodeNotInitialized, "Storage not initialized")
	}

	client := whatsmeow.NewClient(device, log)
	
	// Enable auto-reconnect
	client.EnableAutoReconnect = true
	client.AutoReconnectErrors = 5 // Allow 5 consecutive errors before giving up

	return &Client{
		client:     client,
		storage:    storage,
		eventQueue: eventQueue,
		state:      int32(StateDisconnected),
		log:        log,
	}, nil
}

// Connect establishes a connection to WhatsApp.
// IDEMPOTENT: Safe to call multiple times.
//
// Behavior:
// - If already connected, returns nil (no-op)
// - If session exists, reconnects automatically (no QR needed)
// - If no session, caller must use StartPairing() first
func (c *Client) Connect() error {
	currentState := ClientState(atomic.LoadInt32(&c.state))
	
	// Already connected or connecting - idempotent success
	if currentState == StateConnected || currentState == StateConnecting {
		return nil
	}
	
	// If pairing is active, don't interrupt
	if currentState == StatePairing {
		return nil
	}

	// Set connecting state
	if !atomic.CompareAndSwapInt32(&c.state, int32(currentState), int32(StateConnecting)) {
		// State changed concurrently, retry
		return c.Connect()
	}

	// Register event handler (safe to call multiple times)
	c.client.AddEventHandler(c.handleEvent)

	// Check if we have an existing session
	if c.storage.IsPaired() {
		// We have a session - connect directly
		return c.connectExisting()
	}

	// No session - caller needs to use StartPairing()
	atomic.StoreInt32(&c.state, int32(StateDisconnected))
	return ErrNotPaired
}

// StartPairing initiates QR code based authentication.
// Call this when IsPaired() returns false.
//
// Flow:
// 1. Emits qr.updated events with QR code strings
// 2. User scans QR with WhatsApp mobile
// 3. Emits pairing.success or pairing.failed
// 4. On success, session is persisted automatically
func (c *Client) StartPairing() error {
	currentState := ClientState(atomic.LoadInt32(&c.state))
	
	// Already pairing - idempotent
	if currentState == StatePairing {
		return nil
	}
	
	// Already connected - can't pair
	if currentState == StateConnected {
		return NewError(ErrCodeAlreadyRunning, "Already connected, cannot start pairing")
	}
	
	// Already paired - no need to pair again
	if c.storage.IsPaired() {
		return NewError(ErrCodeAlreadyRunning, "Already paired, use Connect() instead")
	}

	// Set pairing state
	atomic.StoreInt32(&c.state, int32(StatePairing))
	atomic.StoreInt32(&c.pairingActive, 1)

	// Register event handler
	c.client.AddEventHandler(c.handleEvent)

	// Create cancellable context for QR channel
	c.mu.Lock()
	if c.qrCancel != nil {
		c.qrCancel() // Cancel any existing QR session
	}
	c.qrCtx, c.qrCancel = context.WithCancel(context.Background())
	ctx := c.qrCtx
	c.mu.Unlock()

	// Get QR channel
	qrChan, err := c.client.GetQRChannel(ctx)
	if err != nil {
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		atomic.StoreInt32(&c.pairingActive, 0)
		return WrapError(ErrCodeConnectionFailed, "Failed to get QR channel", err)
	}

	// Connect to WhatsApp
	err = c.client.Connect()
	if err != nil {
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		atomic.StoreInt32(&c.pairingActive, 0)
		return WrapError(ErrCodeConnectionFailed, "Failed to connect for pairing", err)
	}

	// Handle QR codes in background
	go c.handleQRChannel(qrChan)

	return nil
}

// handleQRChannel processes QR codes from the channel.
func (c *Client) handleQRChannel(qrChan <-chan whatsmeow.QRChannelItem) {
	defer func() {
		atomic.StoreInt32(&c.pairingActive, 0)
		c.mu.Lock()
		c.currentQR = ""
		c.mu.Unlock()
	}()

	for item := range qrChan {
		switch item.Event {
		case "code":
			c.mu.Lock()
			c.currentQR = item.Code
			c.mu.Unlock()
			c.eventQueue.Push(NewQREvent(item.Code))
			c.log.Infof("QR code updated")

		case "success":
			c.mu.Lock()
			c.currentQR = ""
			c.mu.Unlock()
			c.log.Infof("QR pairing successful")
			// Don't emit pairing.success here - wait for PairSuccess event

		case "timeout":
			c.mu.Lock()
			c.currentQR = ""
			c.mu.Unlock()
			c.log.Warnf("QR code timed out")
			c.eventQueue.Push(NewQRExpiredEvent())
			c.eventQueue.Push(NewPairingFailedEvent("QR code expired"))
			atomic.StoreInt32(&c.state, int32(StateDisconnected))

		case "error":
			c.mu.Lock()
			c.currentQR = ""
			c.mu.Unlock()
			c.log.Errorf("QR pairing error")
			c.eventQueue.Push(NewPairingFailedEvent("QR authentication error"))
			atomic.StoreInt32(&c.state, int32(StateDisconnected))
		}
	}
}

// connectExisting connects using an existing session.
func (c *Client) connectExisting() error {
	c.log.Infof("Connecting with existing session")
	
	err := c.client.Connect()
	if err != nil {
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		return WrapError(ErrCodeConnectionFailed, "Failed to connect", err)
	}
	
	// State will be updated to Connected when we receive the Connected event
	return nil
}

// Disconnect closes the WhatsApp connection.
// IDEMPOTENT: Safe to call multiple times.
func (c *Client) Disconnect() {
	c.disconnectMu.Lock()
	defer c.disconnectMu.Unlock()

	currentState := ClientState(atomic.LoadInt32(&c.state))
	if currentState == StateDisconnected {
		return // Already disconnected
	}

	// Cancel QR context if active
	c.mu.Lock()
	if c.qrCancel != nil {
		c.qrCancel()
		c.qrCancel = nil
	}
	c.currentQR = ""
	c.mu.Unlock()

	// Disconnect client
	if c.client != nil {
		c.client.Disconnect()
	}

	atomic.StoreInt32(&c.state, int32(StateDisconnected))
	atomic.StoreInt32(&c.pairingActive, 0)
}

// handleEvent processes events from WhatsMeow.
func (c *Client) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		c.log.Infof("Connected to WhatsApp")
		atomic.StoreInt32(&c.state, int32(StateConnected))
		c.eventQueue.Push(NewConnectionOpenEvent())

		// Emit logged in event with user info
		if c.client.Store.ID != nil {
			c.eventQueue.Push(NewLoggedInEvent(
				c.client.Store.ID.String(),
				c.client.Store.PushName,
				c.client.Store.Platform,
				false,
			))
		}

	case *events.Disconnected:
		c.log.Warnf("Disconnected from WhatsApp")
		// Don't set state to disconnected immediately - auto-reconnect may kick in
		c.eventQueue.Push(NewConnectionClosedEvent("disconnected"))

	case *events.LoggedOut:
		c.log.Warnf("Logged out from WhatsApp")
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		c.mu.Lock()
		c.currentQR = ""
		c.mu.Unlock()
		c.eventQueue.Push(NewLoggedOutEvent())
		c.eventQueue.Push(NewConnectionClosedEvent("logged_out"))

	case *events.StreamReplaced:
		c.log.Warnf("Stream replaced (connected from another device)")
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		c.eventQueue.Push(NewConnectionClosedEvent("stream_replaced"))

	case *events.Message:
		c.handleMessage(v)

	case *events.Receipt:
		// Handle message receipts (delivered, read, etc.)
		// TODO: Implement receipt events

	case *events.Presence:
		// Handle presence updates
		// TODO: Implement presence events

	case *events.HistorySync:
		c.eventQueue.Push(NewEvent(EventHistorySync, &HistorySyncData{
			Progress: 0, // Progress field removed from whatsmeow API
		}))

	case *events.PairSuccess:
		c.log.Infof("Device pairing successful: %s", v.ID.String())
		atomic.StoreInt32(&c.state, int32(StateConnected))
		c.eventQueue.Push(NewPairingSuccessEvent(
			v.ID.String(),
			v.BusinessName,
			v.Platform,
		))
		c.eventQueue.Push(NewLoggedInEvent(
			v.ID.String(),
			v.BusinessName,
			v.Platform,
			true,
		))

	case *events.PairError:
		c.log.Errorf("Device pairing failed: %v", v.Error.Error())
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		c.eventQueue.Push(NewPairingFailedEvent(v.Error.Error()))
		
	case *events.KeepAliveTimeout:
		c.log.Warnf("Keep-alive timeout, connection may be unstable")
		atomic.StoreInt32(&c.state, int32(StateReconnecting))
		c.eventQueue.Push(NewReconnectingEvent())
		
	case *events.KeepAliveRestored:
		c.log.Infof("Keep-alive restored")
		atomic.StoreInt32(&c.state, int32(StateConnected))
		c.eventQueue.Push(NewConnectionOpenEvent())
	}
}

// handleMessage processes incoming messages.
func (c *Client) handleMessage(msg *events.Message) {
	info := msg.Info

	// Extract text content
	text := ""
	if msg.Message.GetConversation() != "" {
		text = msg.Message.GetConversation()
	} else if msg.Message.GetExtendedTextMessage() != nil {
		text = msg.Message.GetExtendedTextMessage().GetText()
	}

	// Check for media
	hasMedia := false
	mediaType := ""
	mediaMimeType := ""

	if msg.Message.GetImageMessage() != nil {
		hasMedia = true
		mediaType = "image"
		mediaMimeType = msg.Message.GetImageMessage().GetMimetype()
	} else if msg.Message.GetVideoMessage() != nil {
		hasMedia = true
		mediaType = "video"
		mediaMimeType = msg.Message.GetVideoMessage().GetMimetype()
	} else if msg.Message.GetAudioMessage() != nil {
		hasMedia = true
		mediaType = "audio"
		mediaMimeType = msg.Message.GetAudioMessage().GetMimetype()
	} else if msg.Message.GetDocumentMessage() != nil {
		hasMedia = true
		mediaType = "document"
		mediaMimeType = msg.Message.GetDocumentMessage().GetMimetype()
	} else if msg.Message.GetStickerMessage() != nil {
		hasMedia = true
		mediaType = "sticker"
		mediaMimeType = msg.Message.GetStickerMessage().GetMimetype()
	}

	// Handle quoted messages
	quotedID := ""
	quotedText := ""
	if extMsg := msg.Message.GetExtendedTextMessage(); extMsg != nil {
		if ctx := extMsg.GetContextInfo(); ctx != nil {
			quotedID = ctx.GetStanzaID()
			if ctx.GetQuotedMessage() != nil {
				if ctx.GetQuotedMessage().GetConversation() != "" {
					quotedText = ctx.GetQuotedMessage().GetConversation()
				} else if ctx.GetQuotedMessage().GetExtendedTextMessage() != nil {
					quotedText = ctx.GetQuotedMessage().GetExtendedTextMessage().GetText()
				}
			}
		}
	}

	// Determine sender info
	senderJID := info.Sender.String()
	senderName := info.PushName
	chatJID := info.Chat.String()
	isGroup := info.IsGroup

	msgData := &MessageData{
		ID:            info.ID,
		ChatJID:       chatJID,
		SenderJID:     senderJID,
		SenderName:    senderName,
		Text:          text,
		Timestamp:     info.Timestamp.UnixMilli(),
		IsFromMe:      info.IsFromMe,
		IsGroup:       isGroup,
		QuotedID:      quotedID,
		QuotedText:    quotedText,
		HasMedia:      hasMedia,
		MediaType:     mediaType,
		MediaMimeType: mediaMimeType,
	}

	c.eventQueue.Push(NewMessageReceivedEvent(msgData))
}

// GetState returns the current client state.
func (c *Client) GetState() ClientState {
	return ClientState(atomic.LoadInt32(&c.state))
}

// GetCurrentQR returns the current QR code string.
// Returns empty string if no QR is active.
func (c *Client) GetCurrentQR() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentQR
}

// IsLoggedIn returns true if the client is authenticated and connected.
func (c *Client) IsLoggedIn() bool {
	state := ClientState(atomic.LoadInt32(&c.state))
	return c.client != nil && c.client.Store.ID != nil && state == StateConnected
}

// IsPaired returns true if the device is paired (session exists).
// This works even when disconnected.
func (c *Client) IsPaired() bool {
	return c.storage.IsPaired()
}

// IsConnected returns true if currently connected to WhatsApp servers.
func (c *Client) IsConnected() bool {
	return ClientState(atomic.LoadInt32(&c.state)) == StateConnected
}

// IsPairingActive returns true if QR pairing is in progress.
func (c *Client) IsPairingActive() bool {
	return atomic.LoadInt32(&c.pairingActive) == 1
}

// GetClient returns the underlying WhatsMeow client.
// Use with caution - prefer using wrapper methods.
func (c *Client) GetClient() *whatsmeow.Client {
	return c.client
}

// GetJID returns the current user's JID if logged in.
func (c *Client) GetJID() *types.JID {
	if c.client != nil && c.client.Store.ID != nil {
		return c.client.Store.ID
	}
	return nil
}

// WaitForConnection waits for the client to connect with timeout.
// Returns nil if connected, error on timeout.
func (c *Client) WaitForConnection(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.IsConnected() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return NewError(ErrCodeConnectionTimeout, "Connection timeout")
}
