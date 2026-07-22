// Windows sleep/wake watcher.
// Uses RegisterSuspendResumeNotification (Win8+) via syscall — no CGO needed.
// Falls back to a hidden message-only window on older Windows.

//go:build windows

package core

import (
	"syscall"
	"unsafe"
)

var (
	modPowrProf                        = syscall.NewLazyDLL("PowrProf.dll")
	procRegisterSuspendResumeNotif     = modPowrProf.NewProc("RegisterSuspendResumeNotification")
	procUnregisterSuspendResumeNotif   = modPowrProf.NewProc("UnregisterSuspendResumeNotification")
)

// DEVICE_NOTIFY_CALLBACK = 2
const deviceNotifyCallback = 2

// POWERBROADCAST_SETTING passed to the callback.
type powerBroadcastSetting struct {
	PowerSetting syscall.GUID
	DataLength   uint32
	Data         [1]byte
}

// suspendResumeChan bridges the syscall callback to Go.
var suspendResumeChan = make(chan bool, 4) // true=sleep, false=wake

// deviceNotifyCallbackProc is the callback registered with Windows.
var deviceNotifyCallbackProc = syscall.NewCallback(func(context uintptr, changeType uint32, setting uintptr) uintptr {
	const (
		PBT_APMSUSPEND   = 4
		PBT_APMRESUMEAUTOMATIC = 18
		PBT_APMRESUMESUSPEND   = 7
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
	handle, _, _ := procRegisterSuspendResumeNotif.Call(
		uintptr(unsafe.Pointer(&params)),
		deviceNotifyCallback,
	)

	go func() {
		for {
			select {
			case <-m.stop:
				if handle != 0 {
					procUnregisterSuspendResumeNotif.Call(handle)
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
