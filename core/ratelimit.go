// Package core provides the WhatsApp engine implementation.
// This file implements message rate limiting to avoid WhatsApp bans.
//
// ANTI-BAN STRATEGY:
// WhatsApp monitors message frequency, timing patterns, and burst behavior.
// A real human types and sends messages with natural variance - never at
// machine-perfect intervals. We simulate this with:
//
//  1. Token bucket: limits sustained throughput (default: ~20 msg/min burst,
//     refills at ~10/min for sustained use)
//  2. Minimum interval: no two messages faster than 800ms apart
//  3. Jitter: ±30% random variance on all wait times
//  4. Typing simulation: optional pre-send typing indicator with realistic delay
//
// USAGE:
//
//	limiter := NewRateLimiter(DefaultRateLimiterConfig())
//	limiter.Wait()              // call before every send
//	limiter.WaitWithTyping(...)  // call when you want to simulate typing first
package core

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// RateLimiterConfig controls rate limiter behavior.
type RateLimiterConfig struct {
	// BurstSize is the max messages that can be sent in rapid succession.
	BurstSize int

	// SustainedPerMinute is the long-term messages-per-minute limit.
	// Refill rate = SustainedPerMinute / 60 tokens/sec.
	SustainedPerMinute float64

	// MinInterval is the minimum time between any two messages.
	MinInterval time.Duration

	// JitterFactor is the ±fraction of random variance on waits (0.0–1.0).
	// 0.3 = ±30% variance. Mimics human timing imprecision.
	JitterFactor float64

	// TypingCPS is chars-per-second used to compute simulated typing delay.
	// Real humans average ~40 WPM ≈ 4–5 chars/sec but with variance.
	TypingCPS float64

	// MaxTypingDelay caps the typing simulation delay.
	MaxTypingDelay time.Duration
}

// DefaultRateLimiterConfig returns safe defaults for personal/low-volume use.
// Tune SustainedPerMinute and BurstSize for higher-volume legitimate use cases.
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		BurstSize:          8,
		SustainedPerMinute: 10,
		MinInterval:        900 * time.Millisecond,
		JitterFactor:       0.30,
		TypingCPS:          5.5, // ~40 WPM
		MaxTypingDelay:     4 * time.Second,
	}
}

// AggressiveRateLimiterConfig is for high-volume use - still safer than no limits,
// but more likely to attract scrutiny on fresh or unverified accounts.
func AggressiveRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		BurstSize:          20,
		SustainedPerMinute: 30,
		MinInterval:        300 * time.Millisecond,
		JitterFactor:       0.20,
		TypingCPS:          8.0,
		MaxTypingDelay:     2 * time.Second,
	}
}

// RateLimiter enforces message timing limits to avoid WhatsApp bans.
// All methods are thread-safe.
type RateLimiter struct {
	mu           sync.Mutex
	cfg          RateLimiterConfig
	tokens       float64
	lastRefill   time.Time
	lastSendTime time.Time
	refillPerSec float64 // tokens per second
}

// NewRateLimiter creates a rate limiter with the given config.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	return &RateLimiter{
		cfg:          cfg,
		tokens:       float64(cfg.BurstSize), // start with full burst capacity
		lastRefill:   time.Now(),
		refillPerSec: cfg.SustainedPerMinute / 60.0,
	}
}

// Wait blocks until it's safe to send the next message.
// Call this before every SendText / SendImage / etc.
func (r *RateLimiter) Wait() {
	r.WaitContext(context.Background())
}

// WaitContext is like Wait but respects context cancellation.
func (r *RateLimiter) WaitContext(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Enforce minimum inter-message interval (with jitter)
	minWait := r.jitter(r.cfg.MinInterval)
	sinceLastSend := time.Since(r.lastSendTime)
	if sinceLastSend < minWait {
		sleepFor := minWait - sinceLastSend
		r.mu.Unlock()
		select {
		case <-time.After(sleepFor):
		case <-ctx.Done():
			r.mu.Lock()
			return
		}
		r.mu.Lock()
	}

	// 2. Refill token bucket based on elapsed time
	now := time.Now()
	elapsed := now.Sub(r.lastRefill).Seconds()
	r.tokens += elapsed * r.refillPerSec
	if r.tokens > float64(r.cfg.BurstSize) {
		r.tokens = float64(r.cfg.BurstSize)
	}
	r.lastRefill = now

	// 3. Wait for a token (blocking with jitter)
	for r.tokens < 1.0 {
		// How long until we have a token?
		deficit := 1.0 - r.tokens
		baseWait := time.Duration(deficit/r.refillPerSec*float64(time.Second))
		sleepFor := r.jitter(baseWait)

		r.mu.Unlock()
		select {
		case <-time.After(sleepFor):
		case <-ctx.Done():
			r.mu.Lock()
			return
		}
		r.mu.Lock()

		// Re-refill after sleep
		now = time.Now()
		elapsed = now.Sub(r.lastRefill).Seconds()
		r.tokens += elapsed * r.refillPerSec
		if r.tokens > float64(r.cfg.BurstSize) {
			r.tokens = float64(r.cfg.BurstSize)
		}
		r.lastRefill = now
	}

	r.tokens--
	r.lastSendTime = time.Now()
}

// SimulateTypingDelay returns the duration a typing indicator should show
// before sending a message of the given text length.
// This makes message timing look human - longer texts take longer to type.
func (r *RateLimiter) SimulateTypingDelay(textLen int) time.Duration {
	if textLen <= 0 {
		return 0
	}

	// Base: time to type at configured CPS
	baseMs := float64(textLen) / r.cfg.TypingCPS * 1000
	base := time.Duration(baseMs) * time.Millisecond

	// Apply jitter
	delay := r.jitter(base)

	// Cap at max
	if delay > r.cfg.MaxTypingDelay {
		delay = r.jitter(r.cfg.MaxTypingDelay)
	}

	// Floor: always at least 400ms even for short messages
	if delay < 400*time.Millisecond {
		delay = r.jitter(400 * time.Millisecond)
	}

	return delay
}

// jitter applies ±JitterFactor random variance to a duration.
// Example: 1000ms with 0.3 factor → 700–1300ms range.
func (r *RateLimiter) jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	factor := r.cfg.JitterFactor
	// rand.Float64() in [0,1), shift to [-1, 1)
	variance := (rand.Float64()*2 - 1) * factor
	jittered := float64(d) * (1 + variance)
	if jittered < 0 {
		jittered = 0
	}
	return time.Duration(jittered)
}

// UpdateLastSend records that a message was just sent externally.
// Call this if you send a message bypassing Wait() (e.g., receipts, presence).
func (r *RateLimiter) UpdateLastSend() {
	r.mu.Lock()
	r.lastSendTime = time.Now()
	r.mu.Unlock()
}
