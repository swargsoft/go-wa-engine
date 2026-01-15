// Package waengine provides the public API for the WhatsApp engine.
// This package exposes gomobile-compatible functions for Android/iOS bindings.
//
// IMPORTANT: This package is designed for gomobile bind compatibility.
// All public functions use only gomobile-supported types:
// - Primitive types (string, int, bool, float64)
// - []byte (as byte arrays)
// - error (returned as exceptions on mobile)
//
// Complex data is serialized as JSON strings.
//
// MULTI-SESSION SUPPORT (v1.2.0):
// - Support for up to 5 concurrent WhatsApp accounts
// - Each session has its own SQLite database, event queue, and lifecycle
// - Sessions identified by sessionName string parameter
// - Storage layout: dataDir/sessionName/wa.db
//
// LIFECYCLE:
// - Call Init(dataDir) once on app start
// - Existing sessions are auto-discovered from disk
// - Use Start(sessionName) / StartPairing(sessionName) per session
// - Poll PollEvent(sessionName) for each session's events
// - Call StopAll() before app termination
//
// APP RESTART HANDLING:
// - Sessions persist in SQLite (dataDir/sessionName/wa.db)
// - On restart: Init(dataDir) auto-discovers existing sessions
// - Call Start(sessionName) to reconnect paired sessions
// - No re-pairing needed unless session invalidated
//
// BACKWARD COMPATIBILITY:
// - Legacy single-session functions still work using "default" session
// - NewEngine() -> Init() with "default" session
// - Start() -> Start("default")
//
// THREAD SAFETY:
// - All functions are thread-safe
// - Safe to call from any thread/goroutine
package waengine

import (
	"encoding/json"
	"sync"

	"github.com/mml/wa-engine/core"
)

// Global session manager with mutex for thread-safety.
var (
	sessionMgr *core.SessionManager
	managerMu  sync.RWMutex
)

// Version of the SDK
const Version = "1.2.0"

// MaxSessions is the maximum number of concurrent WhatsApp sessions.
const MaxSessions = core.MaxSessions // 5

// DefaultSession is the session name used for backward-compatible single-session APIs.
const DefaultSession = "default"

// =====================================================================
// MULTI-SESSION API (PRIMARY)
// =====================================================================

// ----- Initialization -----

// Init initializes the WhatsApp session manager with the specified data directory.
// This must be called before any other function.
// dataDir should be an app-private directory with write permissions.
//
// IDEMPOTENT: Safe to call multiple times with same dataDir.
// On app restart, this auto-discovers existing sessions from disk.
//
// Example:
//
//	err := waengine.Init("/data/data/com.example.app/files/whatsapp")
func Init(dataDir string) error {
	managerMu.Lock()
	defer managerMu.Unlock()

	// Already initialized
	if sessionMgr != nil {
		return nil
	}

	sm, err := core.NewSessionManager(dataDir)
	if err != nil {
		return err
	}

	sessionMgr = sm
	return nil
}

// ----- Connection Lifecycle (Multi-Session) -----

// StartSession initiates connection for a named session.
// If session doesn't exist, creates it first.
// IDEMPOTENT: Safe to call multiple times.
//
// Behavior:
// - If already running, returns nil (no-op)
// - If session is paired (has stored auth), auto-reconnects
// - If not paired, returns error - use StartPairingSession() instead
//
// Example:
//
//	waengine.Init(dataDir)
//	err := waengine.StartSession("work")
func StartSession(sessionName string) error {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return core.ErrNotInitialized
	}
	return sm.Start(sessionName)
}

// StartPairingSession initiates QR code authentication for a named session.
// Creates the session if it doesn't exist.
// Returns error if max sessions (5) exceeded.
//
// Flow:
// 1. Call StartPairingSession(sessionName)
// 2. Poll PollEventSession(sessionName) for qr.updated events
// 3. Display QR code to user
// 4. Wait for pairing.success or pairing.failed event
// 5. On success, session is persisted automatically
//
// Example:
//
//	if !waengine.IsPairedSession("work") {
//	    err := waengine.StartPairingSession("work")
//	    // Poll for qr.updated events
//	}
func StartPairingSession(sessionName string) error {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return core.ErrNotInitialized
	}
	return sm.StartPairing(sessionName)
}

// StopSession gracefully closes connection for a named session.
// IDEMPOTENT: Safe to call multiple times.
// The session can be restarted later with StartSession().
func StopSession(sessionName string) error {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return core.ErrNotInitialized
	}
	return sm.Stop(sessionName)
}

// StopAll stops all active sessions.
// Call this before app termination for graceful shutdown.
func StopAll() {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm != nil {
		sm.StopAll()
	}
}

// RemoveSession stops and permanently deletes a session.
// WARNING: This deletes the session's SQLite database.
// The session data cannot be recovered after removal.
func RemoveSession(sessionName string) error {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return core.ErrNotInitialized
	}
	return sm.RemoveSession(sessionName)
}

// ----- Status Methods (Multi-Session) -----

// IsPairedSession checks if a session has stored authentication.
// Works across app restarts - checks SQLite database.
func IsPairedSession(sessionName string) bool {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return false
	}
	return sm.IsPaired(sessionName)
}

// IsConnectedSession checks if a session is currently connected.
func IsConnectedSession(sessionName string) bool {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return false
	}
	return sm.IsConnected(sessionName)
}

// GetQRSession returns the current QR code for a session.
// Returns empty string if no QR available or session not found.
func GetQRSession(sessionName string) string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return ""
	}
	return sm.GetQR(sessionName)
}

// GetJIDSession returns the JID for a session if paired.
func GetJIDSession(sessionName string) string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return ""
	}
	return sm.GetJID(sessionName)
}

// GetStateSession returns the lifecycle state of a session.
// Possible values: "stopped", "starting", "running", "stopping", "pairing", "not_found"
func GetStateSession(sessionName string) string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return "not_initialized"
	}
	return sm.GetState(sessionName)
}

// GetConnectionStateSession returns the connection state of a session.
// Possible values: "disconnected", "connecting", "connected", "reconnecting", "pairing", "not_found"
func GetConnectionStateSession(sessionName string) string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return "not_initialized"
	}
	return sm.GetConnectionState(sessionName)
}

// ----- Messaging (Multi-Session) -----

// SendTextSession sends a text message via the specified session.
// Returns the message ID on success.
//
// to: recipient JID (use FormatPhoneJID or FormatGroupJID)
// sessionName: which WhatsApp account to send from
// text: message content
func SendTextSession(to string, sessionName string, text string) (string, error) {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return "", core.ErrNotInitialized
	}
	return sm.SendText(to, sessionName, text)
}

// SendMessageSession sends a text message via the specified session.
// This is an alias for SendTextSession with the preferred parameter order.
func SendMessageSession(to string, sessionName string, message string) (string, error) {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return "", core.ErrNotInitialized
	}
	return sm.SendMessage(to, sessionName, message)
}

// SendTextReplySession sends a reply message via the specified session.
func SendTextReplySession(to string, sessionName string, text string, quotedID string) (string, error) {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return "", core.ErrNotInitialized
	}
	return sm.SendTextReply(to, sessionName, text, quotedID)
}

// SendImageWithCaptionSession sends an image with caption via the specified session.
// imageSource: URL (http/https) or base64 data URI
//
// Example with URL:
//
//	msgID, err := waengine.SendImageWithCaptionSession(jid, "work", "https://example.com/image.jpg", "Caption")
//
// Example with base64:
//
//	msgID, err := waengine.SendImageWithCaptionSession(jid, "work", "data:image/jpeg;base64,/9j...", "Caption")
func SendImageWithCaptionSession(to string, sessionName string, imageSource string, caption string) (string, error) {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return "", core.ErrNotInitialized
	}
	return sm.SendImageWithCaption(to, sessionName, imageSource, caption)
}

// ----- Events (Multi-Session) -----

// PollEventSession retrieves the next event from a session's queue.
// Returns empty string if no events available or session not found.
// NON-BLOCKING. Recommended polling interval: 50-100ms per session.
//
// Event types include: qr.updated, qr.expired, pairing.success, pairing.failed,
// connection.open, connection.closed, message.received, message.sent, etc.
func PollEventSession(sessionName string) string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return ""
	}
	return sm.PollEvent(sessionName)
}

// GetEventQueueSizeSession returns pending event count for a session.
func GetEventQueueSizeSession(sessionName string) int {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return 0
	}
	return sm.GetEventQueueSize(sessionName)
}

// GetEventQueueStatsSession returns queue statistics for a session as JSON.
func GetEventQueueStatsSession(sessionName string) string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return `{"error":"not_initialized"}`
	}
	return sm.GetEventQueueStats(sessionName)
}

// ClearEventsSession clears all pending events for a session.
func ClearEventsSession(sessionName string) {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm != nil {
		sm.ClearEvents(sessionName)
	}
}

// ----- Session Management -----

// LogoutSession logs out and clears authentication for a session.
// The session will need to be re-paired via StartPairingSession().
func LogoutSession(sessionName string) error {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return core.ErrNotInitialized
	}
	return sm.Logout(sessionName)
}

// ----- Utility Methods (Multi-Session) -----

// SetTypingSession sends typing indicator via the specified session.
func SetTypingSession(to string, sessionName string, typing bool) error {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return core.ErrNotInitialized
	}
	return sm.SetTyping(to, sessionName, typing)
}

// MarkReadSession marks messages as read via the specified session.
func MarkReadSession(chatJID string, senderJID string, sessionName string, messageIDsJSON string) error {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return core.ErrNotInitialized
	}
	return sm.MarkRead(chatJID, senderJID, sessionName, messageIDsJSON)
}

// ----- Session Info -----

// ListSessions returns a JSON array of all session names.
func ListSessions() string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return "[]"
	}
	sessions := sm.ListSessions()
	data, _ := json.Marshal(sessions)
	return string(data)
}

// GetSessionCount returns the number of active sessions.
func GetSessionCount() int {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return 0
	}
	return sm.GetSessionCount()
}

// GetSessionInfo returns detailed info about a session as JSON.
func GetSessionInfo(sessionName string) string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return `{"error":"not_initialized"}`
	}
	return sm.GetSessionInfoJSON(sessionName)
}

// GetAllSessionsInfo returns info for all sessions as JSON.
func GetAllSessionsInfo() string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm == nil {
		return `{"sessions":[],"count":0,"max_sessions":5}`
	}
	return sm.GetAllSessionsInfo()
}

// =====================================================================
// BACKWARD-COMPATIBLE SINGLE-SESSION API (uses "default" session)
// =====================================================================

// NewEngine initializes the WhatsApp engine with a single "default" session.
// DEPRECATED: Use Init() for multi-session support.
// Kept for backward compatibility with existing code.
func NewEngine(dataDir string) error {
	return Init(dataDir)
}

// Start initiates the WhatsApp connection for the default session.
// DEPRECATED: Use StartSession(sessionName) for multi-session.
func Start() error {
	return StartSession(DefaultSession)
}

// StartPairing initiates QR authentication for the default session.
// DEPRECATED: Use StartPairingSession(sessionName) for multi-session.
func StartPairing() error {
	return StartPairingSession(DefaultSession)
}

// Stop closes the default session connection.
// DEPRECATED: Use StopSession(sessionName) for multi-session.
func Stop() {
	_ = StopSession(DefaultSession)
}

// IsPaired checks if the default session is paired.
// DEPRECATED: Use IsPairedSession(sessionName) for multi-session.
func IsPaired() bool {
	return IsPairedSession(DefaultSession)
}

// IsConnected checks if the default session is connected.
// DEPRECATED: Use IsConnectedSession(sessionName) for multi-session.
func IsConnected() bool {
	return IsConnectedSession(DefaultSession)
}

// IsLoggedIn is an alias for IsConnected() for backward compatibility.
func IsLoggedIn() bool {
	return IsConnected()
}

// GetQR returns the QR code for the default session.
// DEPRECATED: Use GetQRSession(sessionName) for multi-session.
func GetQR() string {
	return GetQRSession(DefaultSession)
}

// GetState returns the state of the default session.
// DEPRECATED: Use GetStateSession(sessionName) for multi-session.
func GetState() string {
	return GetStateSession(DefaultSession)
}

// GetConnectionState returns the connection state of the default session.
// DEPRECATED: Use GetConnectionStateSession(sessionName) for multi-session.
func GetConnectionState() string {
	return GetConnectionStateSession(DefaultSession)
}

// GetJID returns the JID for the default session.
// DEPRECATED: Use GetJIDSession(sessionName) for multi-session.
func GetJID() string {
	return GetJIDSession(DefaultSession)
}

// HasSession checks if the default session has stored auth.
// DEPRECATED: Use IsPairedSession(sessionName) for multi-session.
func HasSession() bool {
	return IsPaired()
}

// SendText sends a text message via the default session.
// DEPRECATED: Use SendTextSession(to, sessionName, text) for multi-session.
func SendText(jid string, text string) (string, error) {
	return SendTextSession(jid, DefaultSession, text)
}

// SendMessage sends a text message via the default session.
// DEPRECATED: Use SendMessageSession(to, sessionName, message) for multi-session.
func SendMessage(to string, sessionName string, message string) (string, error) {
	// For backward compatibility, sessionName is ignored and uses default
	return SendMessageSession(to, DefaultSession, message)
}

// SendTextReply sends a reply via the default session.
// DEPRECATED: Use SendTextReplySession() for multi-session.
func SendTextReply(jid string, text string, quotedID string) (string, error) {
	return SendTextReplySession(jid, DefaultSession, text, quotedID)
}

// SendImageWithCaption sends an image via the default session.
// DEPRECATED: Use SendImageWithCaptionSession() for multi-session.
func SendImageWithCaption(to string, sessionName string, imageSource string, caption string) (string, error) {
	// For backward compatibility, sessionName is ignored and uses default
	return SendImageWithCaptionSession(to, DefaultSession, imageSource, caption)
}

// PollEvent retrieves the next event from the default session.
// DEPRECATED: Use PollEventSession(sessionName) for multi-session.
func PollEvent() string {
	return PollEventSession(DefaultSession)
}

// GetEventQueueSize returns pending events for the default session.
// DEPRECATED: Use GetEventQueueSizeSession() for multi-session.
func GetEventQueueSize() int {
	return GetEventQueueSizeSession(DefaultSession)
}

// GetEventQueueStats returns queue stats for the default session.
// DEPRECATED: Use GetEventQueueStatsSession() for multi-session.
func GetEventQueueStats() string {
	return GetEventQueueStatsSession(DefaultSession)
}

// ClearEvents clears events for the default session.
// DEPRECATED: Use ClearEventsSession() for multi-session.
func ClearEvents() {
	ClearEventsSession(DefaultSession)
}

// Logout logs out the default session.
// DEPRECATED: Use LogoutSession() for multi-session.
func Logout() error {
	return LogoutSession(DefaultSession)
}

// SetTyping sends typing indicator via the default session.
// DEPRECATED: Use SetTypingSession() for multi-session.
func SetTyping(jid string, typing bool) error {
	return SetTypingSession(jid, DefaultSession, typing)
}

// MarkRead marks messages as read via the default session.
// DEPRECATED: Use MarkReadSession() for multi-session.
func MarkRead(chatJID string, senderJID string, messageIDsJSON string) error {
	return MarkReadSession(chatJID, senderJID, DefaultSession, messageIDsJSON)
}

// =====================================================================
// JID UTILITY FUNCTIONS (shared across all sessions)
// =====================================================================

// FormatPhoneJID converts a phone number to a WhatsApp JID.
// Phone number should include country code without '+' or '00' prefix.
//
// Example:
//
//	jid := waengine.FormatPhoneJID("1234567890")
//	// Returns: "1234567890@s.whatsapp.net"
func FormatPhoneJID(phone string) string {
	return core.FormatPhoneToJID(phone)
}

// FormatGroupJID converts a group ID to a WhatsApp group JID.
//
// Example:
//
//	jid := waengine.FormatGroupJID("1234567890-1234567890")
//	// Returns: "1234567890-1234567890@g.us"
func FormatGroupJID(groupID string) string {
	return core.FormatGroupToJID(groupID)
}

// ValidateJID checks if a JID string is valid.
func ValidateJID(jid string) bool {
	return core.IsValidJID(jid)
}

// =====================================================================
// INFO & LIFECYCLE
// =====================================================================

// GetInfo returns session manager information as JSON.
// Includes version, session count, session names, etc.
func GetInfo() string {
	managerMu.RLock()
	sm := sessionMgr
	managerMu.RUnlock()

	if sm != nil {
		return sm.GetInfo()
	}

	info := map[string]interface{}{
		"version":       Version,
		"initialized":   false,
		"session_count": 0,
		"max_sessions":  MaxSessions,
	}
	data, _ := json.Marshal(info)
	return string(data)
}

// Destroy completely shuts down the session manager and all sessions.
// After calling this, Init() must be called again to use the SDK.
func Destroy() {
	managerMu.Lock()
	defer managerMu.Unlock()

	if sessionMgr != nil {
		sessionMgr.Destroy()
		sessionMgr = nil
	}
}
