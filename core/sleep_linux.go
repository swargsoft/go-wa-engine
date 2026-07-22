// Linux sleep/wake watcher.
// Polls /sys/power/wakeup which the kernel increments on every system wake.
// A change in the counter means the system just woke from sleep.
//
// This approach works on all Linux kernels without any extra dependencies.
// If you want D-Bus/logind support, add it here behind a build tag.

//go:build linux

package core

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const wakeupCountPath = "/sys/power/wakeup_count"

func readWakeupCount() (uint64, bool) {
	data, err := os.ReadFile(wakeupCountPath)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Start begins watching for sleep/wake events on Linux.
func (m *SleepMonitor) Start() {
	last, ok := readWakeupCount()
	if !ok {
		// /sys/power/wakeup_count not available — nothing to do.
		<-m.stop
		return
	}

	// We can't get a "will sleep" signal without D-Bus, so we approximate:
	// stop sessions when we detect a wake (the previous sleep already happened).
	// For a cleaner stop-before-sleep, a systemd sleep hook is the right tool —
	// the install-service command wires that up automatically.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			current, ok := readWakeupCount()
			if !ok {
				continue
			}
			if current != last {
				last = current
				// System just woke — sessions were already stopped by the
				// systemd sleep hook; just reconnect them.
				m.onWake()
			}
		}
	}
}
