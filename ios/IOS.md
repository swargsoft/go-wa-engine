# iOS Integration Plan: wa-engine in Capacitor IPA

## Overview

Bundle `wa-engine` as a static library (`.a`) inside a Capacitor-built IPA. A Swift Capacitor plugin calls the library via C bridge functions. Unlike Android, iOS **does not allow long-running background processes** — the engine only runs while the app is in the foreground and reconnects on resume.

## Architecture

```
┌──────────────────────────────────────────────┐
│  React Web App (Capacitor WKWebView)         │
│  ┌──────────────────────────────────────────┐│
│  │  @msgly/engine-plugin (TS)               ││
│  └──────┬───────────────────────────────────┘│
└─────────┼────────────────────────────────────┘
          │ Capacitor Bridge
          ▼
┌──────────────────────────────────────────────┐
│  Capacitor Plugin (Swift)                    │
│  ┌──────────────────────────────────────────┐│
│  │  WaEnginePlugin.swift                    ││
│  │  ┌──────────────┐  ┌──────────────────┐  ││
│  │  │ @PluginMethod│  │  EventEmitter    │  ││
│  │  └──────┬───────┘  └────────┬─────────┘  ││
│  └─────────┼───────────────────┼────────────┘│
└────────────┼───────────────────┼─────────────┘
             │ C function calls  │ Event Queue
             ▼                   ▼
┌──────────────────────────────────────────────┐
│  C Bridge (waengine.h + libwaengine.a)       │
│  ┌──────────────────────────────────────────┐│
│  │  wa_engine_init()                        ││
│  │  wa_engine_start_session()               ││
│  │  wa_engine_poll_event()                  ││
│  │  wa_engine_send_text()                   ││
│  └──────────────┬───────────────────────────┘│
└─────────────────┼────────────────────────────┘
┌─────────────────┼────────────────────────────┐
│  SessionManager │◄── calls ───────┘          │
│  └── Engine instances + Event Queues         │
│  └── SQLite Storage (wa.db per session)      │
│  └── whatsmeow client ←→ WhatsApp servers   │
└──────────────────────────────────────────────┘
```

## Key Limitation: iOS Background Execution

| Feature | Android | iOS |
|---------|---------|-----|
| Background runtime | Foreground Service → unlimited | ~30 seconds then suspended |
| TCP connection | Stays alive | Killed on suspend |
| On resume | Already connected | Auto-reconnect (already built in) |
| "Always online" | Possible | **Not possible** — Apple restriction |

The engine's auto-reconnect + backoff logic already handles this: on `AppDelegate.applicationDidBecomeActive`, call `wa_engine_resume()` which reconnects all sessions. On `applicationDidEnterBackground`, call `wa_engine_suspend()`.

## Step-by-Step Implementation

### Step 1: Go C Bridge (`wa-engine/mobile/`)

Same Go bridge package as Android, but exported as plain C functions (no JNI naming):

**File: `wa-engine/mobile/bridge_ios.go`**

```go
package main

/*
#include <stdint.h>
*/
import "C"
import (
    "unsafe"
    mobilecore "github.com/mml/wa-engine/mobile/core"
)

var manager *mobilecore.Manager

//export wa_engine_init
func wa_engine_init(dataDir *C.char) C.int {
    dir := C.GoString(dataDir)
    var err error
    manager, err = mobilecore.NewManager(dir)
    if err != nil {
        return 0
    }
    return 1
}

//export wa_engine_start_session
func wa_engine_start_session(name *C.char) C.int {
    if manager == nil { return 0 }
    if err := manager.Start(C.GoString(name)); err != nil {
        return 0
    }
    return 1
}

//export wa_engine_send_text
func wa_engine_send_text(session, to, text *C.char) *C.char {
    if manager == nil { return nil }
    id, err := manager.SendText(C.GoString(to), C.GoString(session), C.GoString(text))
    if err != nil {
        return C.CString(`{"error":"` + err.Error() + `"}`)
    }
    return C.CString(`{"id":"` + id + `"}`)
}

//export wa_engine_poll_event
func wa_engine_poll_event(session *C.char) *C.char {
    if manager == nil { return nil }
    event := manager.PollEvent(C.GoString(session))
    return C.CString(event)
}

//export wa_engine_free_string
func wa_engine_free_string(s *C.char) {
    C.free(unsafe.Pointer(s))
}
```

### Step 2: Build the Static Library

**`wa-engine/Makefile` additions:**

```makefile
# iOS cross-compilation
IOS_SDK       ?= $(shell xcrun --sdk iphoneos --show-sdk-path)
IOS_CC        ?= $(shell xcrun --sdk iphoneos -f clang)

ios-lib:
	@mkdir -p build/ios
	CGO_ENABLED=1 \
	GOOS=ios GOARCH=arm64 \
	CC=$(IOS_CC) \
	CGO_CFLAGS="-isysroot $(IOS_SDK) -miphoneos-version-min=15.0 -fembed-bitcode" \
	CGO_LDFLAGS="-isysroot $(IOS_SDK) -miphoneos-version-min=15.0" \
	go build -buildmode=c-archive \
	  -ldflags="-s -w" \
	  -o build/ios/libwaengine.a ./mobile
	@echo "✓ Built build/ios/libwaengine.a"
	@echo "  Header: build/ios/libwaengine.h"

ios-sim-lib:
	@mkdir -p build/ios-sim
	CGO_ENABLED=1 \
	GOOS=ios GOARCH=arm64 \
	CC=$(shell xcrun --sdk iphonesimulator -f clang) \
	CGO_CFLAGS="-isysroot $(shell xcrun --sdk iphonesimulator --show-sdk-path) -miphoneos-version-min=15.0" \
	CGO_LDFLAGS="-isysroot $(shell xcrun --sdk iphonesimulator --show-sdk-path) -miphoneos-version-min=15.0" \
	go build -buildmode=c-archive \
	  -ldflags="-s -w" \
	  -o build/ios-sim/libwaengine.a ./mobile
	@echo "✓ Built build/ios-sim/libwaengine.a"

ios-xcframework: ios-lib ios-sim-lib
	xcodebuild -create-xcframework \
	  -library build/ios/libwaengine.a -headers build/ios \
	  -library build/ios-sim/libwaengine.a -headers build/ios-sim \
	  -output build/ios/WaEngine.xcframework
	@echo "✓ Built build/ios/WaEngine.xcframework"
```

**Requirements:**
- macOS with Xcode 15+
- Go 1.25+ with CGO
- `xcrun` toolchain
- Build simulator + device slices, combine into `.xcframework`

### Step 3: Capacitor iOS Setup

```bash
cd msgly
npx cap add ios
```

### Step 4: Create the Capacitor Plugin (Swift)

**Directory structure:**
```
msgly/
├── ios/
│   └── App/
│       ├── Plugins/
│       │   └── WaEnginePlugin/
│       │       ├── WaEnginePlugin.swift
│       │       └── WaEnginePlugin.m
│       └── Frameworks/
│           └── WaEngine.xcframework   # from build step
├── src/
│   └── engine-plugin.ts              # Same TS API as Android
```

**`WaEnginePlugin.swift`:**

```swift
import Capacitor
import WaEngine  // from the xcframework

@objc(WaEnginePlugin)
public class WaEnginePlugin: CAPPlugin {
    
    override public func load() {
        let dataDir = FileManager.default.urls(
            for: .documentDirectory, in: .userDomainMask
        ).first!.appendingPathComponent("wa-engine").path
        
        try? FileManager.default.createDirectory(
            atPath: dataDir, withIntermediateDirectories: true
        )
        
        wa_engine_init((dataDir as NSString).utf8String)
        
        // Observe app lifecycle for reconnect
        NotificationCenter.default.addObserver(
            self, selector: #selector(appDidBecomeActive),
            name: UIApplication.didBecomeActiveNotification, object: nil
        )
    }
    
    @objc func appDidBecomeActive() {
        // Reconnect all sessions after background suspension
        for session in WaEngineBridge.listSessions() {
            wa_engine_start_session((session as NSString).utf8String)
        }
    }
    
    @objc func startSession(_ call: CAPPluginCall) {
        guard let name = call.getString("name") else {
            call.reject("Missing session name"); return
        }
        let result = wa_engine_start_session((name as NSString).utf8String)
        call.resolve(["status": result == 1 ? "ok" : "error"])
    }
    
    @objc func startPairing(_ call: CAPPluginCall) {
        guard let name = call.getString("name") else {
            call.reject("Missing session name"); return
        }
        let result = wa_engine_start_pairing((name as NSString).utf8String)
        call.resolve(["status": result == 1 ? "pairing" : "error"])
    }
    
    @objc func pollEvent(_ call: CAPPluginCall) {
        guard let session = call.getString("session") else {
            call.reject("Missing session"); return
        }
        guard let eventPtr = wa_engine_poll_event((session as NSString).utf8String) else {
            call.resolve(["event": ""]); return
        }
        let event = String(cString: eventPtr)
        wa_engine_free_string(eventPtr)
        call.resolve(["event": event])
    }
    
    @objc func sendText(_ call: CAPPluginCall) {
        guard let session = call.getString("session"),
              let to = call.getString("to"),
              let text = call.getString("text") else {
            call.reject("Missing parameters"); return
        }
        guard let resultPtr = wa_engine_send_text(
            (session as NSString).utf8String,
            (to as NSString).utf8String,
            (text as NSString).utf8String
        ) else { call.reject("Send failed"); return }
        
        let result = String(cString: resultPtr)
        wa_engine_free_string(resultPtr)
        
        if let data = result.data(using: .utf8),
           let json = try? JSONSerialization.jsonObject(with: data) as? [String: String] {
            call.resolve(json)
        } else {
            call.reject("Send failed")
        }
    }
}
```

**`WaEnginePlugin.m` (Obj-C bridge for Capacitor):**

```objc
#import <Capacitor/Capacitor.h>

CAP_PLUGIN(WaEnginePlugin, "WaEngine",
    CAP_PLUGIN_METHOD(init, CAPPluginReturnPromise);
    CAP_PLUGIN_METHOD(startSession, CAPPluginReturnPromise);
    CAP_PLUGIN_METHOD(startPairing, CAPPluginReturnPromise);
    CAP_PLUGIN_METHOD(startPhonePairing, CAPPluginReturnPromise);
    CAP_PLUGIN_METHOD(sendText, CAPPluginReturnPromise);
    CAP_PLUGIN_METHOD(pollEvent, CAPPluginReturnPromise);
    CAP_PLUGIN_METHOD(stopSession, CAPPluginReturnPromise);
    CAP_PLUGIN_METHOD(getQR, CAPPluginReturnPromise);
    CAP_PLUGIN_METHOD(getStatus, CAPPluginReturnPromise);
)
```

### Step 5: Add Framework to Xcode Project

After `npx cap sync ios`:

1. Open `msgly/ios/App.xcworkspace` in Xcode
2. Drag `WaEngine.xcframework` into the project under `App/Frameworks`
3. In target settings → General → Frameworks, Libraries & Embedded Content:
   - Add `WaEngine.xcframework` → "Embed & Sign"
4. Build settings:
   - Set `C++ Standard Library` to `libc++`
   - Ensure `Enable Bitcode` matches your build

### Step 6: TypeScript Plugin API

Identical to Android. Use the same `engine-plugin.ts`:

```typescript
import { registerPlugin } from '@capacitor/core';

export interface WaEnginePlugin {
  init(): Promise<void>;
  startSession(options: { name: string }): Promise<{ status: string }>;
  startPairing(options: { name: string }): Promise<{ status: string }>;
  startPhonePairing(options: { name: string; phone: string }): Promise<{ code: string }>;
  sendText(options: { session: string; to: string; text: string }): Promise<{ messageId: string }>;
  pollEvent(options: { session: string }): Promise<{ event: string }>;
  stopSession(options: { name: string }): Promise<void>;
  getQR(options: { name: string }): Promise<{ qr: string }>;
  getStatus(options: { name: string }): Promise<{ status: string }>;
}

const WaEngine = registerPlugin<WaEnginePlugin>('WaEngine');
export default WaEngine;
```

### Step 7: App Lifecycle Handling

**`msgly/ios/App/AppDelegate.swift`:**

```swift
import UIKit

@UIApplicationMain
class AppDelegate: UIResponder, UIApplicationDelegate {
    func applicationDidEnterBackground(_ application: UIApplication) {
        // iOS will suspend the app shortly after
        // wa_engine_stop_all() is called automatically when process suspends
        // TCP sockets are closed by the OS
    }
    
    func applicationDidBecomeActive(_ application: UIApplication) {
        // OS has restored TCP connectivity
        // WaEnginePlugin's observer handles reconnection
    }
    
    func applicationWillTerminate(_ application: UIApplication) {
        wa_engine_stop_all()
    }
}
```

### Step 8: Background Modes (Limited)

For brief background execution (sending a message when app is backgrounded):

```xml
<!-- Info.plist -->
<key>UIBackgroundModes</key>
<array>
    <string>processing</string>
</array>
```

Use `BGProcessingTask` for short-lived tasks:

```swift
// Register in AppDelegate
BGTaskScheduler.shared.register(forTaskWithIdentifier: "com.msgly.engine", using: nil) { task in
    self.handleBackgroundTask(task as! BGProcessingTask)
}

func handleBackgroundTask(_ task: BGProcessingTask) {
    // Brief window (~30 seconds) to process events
    // Engine must already be initialized
    task.expirationHandler = {
        wa_engine_stop_all()
    }
}
```

This allows brief message processing but **cannot keep the engine running persistently**.

## Build & Run

```bash
# 1. Build Go static library
cd wa-engine && make ios-xcframework

# 2. Copy xcframework into Capacitor project
cp -r build/ios/WaEngine.xcframework ../msgly/ios/App/Frameworks/

# 3. Build web app
cd ../msgly && npm run build

# 4. Sync Capacitor
npx cap sync ios

# 5. Open in Xcode
npx cap open ios

# 6. Build & run from Xcode (select your team for signing)
```

## Key Differences from Android

| Aspect | Android | iOS |
|--------|---------|-----|
| Library format | `.so` (shared) | `.a` (static) or `.xcframework` |
| Bridge mechanism | JNI (Java ↔ C) | Direct C function calls (Swift ↔ C) |
| Background execution | Foreground Service → unlimited | ~30 seconds → suspended |
| Connection persistence | Stays alive | Drops on suspend |
| Reconnection | Optional | Required on every foreground |
| Data directory | `getFilesDir()` | `Documents/` |
| Build host | Any OS | macOS only (Xcode required) |

## WhatsApp iOS Behavior Notes

- WhatsApp Web sessions persist server-side for ~14 days even without connection
- On reconnection, the engine's `AutoReconnectErrors = 10` and backoff handle reconnection gracefully
- `KeepAliveTimeout` events → `KeepAliveRestored` events work as expected
- The anti-ban presence manager re-sends online status on reconnection
- From WhatsApp's perspective, frequent disconnect/reconnect looks like a mobile client switching networks (normal behavior)

## Testing

```bash
# Unit tests on Go bridge
cd wa-engine && go test ./mobile/...

# Build for iOS simulator
make ios-sim-lib

# Run on simulator
cd msgly && npx cap run ios

# Check logs (from Xcode console or)
xcrun simctl spawn booted log stream --predicate 'subsystem == "com.msgly.engine"'
```