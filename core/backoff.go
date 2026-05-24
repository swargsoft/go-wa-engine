// Package core provides the WhatsApp engine implementation.
// This file implements exponential backoff for reconnection attempts.
//
// WHY BACKOFF MATTERS:
// When WhatsApp disconnects an engine that immediately retries in a tight loop,
// their servers see a flood of connection attempts - a strong bot signal that
// can escalate from a soft block to a permanent ban.
//
// STRATEGY:
//   - First retry: ~2s
//   - Subsequent retries: doubles each time (2 → 4 → 8 → 16 → 32s)
//   - Max cap: 5 minutes (prevents hours-long wait after network outage)
//   - Jitter: ±20% so multiple instances don't reconnect in lockstep
//   - Reset on successful connection
package core

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// BackoffConfig controls reconnection backoff behavior.
type BackoffConfig struct {
	// InitialDelay is the wait before the first retry.
	InitialDelay time.Duration

	// Multiplier grows the delay each attempt (2.0 = double each time).
	Multiplier float64

	// MaxDelay caps the delay so we don't wait forever.
	MaxDelay time.Duration

	// JitterFactor adds ±variance to prevent thundering herd.
	JitterFactor float64

	// MaxAttempts is the max retries before giving up (0 = unlimited).
	MaxAttempts int
}

// DefaultBackoffConfig returns sensible defaults.
func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		InitialDelay: 2 * time.Second,
		Multiplier:   2.0,
		MaxDelay:     5 * time.Minute,
		JitterFactor: 0.20,
		MaxAttempts:  0, // unlimited - let whatsmeow handle final give-up
	}
}

// Backoff tracks reconnection state for a single session.
// Thread-safe.
type Backoff struct {
	mu       sync.Mutex
	cfg      BackoffConfig
	attempts int
}

// NewBackoff creates a new backoff tracker.
func NewBackoff(cfg BackoffConfig) *Backoff {
	return &Backoff{cfg: cfg}
}

// Next returns the duration to wait before the next reconnection attempt
// and increments the attempt counter.
// Returns (delay, true) normally, or (0, false) if MaxAttempts exceeded.
func (b *Backoff) Next() (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cfg.MaxAttempts > 0 && b.attempts >= b.cfg.MaxAttempts {
		return 0, false
	}

	// Compute delay: InitialDelay * Multiplier^attempts
	delay := float64(b.cfg.InitialDelay) * math.Pow(b.cfg.Multiplier, float64(b.attempts))

	// Cap at max
	maxNs := float64(b.cfg.MaxDelay)
	if delay > maxNs {
		delay = maxNs
	}

	// Apply ±jitter
	jitter := (rand.Float64()*2 - 1) * b.cfg.JitterFactor
	delay = delay * (1 + jitter)
	if delay < 0 {
		delay = float64(b.cfg.InitialDelay)
	}

	b.attempts++
	return time.Duration(delay), true
}

// Reset clears the attempt counter after a successful connection.
// Call this from the Connected event handler.
func (b *Backoff) Reset() {
	b.mu.Lock()
	b.attempts = 0
	b.mu.Unlock()
}

// Attempts returns the current attempt count.
func (b *Backoff) Attempts() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.attempts
}
