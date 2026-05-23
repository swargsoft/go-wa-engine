// Package core — session manager extensions for anti-ban support.
// Add these methods to session.go, or keep as a separate file.
// They expose the Engine.MarkActive() call through the SessionManager API.
package core

// MarkActiveSession signals that the user is actively using a session.
// This keeps the WhatsApp presence "online" and resets the idle timer.
// Call this from your UI polling loop while the session is in foreground.
func (sm *SessionManager) MarkActiveSession(sessionName string) {
	sm.mu.RLock()
	engine, exists := sm.sessions[sessionName]
	sm.mu.RUnlock()
	if exists {
		engine.MarkActive()
	}
}

// SetRateLimiterForSession replaces the rate limiter config for a specific session.
// Useful for adjusting send speed per-account (e.g., business vs personal).
func (sm *SessionManager) SetRateLimiterForSession(sessionName string, cfg RateLimiterConfig) error {
	sm.mu.RLock()
	engine, exists := sm.sessions[sessionName]
	sm.mu.RUnlock()
	if !exists {
		return ErrSessionNotFound
	}
	engine.SetRateLimiterConfig(cfg)
	return nil
}

// SetPresenceForSession replaces the presence config for a specific session.
func (sm *SessionManager) SetPresenceForSession(sessionName string, cfg PresenceConfig) error {
	sm.mu.RLock()
	engine, exists := sm.sessions[sessionName]
	sm.mu.RUnlock()
	if !exists {
		return ErrSessionNotFound
	}
	engine.SetPresenceConfig(cfg)
	return nil
}

// --- these already exist in session.go but listed here for reference ---
// StartSession, StopSession, PollEventSession, SendTextSession,
// SendImageWithCaptionSession, GetQRSession, GetSessionInfo, GetAllSessionsInfo,
// ListSessions, GetSessionCount, IsPairedSession, IsConnectedSession, StopAll
