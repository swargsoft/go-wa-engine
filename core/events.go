// Package core provides the WhatsApp engine implementation.
// This file defines the event system for the SDK.
//
// EVENT SYSTEM DESIGN:
// - Events are delivered via a bounded queue with configurable size
// - PollEvent() is non-blocking and returns JSON or empty string
// - Overflow strategy: drop oldest events first, emit event.dropped
// - All events have type, timestamp, and optional data
//
// THREAD SAFETY:
// - EventQueue is fully thread-safe
// - Push() and Poll() can be called from any goroutine
package core

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// EventType represents the type of event emitted by the engine.
type EventType string

const (
	// ----- QR & Pairing Events -----

	// EventQRUpdated is emitted when a new QR code is available for scanning.
	EventQRUpdated EventType = "qr.updated"

	// EventQRExpired is emitted when the QR code expires and needs refresh.
	EventQRExpired EventType = "qr.expired"

	// EventPairingSuccess is emitted when device pairing succeeds.
	EventPairingSuccess EventType = "pairing.success"

	// EventPairingFailed is emitted when device pairing fails.
	EventPairingFailed EventType = "pairing.failed"

	// ----- Connection Events -----

	// EventConnectionOpen is emitted when connected to WhatsApp.
	EventConnectionOpen EventType = "connection.open"

	// EventConnectionClosed is emitted when disconnected from WhatsApp.
	EventConnectionClosed EventType = "connection.closed"

	// EventConnectionReconnecting is emitted during reconnection attempts.
	EventConnectionReconnecting EventType = "connection.reconnecting"

	// ----- Message Events -----

	// EventMessageReceived is emitted when a message is received.
	EventMessageReceived EventType = "message.received"

	// EventMessageSent is emitted when a message is successfully sent.
	EventMessageSent EventType = "message.sent"

	// EventMessageFailed is emitted when a message fails to send.
	EventMessageFailed EventType = "message.failed"

	// ----- Session Events -----

	// EventLoggedIn is emitted when successfully logged in.
	EventLoggedIn EventType = "logged_in"

	// EventLoggedOut is emitted when logged out.
	EventLoggedOut EventType = "logged_out"

	// ----- System Events -----

	// EventError is emitted when an error occurs.
	EventError EventType = "error"

	// EventHistorySync is emitted during history sync.
	EventHistorySync EventType = "history.sync"

	// EventDropped is emitted when events are dropped due to queue overflow.
	// data contains: count (int), oldest_type (string)
	EventDropped EventType = "event.dropped"
)

// Event represents an event emitted by the engine.
// All events are serialized to JSON for cross-language compatibility.
type Event struct {
	Type      EventType   `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
}

// QRData contains QR code information.
type QRData struct {
	Code string `json:"code"`
}

// ConnectionData contains connection state information.
type ConnectionData struct {
	Reason string `json:"reason,omitempty"`
}

// MessageData contains message information.
type MessageData struct {
	ID            string `json:"id"`
	ChatJID       string `json:"chat_jid"`
	SenderJID     string `json:"sender_jid"`
	SenderName    string `json:"sender_name,omitempty"`
	Text          string `json:"text,omitempty"`
	Timestamp     int64  `json:"timestamp"`
	IsFromMe      bool   `json:"is_from_me"`
	IsGroup       bool   `json:"is_group"`
	QuotedID      string `json:"quoted_id,omitempty"`
	QuotedText    string `json:"quoted_text,omitempty"`
	HasMedia      bool   `json:"has_media"`
	MediaType     string `json:"media_type,omitempty"`
	MediaMimeType string `json:"media_mime_type,omitempty"`
}

// ErrorData contains error information.
type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// LoggedInData contains login information.
type LoggedInData struct {
	JID       string `json:"jid"`
	PushName  string `json:"push_name,omitempty"`
	Platform  string `json:"platform,omitempty"`
	IsNewUser bool   `json:"is_new_user"`
}

// HistorySyncData contains history sync progress.
type HistorySyncData struct {
	Progress    int    `json:"progress"`
	MessageType string `json:"message_type,omitempty"`
}

// PairingData contains pairing result information.
type PairingData struct {
	JID      string `json:"jid,omitempty"`
	PushName string `json:"push_name,omitempty"`
	Platform string `json:"platform,omitempty"`
	Reason   string `json:"reason,omitempty"` // For failures
}

// DroppedEventData contains information about dropped events.
type DroppedEventData struct {
	Count      int    `json:"count"`
	OldestType string `json:"oldest_type,omitempty"`
}

// MessageFailedData contains information about a failed message send.
type MessageFailedData struct {
	To      string `json:"to"`
	Text    string `json:"text,omitempty"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// NewEvent creates a new event with the current timestamp.
func NewEvent(eventType EventType, data interface{}) *Event {
	return &Event{
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
}

// ToJSON serializes the event to JSON.
func (e *Event) ToJSON() string {
	data, err := json.Marshal(e)
	if err != nil {
		// Return a minimal error event if serialization fails
		return `{"type":"error","data":{"code":"ERR_INTERNAL","message":"Event serialization failed"}}`
	}
	return string(data)
}

// EventQueue is a thread-safe bounded queue for events.
// It provides non-blocking poll operations for gomobile compatibility.
//
// OVERFLOW BEHAVIOR:
// When the queue is full and Push() is called:
// 1. The oldest event is dropped
// 2. A drop counter is incremented
// 3. Periodically, an event.dropped event is injected with the count
//
// This ensures the consumer is aware of missed events without flooding
// the queue with drop notifications.
type EventQueue struct {
	mu            sync.Mutex
	events        []*Event
	maxSize       int
	dropCount     int64         // Atomic counter for dropped events
	lastDropType  string        // Type of last dropped event
	totalPushed   int64         // Total events ever pushed
	totalPolled   int64         // Total events ever polled
	totalDropped  int64         // Total events ever dropped
}

// EventQueueStats contains queue statistics.
type EventQueueStats struct {
	Size         int   `json:"size"`
	MaxSize      int   `json:"max_size"`
	TotalPushed  int64 `json:"total_pushed"`
	TotalPolled  int64 `json:"total_polled"`
	TotalDropped int64 `json:"total_dropped"`
}

// NewEventQueue creates a new event queue with the specified maximum size.
// Default size is 1000 if maxSize <= 0.
func NewEventQueue(maxSize int) *EventQueue {
	if maxSize <= 0 {
		maxSize = 1000 // Default max size
	}
	return &EventQueue{
		events:  make([]*Event, 0, maxSize),
		maxSize: maxSize,
	}
}

// Push adds an event to the queue.
// If the queue is full, the oldest event is dropped.
// This method is thread-safe and never blocks.
func (q *EventQueue) Push(event *Event) {
	q.mu.Lock()
	defer q.mu.Unlock()

	atomic.AddInt64(&q.totalPushed, 1)

	// Drop oldest event if queue is full
	if len(q.events) >= q.maxSize {
		dropped := q.events[0]
		q.events = q.events[1:]
		q.lastDropType = string(dropped.Type)
		atomic.AddInt64(&q.dropCount, 1)
		atomic.AddInt64(&q.totalDropped, 1)
	}

	q.events = append(q.events, event)
}

// Poll retrieves and removes the next event from the queue.
// Returns nil if the queue is empty (non-blocking).
func (q *EventQueue) Poll() *Event {
	q.mu.Lock()
	defer q.mu.Unlock()

	// First, check if we need to inject a drop notification
	dropCount := atomic.SwapInt64(&q.dropCount, 0)
	if dropCount > 0 {
		// Inject a drop notification event
		dropEvent := NewEvent(EventDropped, &DroppedEventData{
			Count:      int(dropCount),
			OldestType: q.lastDropType,
		})
		atomic.AddInt64(&q.totalPolled, 1)
		return dropEvent
	}

	if len(q.events) == 0 {
		return nil
	}

	event := q.events[0]
	q.events = q.events[1:]
	atomic.AddInt64(&q.totalPolled, 1)
	return event
}

// PollJSON retrieves the next event as a JSON string.
// Returns an empty string if the queue is empty (non-blocking).
// This is the primary method for gomobile compatibility.
func (q *EventQueue) PollJSON() string {
	event := q.Poll()
	if event == nil {
		return ""
	}
	return event.ToJSON()
}

// Size returns the current number of events in the queue.
func (q *EventQueue) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.events)
}

// Clear removes all events from the queue.
// Drop counter is preserved to inform consumer of cleared events.
func (q *EventQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	cleared := len(q.events)
	if cleared > 0 {
		atomic.AddInt64(&q.dropCount, int64(cleared))
		atomic.AddInt64(&q.totalDropped, int64(cleared))
	}
	q.events = q.events[:0]
}

// Stats returns current queue statistics.
func (q *EventQueue) Stats() EventQueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return EventQueueStats{
		Size:         len(q.events),
		MaxSize:      q.maxSize,
		TotalPushed:  atomic.LoadInt64(&q.totalPushed),
		TotalPolled:  atomic.LoadInt64(&q.totalPolled),
		TotalDropped: atomic.LoadInt64(&q.totalDropped),
	}
}

// Helper functions for creating common events.

// NewQREvent creates a QR code event.
func NewQREvent(code string) *Event {
	return NewEvent(EventQRUpdated, &QRData{Code: code})
}

// NewConnectionOpenEvent creates a connection open event.
func NewConnectionOpenEvent() *Event {
	return NewEvent(EventConnectionOpen, nil)
}

// NewConnectionClosedEvent creates a connection closed event.
func NewConnectionClosedEvent(reason string) *Event {
	return NewEvent(EventConnectionClosed, &ConnectionData{Reason: reason})
}

// NewReconnectingEvent creates a reconnecting event.
func NewReconnectingEvent() *Event {
	return NewEvent(EventConnectionReconnecting, nil)
}

// NewMessageReceivedEvent creates a message received event.
func NewMessageReceivedEvent(data *MessageData) *Event {
	return NewEvent(EventMessageReceived, data)
}

// NewMessageSentEvent creates a message sent event.
func NewMessageSentEvent(id, chatJID, text string) *Event {
	return NewEvent(EventMessageSent, &MessageData{
		ID:        id,
		ChatJID:   chatJID,
		Text:      text,
		Timestamp: time.Now().UnixMilli(),
		IsFromMe:  true,
	})
}

// NewErrorEvent creates an error event.
func NewErrorEvent(err *EngineError) *Event {
	return NewEvent(EventError, &ErrorData{
		Code:    string(err.Code),
		Message: err.Message,
		Details: err.Details,
	})
}

// NewLoggedInEvent creates a logged in event.
func NewLoggedInEvent(jid, pushName, platform string, isNewUser bool) *Event {
	return NewEvent(EventLoggedIn, &LoggedInData{
		JID:       jid,
		PushName:  pushName,
		Platform:  platform,
		IsNewUser: isNewUser,
	})
}

// NewLoggedOutEvent creates a logged out event.
func NewLoggedOutEvent() *Event {
	return NewEvent(EventLoggedOut, nil)
}

// ----- Pairing Events -----

// NewQRExpiredEvent creates a QR expired event.
func NewQRExpiredEvent() *Event {
	return NewEvent(EventQRExpired, nil)
}

// NewPairingSuccessEvent creates a pairing success event.
func NewPairingSuccessEvent(jid, pushName, platform string) *Event {
	return NewEvent(EventPairingSuccess, &PairingData{
		JID:      jid,
		PushName: pushName,
		Platform: platform,
	})
}

// NewPairingFailedEvent creates a pairing failed event.
func NewPairingFailedEvent(reason string) *Event {
	return NewEvent(EventPairingFailed, &PairingData{
		Reason: reason,
	})
}

// ----- Message Events -----

// NewMessageFailedEvent creates a message failed event.
func NewMessageFailedEvent(to, text, errorMsg string, code ErrorCode) *Event {
	return NewEvent(EventMessageFailed, &MessageFailedData{
		To:    to,
		Text:  text,
		Error: errorMsg,
		Code:  string(code),
	})
}
