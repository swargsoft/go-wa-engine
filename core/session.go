// Package core provides the session manager for multi-account WhatsApp support.
//
// DESIGN DECISIONS:
// - SessionManager is the top-level coordinator for multiple WhatsApp accounts
// - Each session has its own Engine instance with isolated:
//   - WhatsMeow client
//   - SQLite database (dataDir/sessionName/wa.db)
//   - Event queue
//   - Lifecycle state
// - Maximum 5 concurrent sessions (hard limit to prevent resource exhaustion)
// - Sessions are identified by user-provided string names
// - Auto-discovery: scans dataDir on init for existing sessions
//
// THREAD SAFETY:
// - RWMutex protects the sessions map
// - Individual Engine instances have their own internal synchronization
// - Safe to call any method from any goroutine
//
// STORAGE LAYOUT:
//
//	dataDir/
//	  ├── sessionA/
//	  │   └── wa.db
//	  ├── sessionB/
//	  │   └── wa.db
//	  └── sessionC/
//	      └── wa.db
package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MaxSessions is the hard limit on concurrent WhatsApp sessions.
// This prevents unbounded resource consumption.
const MaxSessions = 5

// SessionManager coordinates multiple WhatsApp account sessions.
// Thread-safe for concurrent access.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Engine
	dataDir  string

	// initialized prevents double-initialization
	initialized bool
}

// SessionInfo contains metadata about a session for external inspection.
type SessionInfo struct {
	Name            string `json:"name"`
	IsPaired        bool   `json:"is_paired"`
	IsConnected     bool   `json:"is_connected"`
	State           string `json:"state"`
	ConnectionState string `json:"connection_state"`
	JID             string `json:"jid,omitempty"`
	EventQueueSize  int    `json:"event_queue_size"`
}

// NewSessionManager creates a new session manager with the given data directory.
// It automatically discovers and loads existing sessions from disk.
//
// The dataDir should be an app-private directory with write permissions.
// On mobile, this is typically the app's internal storage directory.
func NewSessionManager(dataDir string) (*SessionManager, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory cannot be empty", ErrNotInitialized)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("%w: failed to create data directory: %v", ErrStorageFailed, err)
	}

	sm := &SessionManager{
		sessions:    make(map[string]*Engine),
		dataDir:     dataDir,
		initialized: true,
	}

	// Auto-discover existing sessions from disk
	if err := sm.discoverSessions(); err != nil {
		// Log but don't fail - we can still create new sessions
		// In production, you might want to emit an error event
		fmt.Printf("wa-engine: warning: failed to discover existing sessions: %v\n", err)
	}

	return sm, nil
}

// discoverSessions scans the data directory for existing session databases.
// For each found session, it creates an Engine instance (but doesn't connect).
// This allows seamless app restart with existing sessions.
func (sm *SessionManager) discoverSessions() error {
	entries, err := os.ReadDir(sm.dataDir)
	if err != nil {
		return fmt.Errorf("failed to read data directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionName := entry.Name()
		sessionDir := filepath.Join(sm.dataDir, sessionName)
		dbPath := filepath.Join(sessionDir, "wa.db")

		// Check if wa.db exists in this subdirectory
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			continue // Not a valid session directory
		}

		// Check session limit
		if len(sm.sessions) >= MaxSessions {
			fmt.Printf("wa-engine: warning: skipping session '%s' - max sessions (%d) reached\n",
				sessionName, MaxSessions)
			break
		}

		// Create engine for this session (doesn't connect yet)
		engine, err := NewEngine(sessionDir)
		if err != nil {
			fmt.Printf("wa-engine: warning: failed to load session '%s': %v\n", sessionName, err)
			continue
		}

		sm.sessions[sessionName] = engine
		fmt.Printf("wa-engine: discovered existing session '%s'\n", sessionName)
	}

	return nil
}

// getOrCreateSession gets an existing session or creates a new one.
// Returns error if max sessions exceeded and session doesn't exist.
func (sm *SessionManager) getOrCreateSession(sessionName string) (*Engine, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if session already exists
	if engine, exists := sm.sessions[sessionName]; exists {
		return engine, nil
	}

	// Check session limit before creating new
	if len(sm.sessions) >= MaxSessions {
		return nil, fmt.Errorf("%w: maximum %d sessions allowed, cannot create '%s'",
			ErrMaxSessionsExceeded, MaxSessions, sessionName)
	}

	// Create session directory
	sessionDir := filepath.Join(sm.dataDir, sessionName)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return nil, fmt.Errorf("%w: failed to create session directory: %v",
			ErrStorageFailed, err)
	}

	// Create new engine
	engine, err := NewEngine(sessionDir)
	if err != nil {
		return nil, err
	}

	sm.sessions[sessionName] = engine
	return engine, nil
}

// getSession retrieves an existing session.
// Returns error if session doesn't exist.
func (sm *SessionManager) getSession(sessionName string) (*Engine, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	engine, exists := sm.sessions[sessionName]
	if !exists {
		return nil, fmt.Errorf("%w: '%s'", ErrSessionNotFound, sessionName)
	}
	return engine, nil
}

// ----- Public Session Lifecycle Methods -----

// Start initiates connection for a session.
// If session doesn't exist, creates it first.
// If session is already paired, auto-reconnects.
// If not paired, returns error - use StartPairing() instead.
//
// IDEMPOTENT: Safe to call multiple times.
func (sm *SessionManager) Start(sessionName string) error {
	engine, err := sm.getOrCreateSession(sessionName)
	if err != nil {
		return err
	}
	return engine.Start()
}

// StartPairing initiates QR code authentication for a session.
// Creates the session if it doesn't exist.
//
// Flow:
// 1. Call StartPairing(sessionName)
// 2. Poll PollEvent(sessionName) for qr.updated events
// 3. Display QR code to user
// 4. Wait for pairing.success or pairing.failed
func (sm *SessionManager) StartPairing(sessionName string) error {
	engine, err := sm.getOrCreateSession(sessionName)
	if err != nil {
		return err
	}
	return engine.StartPairing()
}

// Stop gracefully closes connection for a session.
// IDEMPOTENT: Safe to call multiple times.
// Does not remove the session - it can be restarted later.
func (sm *SessionManager) Stop(sessionName string) error {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return err // Session doesn't exist
	}
	engine.Stop()
	return nil
}

// StopAll stops all active sessions.
// Used for graceful app shutdown.
func (sm *SessionManager) StopAll() {
	sm.mu.RLock()
	sessions := make([]*Engine, 0, len(sm.sessions))
	for _, engine := range sm.sessions {
		sessions = append(sessions, engine)
	}
	sm.mu.RUnlock()

	for _, engine := range sessions {
		engine.Stop()
	}
}

// RemoveSession stops and permanently removes a session.
// This deletes the session's SQLite database.
// Use with caution - session data cannot be recovered.
func (sm *SessionManager) RemoveSession(sessionName string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	engine, exists := sm.sessions[sessionName]
	if !exists {
		return fmt.Errorf("%w: '%s'", ErrSessionNotFound, sessionName)
	}

	// Stop the engine
	engine.Stop()

	// Remove from map
	delete(sm.sessions, sessionName)

	// Delete session directory
	sessionDir := filepath.Join(sm.dataDir, sessionName)
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("%w: failed to delete session directory: %v",
			ErrStorageFailed, err)
	}

	return nil
}

// ----- Status Methods -----

// IsPaired checks if a session has a stored authentication.
// Works even when session is not connected.
func (sm *SessionManager) IsPaired(sessionName string) bool {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return false
	}
	return engine.IsPaired()
}

// IsConnected checks if a session is currently connected.
func (sm *SessionManager) IsConnected(sessionName string) bool {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return false
	}
	return engine.IsConnected()
}

// GetQR returns the current QR code for a session.
// Returns empty string if no QR available.
func (sm *SessionManager) GetQR(sessionName string) string {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return ""
	}
	return engine.GetQR()
}

// GetJID returns the JID for a session if paired.
func (sm *SessionManager) GetJID(sessionName string) string {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return ""
	}
	return engine.GetJID()
}

// GetState returns the lifecycle state of a session.
func (sm *SessionManager) GetState(sessionName string) string {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return "not_found"
	}
	return engine.GetState()
}

// GetConnectionState returns the connection state of a session.
func (sm *SessionManager) GetConnectionState(sessionName string) string {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return "not_found"
	}
	return engine.GetConnectionState()
}

// ----- Messaging Methods -----

// SendText sends a text message via the specified session.
func (sm *SessionManager) SendText(to string, sessionName string, text string) (string, error) {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return "", err
	}
	return engine.SendText(to, text)
}

// SendMessage sends a text message via the specified session.
// This is the preferred API matching the signature: SendMessage(to, sessionName, message)
func (sm *SessionManager) SendMessage(to string, sessionName string, message string) (string, error) {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return "", err
	}
	return engine.SendMessage(to, sessionName, message)
}

// SendTextReply sends a reply message via the specified session.
func (sm *SessionManager) SendTextReply(to string, sessionName string, text string, quotedID string) (string, error) {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return "", err
	}
	return engine.SendTextReply(to, text, quotedID)
}

// SendImageWithCaption sends an image with caption via the specified session.
// imageSource can be a URL (http/https) or base64 data URI.
func (sm *SessionManager) SendImageWithCaption(to string, sessionName string, imageSource string, caption string) (string, error) {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return "", err
	}
	return engine.SendImageWithCaption(to, sessionName, imageSource, caption)
}

// ----- Event Methods -----

// PollEvent retrieves the next event from a session's queue.
// Returns empty string if no events available.
// NON-BLOCKING.
func (sm *SessionManager) PollEvent(sessionName string) string {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		// Return error as JSON event
		return sm.errorEventJSON(sessionName, err)
	}
	return engine.PollEvent()
}

// GetEventQueueSize returns pending event count for a session.
func (sm *SessionManager) GetEventQueueSize(sessionName string) int {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return 0
	}
	return engine.GetEventQueueSize()
}

// GetEventQueueStats returns queue statistics for a session as JSON.
func (sm *SessionManager) GetEventQueueStats(sessionName string) string {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return `{"error":"session_not_found"}`
	}
	return engine.GetEventQueueStats()
}

// ClearEvents clears all pending events for a session.
func (sm *SessionManager) ClearEvents(sessionName string) {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return
	}
	engine.ClearEvents()
}

// ----- Utility Methods -----

// SetTyping sends typing indicator via the specified session.
func (sm *SessionManager) SetTyping(to string, sessionName string, typing bool) error {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return err
	}
	return engine.SetTyping(to, typing)
}

// MarkRead marks messages as read via the specified session.
func (sm *SessionManager) MarkRead(chatJID string, senderJID string, sessionName string, messageIDsJSON string) error {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return err
	}
	return engine.MarkRead(chatJID, senderJID, messageIDsJSON)
}

// Logout logs out and clears session data for the specified session.
func (sm *SessionManager) Logout(sessionName string) error {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return err
	}
	return engine.Logout()
}

// ----- Info Methods -----

// ListSessions returns a list of all session names.
func (sm *SessionManager) ListSessions() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	names := make([]string, 0, len(sm.sessions))
	for name := range sm.sessions {
		names = append(names, name)
	}
	return names
}

// GetSessionCount returns the number of active sessions.
func (sm *SessionManager) GetSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// GetSessionInfo returns detailed info about a session.
func (sm *SessionManager) GetSessionInfo(sessionName string) (*SessionInfo, error) {
	engine, err := sm.getSession(sessionName)
	if err != nil {
		return nil, err
	}

	return &SessionInfo{
		Name:            sessionName,
		IsPaired:        engine.IsPaired(),
		IsConnected:     engine.IsConnected(),
		State:           engine.GetState(),
		ConnectionState: engine.GetConnectionState(),
		JID:             engine.GetJID(),
		EventQueueSize:  engine.GetEventQueueSize(),
	}, nil
}

// GetSessionInfoJSON returns session info as JSON string.
func (sm *SessionManager) GetSessionInfoJSON(sessionName string) string {
	info, err := sm.GetSessionInfo(sessionName)
	if err != nil {
		return fmt.Sprintf(`{"error":"%s","session":"%s"}`, ErrSessionNotFound.Error(), sessionName)
	}
	data, _ := json.Marshal(info)
	return string(data)
}

// GetAllSessionsInfo returns info for all sessions as JSON.
func (sm *SessionManager) GetAllSessionsInfo() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	infos := make([]*SessionInfo, 0, len(sm.sessions))
	for name, engine := range sm.sessions {
		infos = append(infos, &SessionInfo{
			Name:            name,
			IsPaired:        engine.IsPaired(),
			IsConnected:     engine.IsConnected(),
			State:           engine.GetState(),
			ConnectionState: engine.GetConnectionState(),
			JID:             engine.GetJID(),
			EventQueueSize:  engine.GetEventQueueSize(),
		})
	}

	result := map[string]interface{}{
		"sessions":     infos,
		"count":        len(infos),
		"max_sessions": MaxSessions,
	}
	data, _ := json.Marshal(result)
	return string(data)
}

// GetInfo returns session manager info as JSON.
func (sm *SessionManager) GetInfo() string {
	sm.mu.RLock()
	count := len(sm.sessions)
	names := make([]string, 0, count)
	for name := range sm.sessions {
		names = append(names, name)
	}
	sm.mu.RUnlock()

	info := map[string]interface{}{
		"version":      "1.2.0",
		"data_dir":     sm.dataDir,
		"session_count": count,
		"max_sessions": MaxSessions,
		"sessions":     names,
	}
	data, _ := json.Marshal(info)
	return string(data)
}

// Destroy stops all sessions and releases resources.
// After calling this, the SessionManager cannot be used.
func (sm *SessionManager) Destroy() {
	sm.StopAll()

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.sessions = nil
	sm.initialized = false
}

// ----- Helper Methods -----

// errorEventJSON creates a JSON error event for session errors.
func (sm *SessionManager) errorEventJSON(sessionName string, err error) string {
	event := map[string]interface{}{
		"type":      "error",
		"timestamp": currentTimeMillis(),
		"data": map[string]interface{}{
			"session": sessionName,
			"code":    "ERR_SESSION_NOT_FOUND",
			"message": err.Error(),
		},
	}
	data, _ := json.Marshal(event)
	return string(data)
}

// currentTimeMillis returns current Unix time in milliseconds.
func currentTimeMillis() int64 {
	return time.Now().UnixMilli()
}
