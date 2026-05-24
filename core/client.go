// Package core provides the WhatsApp engine implementation.
// This file handles the WhatsMeow client wrapper.
//
// ANTI-BAN ADDITIONS:
//   - SetPresenceManager(): called on Connect/Disconnect/KeepAlive events
//   - SetBackoff(): backoff.Reset() on successful connect, Next() on reconnect
//   - EnableAutoReconnect + AutoReconnectErrors are set conservatively
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
type ClientState int32

const (
	StateDisconnected ClientState = iota
	StateConnecting
	StateConnected
	StateReconnecting
	StatePairing
)

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

// Client wraps the WhatsMeow client with state management and anti-ban hooks.
type Client struct {
	mu           sync.RWMutex
	client       *whatsmeow.Client
	storage      *Storage
	eventQueue   *EventQueue
	state        int32  // atomic ClientState
	currentQR    string
	qrCtx        context.Context
	qrCancel     context.CancelFunc
	log          waLog.Logger
	pairingActive int32 // atomic bool
	disconnectMu  sync.Mutex

	// Anti-ban hooks (set after construction)
	presence *PresenceManager // may be nil
	backoff  *Backoff         // may be nil
}

// NewClient creates a new WhatsApp client wrapper.
func NewClient(storage *Storage, eventQueue *EventQueue, log waLog.Logger) (*Client, error) {
	device := storage.GetDevice()
	if device == nil {
		return nil, NewError(ErrCodeNotInitialized, "Storage not initialized")
	}

	waClient := whatsmeow.NewClient(device, log)

	// Anti-ban: allow some auto-reconnect but not unlimited hammering.
	// We layer our own backoff on top via the Disconnected event.
	waClient.EnableAutoReconnect = true
	waClient.AutoReconnectErrors = 3 // reduced from 5 - fail faster, back off ourselves

	return &Client{
		client:     waClient,
		storage:    storage,
		eventQueue: eventQueue,
		state:      int32(StateDisconnected),
		log:        log,
	}, nil
}

// SetPresenceManager wires a presence manager into this client.
// Called by Engine after construction.
func (c *Client) SetPresenceManager(p *PresenceManager) {
	c.mu.Lock()
	c.presence = p
	c.mu.Unlock()
}

// SetBackoff wires a backoff tracker into this client.
func (c *Client) SetBackoff(b *Backoff) {
	c.mu.Lock()
	c.backoff = b
	c.mu.Unlock()
}

// Connect establishes a connection to WhatsApp. IDEMPOTENT.
func (c *Client) Connect() error {
	current := ClientState(atomic.LoadInt32(&c.state))
	if current == StateConnected || current == StateConnecting || current == StatePairing {
		return nil
	}
	if !atomic.CompareAndSwapInt32(&c.state, int32(current), int32(StateConnecting)) {
		return c.Connect()
	}

	c.client.AddEventHandler(c.handleEvent)

	if c.storage.IsPaired() {
		return c.connectExisting()
	}

	atomic.StoreInt32(&c.state, int32(StateDisconnected))
	return ErrNotPaired
}

// StartPairing initiates QR code based authentication.
func (c *Client) StartPairing() error {
	current := ClientState(atomic.LoadInt32(&c.state))
	if current == StatePairing {
		return nil
	}
	if current == StateConnected {
		return NewError(ErrCodeAlreadyRunning, "Already connected, cannot start pairing")
	}
	if c.storage.IsPaired() {
		return NewError(ErrCodeAlreadyRunning, "Already paired, use Connect() instead")
	}

	atomic.StoreInt32(&c.state, int32(StatePairing))
	atomic.StoreInt32(&c.pairingActive, 1)

	c.client.AddEventHandler(c.handleEvent)

	c.mu.Lock()
	if c.qrCancel != nil {
		c.qrCancel()
	}
	c.qrCtx, c.qrCancel = context.WithCancel(context.Background())
	ctx := c.qrCtx
	c.mu.Unlock()

	qrChan, err := c.client.GetQRChannel(ctx)
	if err != nil {
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		atomic.StoreInt32(&c.pairingActive, 0)
		return WrapError(ErrCodeConnectionFailed, "Failed to get QR channel", err)
	}

	if err = c.client.Connect(); err != nil {
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		atomic.StoreInt32(&c.pairingActive, 0)
		return WrapError(ErrCodeConnectionFailed, "Failed to connect for pairing", err)
	}

	go c.handleQRChannel(qrChan)
	return nil
}

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

		case "success":
			c.mu.Lock()
			c.currentQR = ""
			c.mu.Unlock()

		case "timeout":
			c.mu.Lock()
			c.currentQR = ""
			c.mu.Unlock()
			c.eventQueue.Push(NewQRExpiredEvent())
			c.eventQueue.Push(NewPairingFailedEvent("QR code expired"))
			atomic.StoreInt32(&c.state, int32(StateDisconnected))

		case "error":
			c.mu.Lock()
			c.currentQR = ""
			c.mu.Unlock()
			c.eventQueue.Push(NewPairingFailedEvent("QR authentication error"))
			atomic.StoreInt32(&c.state, int32(StateDisconnected))
		}
	}
}

func (c *Client) connectExisting() error {
	c.log.Infof("Connecting with existing session")
	if err := c.client.Connect(); err != nil {
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		return WrapError(ErrCodeConnectionFailed, "Failed to connect", err)
	}
	return nil
}

// Disconnect closes the WhatsApp connection. IDEMPOTENT.
func (c *Client) Disconnect() {
	c.disconnectMu.Lock()
	defer c.disconnectMu.Unlock()

	if ClientState(atomic.LoadInt32(&c.state)) == StateDisconnected {
		return
	}

	c.mu.Lock()
	if c.qrCancel != nil {
		c.qrCancel()
		c.qrCancel = nil
	}
	c.currentQR = ""
	c.mu.Unlock()

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

		// Anti-ban: reset backoff on successful connect
		c.mu.RLock()
		b := c.backoff
		p := c.presence
		c.mu.RUnlock()
		if b != nil {
			b.Reset()
		}
		// Anti-ban: go "online" now
		if p != nil {
			p.OnConnect()
		}

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
		c.eventQueue.Push(NewConnectionClosedEvent("disconnected"))

		// Anti-ban: apply backoff before allowing reconnect
		c.mu.RLock()
		b := c.backoff
		p := c.presence
		c.mu.RUnlock()
		if p != nil {
			p.OnDisconnect()
		}
		if b != nil {
			delay, ok := b.Next()
			if ok && delay > 0 {
				c.log.Infof("Reconnect backoff: waiting %v (attempt %d)", delay, b.Attempts())
				time.Sleep(delay)
			}
		}

	case *events.LoggedOut:
		c.log.Warnf("Logged out from WhatsApp")
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		c.mu.Lock()
		c.currentQR = ""
		c.mu.Unlock()
		c.mu.RLock()
		p := c.presence
		c.mu.RUnlock()
		if p != nil {
			p.OnDisconnect()
		}
		c.eventQueue.Push(NewLoggedOutEvent())
		c.eventQueue.Push(NewConnectionClosedEvent("logged_out"))

	case *events.StreamReplaced:
		c.log.Warnf("Stream replaced (connected from another device)")
		atomic.StoreInt32(&c.state, int32(StateDisconnected))
		c.eventQueue.Push(NewConnectionClosedEvent("stream_replaced"))

	case *events.Message:
		c.handleMessage(v)

	case *events.HistorySync:
		c.eventQueue.Push(NewEvent(EventHistorySync, &HistorySyncData{Progress: 0}))

	case *events.PairSuccess:
		c.log.Infof("Device pairing successful: %s", v.ID.String())
		atomic.StoreInt32(&c.state, int32(StateConnected))
		c.eventQueue.Push(NewPairingSuccessEvent(v.ID.String(), v.BusinessName, v.Platform))
		c.eventQueue.Push(NewLoggedInEvent(v.ID.String(), v.BusinessName, v.Platform, true))

		// Anti-ban: go online after successful pairing
		c.mu.RLock()
		p := c.presence
		c.mu.RUnlock()
		if p != nil {
			p.OnConnect()
		}

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
		// Anti-ban: reset backoff - we're healthy again
		c.mu.RLock()
		b := c.backoff
		c.mu.RUnlock()
		if b != nil {
			b.Reset()
		}
	}
}

// handleMessage processes incoming messages.
func (c *Client) handleMessage(msg *events.Message) {
	info := msg.Info

	text := ""
	if msg.Message.GetConversation() != "" {
		text = msg.Message.GetConversation()
	} else if msg.Message.GetExtendedTextMessage() != nil {
		text = msg.Message.GetExtendedTextMessage().GetText()
	}

	hasMedia := false
	mediaType := ""
	mediaMimeType := ""

	switch {
	case msg.Message.GetImageMessage() != nil:
		hasMedia, mediaType = true, "image"
		mediaMimeType = msg.Message.GetImageMessage().GetMimetype()
	case msg.Message.GetVideoMessage() != nil:
		hasMedia, mediaType = true, "video"
		mediaMimeType = msg.Message.GetVideoMessage().GetMimetype()
	case msg.Message.GetAudioMessage() != nil:
		hasMedia, mediaType = true, "audio"
		mediaMimeType = msg.Message.GetAudioMessage().GetMimetype()
	case msg.Message.GetDocumentMessage() != nil:
		hasMedia, mediaType = true, "document"
		mediaMimeType = msg.Message.GetDocumentMessage().GetMimetype()
	case msg.Message.GetStickerMessage() != nil:
		hasMedia, mediaType = true, "sticker"
		mediaMimeType = msg.Message.GetStickerMessage().GetMimetype()
	}

	quotedID, quotedText := "", ""
	if extMsg := msg.Message.GetExtendedTextMessage(); extMsg != nil {
		if ctx := extMsg.GetContextInfo(); ctx != nil {
			quotedID = ctx.GetStanzaID()
			if q := ctx.GetQuotedMessage(); q != nil {
				if q.GetConversation() != "" {
					quotedText = q.GetConversation()
				} else if q.GetExtendedTextMessage() != nil {
					quotedText = q.GetExtendedTextMessage().GetText()
				}
			}
		}
	}

	c.eventQueue.Push(NewMessageReceivedEvent(&MessageData{
		ID:            info.ID,
		ChatJID:       info.Chat.String(),
		SenderJID:     info.Sender.String(),
		SenderName:    info.PushName,
		Text:          text,
		Timestamp:     info.Timestamp.UnixMilli(),
		IsFromMe:      info.IsFromMe,
		IsGroup:       info.IsGroup,
		QuotedID:      quotedID,
		QuotedText:    quotedText,
		HasMedia:      hasMedia,
		MediaType:     mediaType,
		MediaMimeType: mediaMimeType,
	}))
}

// --- Accessors ---

func (c *Client) GetState() ClientState  { return ClientState(atomic.LoadInt32(&c.state)) }
func (c *Client) IsConnected() bool      { return c.GetState() == StateConnected }
func (c *Client) IsPairingActive() bool  { return atomic.LoadInt32(&c.pairingActive) == 1 }
func (c *Client) IsPaired() bool         { return c.storage.IsPaired() }
func (c *Client) GetClient() *whatsmeow.Client { return c.client }

func (c *Client) IsLoggedIn() bool {
	return c.client != nil && c.client.Store.ID != nil && c.GetState() == StateConnected
}

func (c *Client) GetJID() *types.JID {
	if c.client != nil && c.client.Store.ID != nil {
		return c.client.Store.ID
	}
	return nil
}

func (c *Client) GetCurrentQR() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentQR
}

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
