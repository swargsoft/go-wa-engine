// Windows sleep/wake watcher.
// Uses PowerRegisterSuspendResumeNotification (PowrProf.dll) via syscall — no CGO needed.
// This is the callback-based API that works from a plain console/service process
// with no window handle. (RegisterSuspendResumeNotification, in user32.dll, is a
// different API that requires an HWND or service control handle and delivers
// WM_POWERBROADCAST messages through a message loop — not usable here.)

//go:build windows

package core

import (
	"syscall"
	"unsafe"
)

var (
	modPowrProf                      = syscall.NewLazyDLL("PowrProf.dll")
	procPowerRegisterSuspendResume   = modPowrProf.NewProc("PowerRegisterSuspendResumeNotification")
	procPowerUnregisterSuspendResume = modPowrProf.NewProc("PowerUnregisterSuspendResumeNotification")
)

// DEVICE_NOTIFY_CALLBACK = 2
const deviceNotifyCallback = 2

// POWERBROADCAST_SETTING passed to the callback for PBT_POWERSETTINGCHANGE.
// Unused for suspend/resume notifications but kept for reference/future use.
type powerBroadcastSetting struct {
	PowerSetting syscall.GUID
	DataLength   uint32
	Data         [1]byte
}

// suspendResumeChan bridges the syscall callback to Go.
var suspendResumeChan = make(chan bool, 4) // true=sleep, false=wake

// deviceNotifyCallbackProc is the callback registered with Windows.
// Signature must match: ULONG CALLBACK(PVOID Context, ULONG Type, PVOID Setting)
var deviceNotifyCallbackProc = syscall.NewCallback(func(context uintptr, changeType uint32, setting uintptr) uintptr {
	const (
		PBT_APMSUSPEND         = 4
		PBT_APMRESUMESUSPEND   = 7
		PBT_APMRESUMEAUTOMATIC = 18
	)
	switch changeType {
	case PBT_APMSUSPEND:
		suspendResumeChan <- true
	case PBT_APMRESUMEAUTOMATIC, PBT_APMRESUMESUSPEND:
		suspendResumeChan <- false
	}
	return 0
})

// DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS
type deviceNotifySubscribeParameters struct {
	Callback uintptr
	Context  uintptr
}

// Start begins watching for sleep/wake events on Windows.
func (m *SleepMonitor) Start() {
	params := deviceNotifySubscribeParameters{
		Callback: deviceNotifyCallbackProc,
		Context:  0,
	}

	// PowerRegisterSuspendResumeNotification(Flags, Recipient, *RegistrationHandle) DWORD
	// Returns ERROR_SUCCESS (0) on success; the registration handle is written
	// through the third (output) parameter, NOT returned directly.
	var handle uintptr
	ret, _, _ := procPowerRegisterSuspendResume.Call(
		uintptr(deviceNotifyCallback),
		uintptr(unsafe.Pointer(&params)),
		uintptr(unsafe.Pointer(&handle)),
	)

	if ret != 0 {
		// Registration failed — fall back to the clock-jump polling detector
		// rather than silently doing nothing.
		m.startPollingFallback()
		<-m.stop
		return
	}

	go func() {
		for {
			select {
			case <-m.stop:
				if handle != 0 {
					procPowerUnregisterSuspendResume.Call(handle)
				}
				return
			case sleeping := <-suspendResumeChan:
				if sleeping {
					m.onSleep()
				} else {
					m.onWake()
				}
			}
		}
	}()

	<-m.stop
}
