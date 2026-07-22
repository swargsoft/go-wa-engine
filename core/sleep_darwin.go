// macOS IOKit sleep/wake watcher.
// Uses CGO to register with the IOKit power notification port.
// Runs a CFRunLoop on a dedicated OS thread — required by IOKit.

//go:build darwin

package core

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation

#include <IOKit/pwr_mgt/IOPMLib.h>
#include <IOKit/IOMessage.h>
#include <CoreFoundation/CoreFoundation.h>

extern void sleepWakeCallback(void *refcon, io_service_t service, natural_t messageType, void *messageArgument);

static io_connect_t registerSleepWake(void *refcon) {
    IONotificationPortRef notifyPort;
    io_object_t           notifier;
    io_connect_t          root;

    root = IORegisterForSystemPower(refcon, &notifyPort, sleepWakeCallback, &notifier);
    if (root == MACH_PORT_NULL) return MACH_PORT_NULL;

    CFRunLoopAddSource(
        CFRunLoopGetCurrent(),
        IONotificationPortGetRunLoopSource(notifyPort),
        kCFRunLoopDefaultMode
    );
    return root;
}
*/
import "C"
import (
	"runtime"
	"unsafe"
)

var sleepWakeChan = make(chan bool, 4) // true = sleep, false = wake

// rootPort is set once in Start() and read by the exported callback.
var rootPort C.io_connect_t

//export sleepWakeCallback
func sleepWakeCallback(refcon unsafe.Pointer, service C.io_service_t, messageType C.natural_t, messageArgument unsafe.Pointer) {
	switch messageType {
	case C.kIOMessageSystemWillSleep:
		sleepWakeChan <- true
		C.IOAllowPowerChange(rootPort, C.long(uintptr(messageArgument)))
	case C.kIOMessageSystemHasPoweredOn:
		sleepWakeChan <- false
	}
}

// Start begins watching for sleep/wake events on macOS.
func (m *SleepMonitor) Start() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	rootPort = C.registerSleepWake(nil)
	if rootPort == C.MACH_PORT_NULL {
		m.startPollingFallback()
		<-m.stop
		return
	}

	go func() {
		for {
			select {
			case <-m.stop:
				return
			case sleeping := <-sleepWakeChan:
				if sleeping {
					m.onSleep()
				} else {
					m.onWake()
				}
			}
		}
	}()

	go func() {
		<-m.stop
		C.CFRunLoopStop(C.CFRunLoopGetCurrent())
	}()

	C.CFRunLoopRun()
}
