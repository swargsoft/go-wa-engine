// Package core provides the WhatsApp engine implementation.
// This file manages WhatsApp presence - online/offline/typing state.
//
// WHY PRESENCE MATTERS FOR ANTI-BAN:
// A real browser user goes "online" when the tab is open, shows "typing..."
// before sending messages, then goes offline after idle time. An engine that
// never sends any presence updates looks like a silent bot.
//
// BEHAVIOR:
//   - On Connect → set Available (appear online)
//   - Before send → set Composing in the target chat
//   - After send → set Paused in that chat (stops "typing...")
//   - After IdleTimeout with no activity → set Unavailable (go offline)
//   - On Disconnect → set Unavailable
//
// All presence calls are fire-and-forget; failures are non-fatal.
package core

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// PresenceConfig controls presence management behavior.
type PresenceConfig struct {
	// Enabled toggles the entire presence system.
	// Set false to disable all presence updates (not recommended).
	Enabled bool

	// IdleTimeout is how long after the last activity before going "offline".
	// WhatsApp Web goes offline after ~5 minutes of no tab focus.
	IdleTimeout time.Duration

	// SendOnConnect sends an Available presence immediately on connection.
	SendOnConnect bool

	// SimulateTyping enables sending composing/paused signals before messages.
	SimulateTyping bool
}

// DefaultPresenceConfig returns safe, human-like presence defaults.
func DefaultPresenceConfig() PresenceConfig {
	return PresenceConfig{
		Enabled:        true,
		IdleTimeout:    5 * time.Minute,
		SendOnConnect:  true,
		SimulateTyping: true,
	}
}

// PresenceManager controls online/offline and typing presence for a session.
// Thread-safe. Attach one per Engine/Client.
type PresenceManager struct {
	mu          sync.Mutex
	client      *Client
	cfg         PresenceConfig
	limiter     *RateLimiter
	log         interface{ Infof(string, ...interface{}) }

	// isAvailable tracks whether we've last set ourselves as Available.
	isAvailable int32 // atomic bool

	// idleTimer fires when we've been idle too long → go offline.
	idleTimer *time.Timer

	// stopped signals the manager has been shut down.
	stopped int32 // atomic bool
}

// NewPresenceManager creates a presence manager.
// client: the WA client to send presence through.
// limiter: shared rate limiter (presence doesn't consume tokens but uses jitter).
func NewPresenceManager(client *Client, limiter *RateLimiter, cfg PresenceConfig) *PresenceManager {
	return &PresenceManager{
		client:  client,
		cfg:     cfg,
		limiter: limiter,
	}
}

// OnConnect should be called immediately after a successful WhatsApp connection.
// Sends Available presence to appear online, starts idle timer.
func (p *PresenceManager) OnConnect() {
	if !p.cfg.Enabled || atomic.LoadInt32(&p.stopped) == 1 {
		return
	}
	if p.cfg.SendOnConnect {
		p.setAvailable()
	}
	p.resetIdleTimer()
}

// OnDisconnect should be called when the connection closes.
// Sends Unavailable presence and stops the idle timer.
func (p *PresenceManager) OnDisconnect() {
	if !p.cfg.Enabled {
		return
	}
	p.stopIdleTimer()
	p.setUnavailable()
}

// BeforeSend should be called before sending a message to a JID.
// Sends a "composing" (typing) indicator and waits the typing delay.
// Returns a cancel function - call it or AfterSend() when done.
func (p *PresenceManager) BeforeSend(jidStr string, textLen int) func() {
	p.resetIdleTimer() // activity happened

	if !p.cfg.Enabled || !p.cfg.SimulateTyping {
		return func() {}
	}

	client := p.client
	if client == nil || !client.IsConnected() {
		return func() {}
	}

	// Mark ourselves available if we went idle
	if atomic.LoadInt32(&p.isAvailable) == 0 {
		p.setAvailable()
	}

	// Send typing indicator (non-fatal if it fails)
	jid, err := parseJIDSafe(jidStr)
	if err == nil {
		_ = client.GetClient().SendChatPresence(
			context.Background(), jid,
			types.ChatPresenceComposing,
			types.ChatPresenceMediaText,
		)
	}

	// Sleep for a human-like typing duration
	delay := p.limiter.SimulateTypingDelay(textLen)
	time.Sleep(delay)

	// Return cleanup function
	return func() {
		if jid, err2 := parseJIDSafe(jidStr); err2 == nil {
			_ = client.GetClient().SendChatPresence(
				context.Background(), jid,
				types.ChatPresencePaused,
				types.ChatPresenceMediaText,
			)
		}
	}
}

// AfterSend should be called after a message is successfully sent.
// Resets the idle timer to prevent premature offline transition.
func (p *PresenceManager) AfterSend() {
	p.resetIdleTimer()
}

// MarkActive signals user activity (e.g., polling events, opening chat).
// Resets idle timer. Call from your event polling loop if you want to
// stay "online" while the app is actively in use.
func (p *PresenceManager) MarkActive() {
	if !p.cfg.Enabled || atomic.LoadInt32(&p.stopped) == 1 {
		return
	}
	if atomic.LoadInt32(&p.isAvailable) == 0 {
		p.setAvailable()
	}
	p.resetIdleTimer()
}

// Stop shuts down the presence manager and releases the idle timer.
func (p *PresenceManager) Stop() {
	atomic.StoreInt32(&p.stopped, 1)
	p.stopIdleTimer()
}

// --- internal ---

func (p *PresenceManager) setAvailable() {
	if atomic.CompareAndSwapInt32(&p.isAvailable, 0, 1) {
		client := p.client
		if client != nil && client.IsConnected() {
			_ = client.GetClient().SendPresence(context.Background(), types.PresenceAvailable)
		}
	}
}

func (p *PresenceManager) setUnavailable() {
	if atomic.CompareAndSwapInt32(&p.isAvailable, 1, 0) {
		client := p.client
		if client != nil && client.IsConnected() {
			_ = client.GetClient().SendPresence(context.Background(), types.PresenceUnavailable)
		}
	}
}

func (p *PresenceManager) resetIdleTimer() {
	if !p.cfg.Enabled || p.cfg.IdleTimeout <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idleTimer != nil {
		p.idleTimer.Reset(p.cfg.IdleTimeout)
	} else {
		p.idleTimer = time.AfterFunc(p.cfg.IdleTimeout, func() {
			if atomic.LoadInt32(&p.stopped) == 0 {
				p.setUnavailable()
			}
		})
	}
}

func (p *PresenceManager) stopIdleTimer() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
}

// parseJIDSafe is a nil-safe JID parser helper used internally.
func parseJIDSafe(jidStr string) (types.JID, error) {
	return types.ParseJID(jidStr)
}
