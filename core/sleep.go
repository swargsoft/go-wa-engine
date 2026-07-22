// Package core - OS-level sleep/wake monitor.
//
// On sleep  → StopAll() so WhatsApp sees a clean disconnect, not a dead socket.
// On wake   → reconnect every session that was running before sleep.
//
// Platform support:
//   macOS   - IOKit power notifications (CGO, zero extra deps)
//   Linux   - polls /sys/power/wakeup counter; upgrades to logind D-Bus if available
//   Windows - RegisterSuspendResumeNotification via syscall
package core

import "time"

// SleepMonitor watches for OS sleep/wake events and manages session lifecycle.
type SleepMonitor struct {
	sm   *SessionManager
	stop chan struct{}
}

// NewSleepMonitor creates a monitor wired to the given SessionManager.
// Call Start() to begin watching.
func NewSleepMonitor(sm *SessionManager) *SleepMonitor {
	return &SleepMonitor{sm: sm, stop: make(chan struct{})}
}

// Stop shuts down the monitor goroutine.
func (m *SleepMonitor) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
}

// onSleep is called by the platform-specific watcher when the system is about to sleep.
func (m *SleepMonitor) onSleep() {
	m.sm.mu.RLock()
	names := make([]string, 0, len(m.sm.sessions))
	for name, eng := range m.sm.sessions {
		if eng.IsConnected() {
			names = append(names, name)
		}
	}
	m.sm.mu.RUnlock()

	// Persist which sessions were live so we can restore them on wake.
	m.sm.mu.Lock()
	m.sm.preSleepSessions = names
	m.sm.mu.Unlock()

	m.sm.StopAll()
}

// onWake is called by the platform-specific watcher when the system wakes.
func (m *SleepMonitor) onWake() {
	// Brief pause — network interfaces take a moment to come back up.
	time.Sleep(3 * time.Second)

	m.sm.mu.RLock()
	names := make([]string, len(m.sm.preSleepSessions))
	copy(names, m.sm.preSleepSessions)
	m.sm.mu.RUnlock()

	for _, name := range names {
		if err := m.sm.Start(name); err != nil {
			// Non-fatal: session will show as disconnected in the UI.
			_ = err
		}
	}

	m.sm.mu.Lock()
	m.sm.preSleepSessions = nil
	m.sm.mu.Unlock()
}

// startPollingFallback is used when the native OS watcher can't register.
// It detects wakes by watching the system clock for jumps > 10s.
func (m *SleepMonitor) startPollingFallback() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		last := time.Now()
		for {
			select {
			case <-m.stop:
				return
			case now := <-ticker.C:
				if now.Sub(last) > 30*time.Second {
					// Clock jumped — system was asleep.
					m.onWake()
				}
				last = now
			}
		}
	}()
}
