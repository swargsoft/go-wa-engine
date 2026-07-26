# Android Integration Plan: wa-engine in Capacitor APK

## Overview

Bundle the Go-based `wa-engine` as a shared library (`.so`) inside a Capacitor-built APK. A Java Capacitor plugin loads the library via JNI and runs the engine in a persistent Android Foreground Service.

## Architecture

```
┌──────────────────────────────────────────────┐
│  React Web App (Capacitor WebView)           │
│  ┌──────────────────────────────────────────┐│
│  │  @msgly/engine-plugin (TS)               ││
│  │  ┌──────────┐  ┌──────────────────────┐  ││
│  │  │ start()  │  │  onEvent(callback)   │  ││
│  │  │ send()   │  │  getQR()             │  ││
│  │  └────┬─────┘  └─────────┬────────────┘  ││
│  └───────┼──────────────────┼───────────────┘│
└──────────┼──────────────────┼────────────────┘
           │ Capacitor Bridge │
           ▼                  ▼
┌──────────────────────────────────────────────┐
│  Capacitor Plugin (Java)                     │
│  ┌──────────────────────────────────────────┐│
│  │  WaEnginePlugin.java                     ││
│  │  ┌──────────────┐  ┌──────────────────┐  ││
│  │  │ @PluginMethod│  │  EventEmitter    │  ││
│  │  └──────┬───────┘  └────────┬─────────┘  ││
│  └─────────┼───────────────────┼────────────┘│
└────────────┼───────────────────┼─────────────┘
             │ JNI (cgo)         │ Event Queue
             ▼                   ▼
┌──────────────────────────────────────────────┐
│  Go Shared Library (libwaengine.so)          │
│  ┌──────────────────────────────────────────┐│
│  │  JNI Bridge (mobile/bridge.go)           ││
│  │  ┌──────────────────────────────────────┐││
│  │  │  Exported JNI functions:            │││
│  │  │  - Java_com_example_WaEngine_*      │││
│  │  └──────────────┬───────────────────────┘││
│  └─────────────────┼────────────────────────┘│
│  ┌─────────────────┼────────────────────────┐│
│  │  SessionManager │◄── calls ───────┘      ││
│  │  ├── Engine (session A)                  ││
│  │  ├── Engine (session B)                  ││
│  │  └── Event Queue                         ││
│  └──────────────────────────────────────────┘│
│  ┌──────────────────────────────────────────┐│
│  │  SQLite Storage (wa.db per session)      ││
│  │  whatsmeow client ←→ WhatsApp servers   ││
│  └──────────────────────────────────────────┘│
└──────────────────────────────────────────────┘
┌──────────────────────────────────────────────┐
│  Foreground Service (Android)                │
│  - Keeps process alive                       │
│  - Persistent notification                   │
│  - Restarts engine on app reboot             │
└──────────────────────────────────────────────┘
```

## Step-by-Step Implementation

### Step 1: Go JNI Bridge (`wa-engine/mobile/`)

Create a new Go package that exports C-compatible functions via `cgo`. Each exported function follows the JNI naming convention.

**File: `wa-engine/mobile/bridge.go`**

```go
package mobile

/*
#include <jni.h>
*/
import "C"
import (
    "unsafe"
    mobilecore "github.com/mml/wa-engine/mobile/core"
)

var manager *mobilecore.Manager

//export Java_com_msgly_engine_WaEngine_nativeInit
func Java_com_msgly_engine_WaEngine_nativeInit(env *C.JNIEnv, cls C.jclass, dataDir C.jstring) C.jint {
    dir := C.GoString(dataDir)
    var err error
    manager, err = mobilecore.NewManager(dir)
    if err != nil {
        return 0 // failure
    }
    return 1 // success
}

//export Java_com_msgly_engine_WaEngine_nativeStartSession
func Java_com_msgly_engine_WaEngine_nativeStartSession(env *C.JNIEnv, cls C.jclass, sessionName C.jstring) C.jint {
    name := C.GoString(sessionName)
    if err := manager.Start(name); err != nil {
        return 0
    }
    return 1
}

//export Java_com_msgly_engine_WaEngine_nativeStartPairing
func Java_com_msgly_engine_WaEngine_nativeStartPairing(env *C.JNIEnv, cls C.jclass, sessionName C.jstring) C.jint {
    name := C.GoString(sessionName)
    if err := manager.StartPairing(name); err != nil {
        return 0
    }
    return 1
}

//export Java_com_msgly_engine_WaEngine_nativeSendText
func Java_com_msgly_engine_WaEngine_nativeSendText(env *C.JNIEnv, cls C.jclass, sessionName, to, text C.jstring) C.jstring {
    // ... returns message ID or error JSON
}

//export Java_com_msgly_engine_WaEngine_nativePollEvent
func Java_com_msgly_engine_WaEngine_nativePollEvent(env *C.JNIEnv, cls C.jclass, sessionName C.jstring) C.jstring {
    // ... returns event JSON or empty string
}

//export Java_com_msgly_engine_WaEngine_nativeStopSession
func Java_com_msgly_engine_WaEngine_nativeStopSession(env *C.JNIEnv, cls C.jclass, sessionName C.jstring) {
    // ...
}
```

**File: `wa-engine/mobile/core/manager.go`**

```go
package mobilecore

import (
    "github.com/mml/wa-engine/core"
)

type Manager struct {
    sm *core.SessionManager
}

func NewManager(dataDir string) (*Manager, error) {
    sm, err := core.NewSessionManager(dataDir)
    if err != nil {
        return nil, err
    }
    return &Manager{sm: sm}, nil
}

func (m *Manager) Start(name string) error     { return m.sm.Start(name) }
func (m *Manager) StartPairing(name string) error { return m.sm.StartPairing(name) }
func (m *Manager) StartPhonePairing(name, phone string) (string, error) {
    return m.sm.StartPhonePairing(name, phone)
}
func (m *Manager) SendText(to, session, text string) (string, error) {
    return m.sm.SendText(to, session, text)
}
func (m *Manager) PollEvent(name string) string { return m.sm.PollEvent(name) }
func (m *Manager) Stop(name string) error       { return m.sm.Stop(name) }
func (m *Manager) StopAll()                     { m.sm.StopAll() }
// ... remaining methods mirror session.go
```

### Step 2: Build the Shared Library

**`wa-engine/Makefile` additions:**

```makefile
# Android cross-compilation
ANDROID_NDK ?= $(HOME)/Library/Android/sdk/ndk/27.0.12077973
API_LEVEL   ?= 24

android-lib: android-arm64 android-x86_64

android-arm64:
	@mkdir -p build/android/arm64-v8a
	CGO_ENABLED=1 \
	GOOS=android GOARCH=arm64 \
	CC=$(ANDROID_NDK)/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android$(API_LEVEL)-clang \
	go build -buildmode=c-shared \
	  -ldflags="-s -w" \
	  -o build/android/arm64-v8a/libwaengine.so ./mobile
	@echo "✓ Built build/android/arm64-v8a/libwaengine.so"

android-x86_64:
	@mkdir -p build/android/x86_64
	CGO_ENABLED=1 \
	GOOS=android GOARCH=amd64 \
	CC=$(ANDROID_NDK)/toolchains/llvm/prebuilt/darwin-x86_64/bin/x86_64-linux-android$(API_LEVEL)-clang \
	go build -buildmode=c-shared \
	  -ldflags="-s -w" \
	  -o build/android/x86_64/libwaengine.so ./mobile
	@echo "✓ Built build/android/x86_64/libwaengine.so"
```

**Build requirements:**
- Android NDK r27+ installed
- Go 1.25+ with CGO support
- Build once, copy the `.so` files into the Capacitor Android project

### Step 3: Set Up Capacitor in msgly

```bash
cd msgly
npm install @capacitor/core @capacitor/cli @capacitor/android
npx cap init msgly com.msgly.app
npx cap add android
```

### Step 4: Create the Android Capacitor Plugin

**Directory structure:**
```
msgly/
├── android/
│   └── app/src/main/java/com/msgly/engine/
│       ├── WaEnginePlugin.java          # Capacitor plugin
│       ├── WaEngineService.java         # Foreground service
│       └── WaEngineBridge.java          # JNI wrapper
│   └── app/src/main/jniLibs/
│       ├── arm64-v8a/libwaengine.so
│       └── x86_64/libwaengine.so
├── src/
│   └── engine-plugin.ts                 # TypeScript API
```

**`WaEnginePlugin.java`:**

```java
package com.msgly.engine;

import com.getcapacitor.*;
import org.json.JSONObject;

@CapacitorPlugin(name = "WaEngine")
public class WaEnginePlugin extends Plugin {

    @PluginMethod
    public void init(PluginCall call) {
        String dataDir = getContext().getFilesDir().getAbsolutePath() + "/wa-engine";
        int result = WaEngineBridge.nativeInit(dataDir);
        if (result == 0) {
            call.reject("Failed to initialize engine");
            return;
        }
        // Start foreground service
        Intent intent = new Intent(getContext(), WaEngineService.class);
        getContext().startForegroundService(intent);
        call.resolve();
    }

    @PluginMethod
    public void startSession(PluginCall call) {
        String name = call.getString("name");
        int result = WaEngineBridge.nativeStartSession(name);
        call.resolve(result == 1 ? new JSObject().put("status", "ok") : null);
    }

    @PluginMethod
    public void startPairing(PluginCall call) {
        String name = call.getString("name");
        int result = WaEngineBridge.nativeStartPairing(name);
        call.resolve(new JSObject().put("status", result == 1 ? "pairing" : "error"));
    }

    @PluginMethod
    public void sendText(PluginCall call) {
        String session = call.getString("session");
        String to = call.getString("to");
        String text = call.getString("text");
        String result = WaEngineBridge.nativeSendText(session, to, text);
        call.resolve(new JSObject().put("messageId", result));
    }

    @PluginMethod
    public void pollEvent(PluginCall call) {
        String session = call.getString("session");
        String event = WaEngineBridge.nativePollEvent(session);
        call.resolve(new JSObject().put("event", event));
    }

    // ... remaining methods
}
```

**`WaEngineBridge.java` (JNI wrapper):**

```java
package com.msgly.engine;

public class WaEngineBridge {
    static {
        System.loadLibrary("waengine");
    }

    public static native int nativeInit(String dataDir);
    public static native int nativeStartSession(String sessionName);
    public static native int nativeStartPairing(String sessionName);
    public static native int nativeStartPhonePairing(String sessionName, String phone);
    public static native String nativeSendText(String sessionName, String to, String text);
    public static native String nativePollEvent(String sessionName);
    public static native void nativeStopSession(String sessionName);
    public static native void nativeStopAll();
    // ...
}
```

**`WaEngineService.java` (Foreground Service):**

```java
package com.msgly.engine;

import android.app.*;
import android.content.Intent;
import android.os.IBinder;
import androidx.annotation.Nullable;

public class WaEngineService extends Service {
    private static final int NOTIFICATION_ID = 1001;
    private static final String CHANNEL_ID = "msgly_engine";

    @Override
    public void onCreate() {
        super.onCreate();
        createNotificationChannel();
        Notification notification = new Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("Msgly")
            .setContentText("WhatsApp engine running")
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setOngoing(true)
            .build();
        startForeground(NOTIFICATION_ID, notification);
    }

    private void createNotificationChannel() {
        if (android.os.Build.VERSION.SDK_INT >= 26) {
            NotificationChannel channel = new NotificationChannel(
                CHANNEL_ID, "Msgly Engine", NotificationManager.IMPORTANCE_LOW
            );
            NotificationManager manager = getSystemService(NotificationManager.class);
            manager.createNotificationChannel(channel);
        }
    }

    @Nullable
    @Override
    public IBinder onBind(Intent intent) { return null; }
}
```

### Step 5: TypeScript Plugin API

**`msgly/src/engine-plugin.ts`:**

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
  // ...
}

const WaEngine = registerPlugin<WaEnginePlugin>('WaEngine');
export default WaEngine;
```

### Step 6: Register the Plugin in Capacitor

In `msgly/android/app/src/main/java/com/msgly/app/MainActivity.java`:

```java
package com.msgly.app;

import com.getcapacitor.BridgeActivity;
import com.msgly.engine.WaEnginePlugin;

public class MainActivity extends BridgeActivity {
    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        registerPlugin(WaEnginePlugin.class);
    }
}
```

### Step 7: Android Manifest Changes

**`msgly/android/app/src/main/AndroidManifest.xml` additions:**

```xml
<uses-permission android:name="android.permission.FOREGROUND_SERVICE" />
<uses-permission android:name="android.permission.POST_NOTIFICATIONS" />
<uses-permission android:name="android.permission.INTERNET" />

<application ...>
    <service
        android:name="com.msgly.engine.WaEngineService"
        android:foregroundServiceType="dataSync"
        android:exported="false" />
</application>
```

### Step 8: Build & Run

```bash
# 1. Build Go shared library
cd wa-engine && make android-lib

# 2. Copy .so files into Capacitor project
cp build/android/arm64-v8a/libwaengine.so ../msgly/android/app/src/main/jniLibs/arm64-v8a/
cp build/android/x86_64/libwaengine.so  ../msgly/android/app/src/main/jniLibs/x86_64/

# 3. Build web app
cd ../msgly && npm run build

# 4. Sync Capacitor
npx cap sync android

# 5. Open in Android Studio or build directly
npx cap open android
# or
npx cap run android
```

## Key Considerations

### CGO Dependencies
`wa-engine` uses `go-sqlite3` which requires CGO. Cross-compiling for Android requires the Android NDK. The `whatsmeow` library uses pure Go for networking, so only SQLite needs the C toolchain.

### Memory Management
- The Go runtime manages its own memory. JNI strings passed from Java must be converted properly.
- Use `C.CString()` for Go → JNI and `C.GoString()` for JNI → Go.
- Free C strings with `C.free(unsafe.Pointer(cstr))` after use.

### Event Queue Bridge
Events are polled from JS (the web layer calls `pollEvent()` on a timer or interval). This avoids the complexity of pushing events from native to JS. For real-time needs, implement an SSE-like mechanism using Capacitor's `notifyListeners()`.

### Foreground Service
- Required for Android 8+ (API 26+) to keep the engine alive in background.
- The service shows a persistent notification ("Msgly is running").
- Without it, Android kills the process within minutes of going to background.

### Data Directory
- Use `context.getFilesDir().getAbsolutePath() + "/wa-engine"` as the `dataDir`.
- This is app-private storage — no permissions needed.
- Session data (SQlite DBs) persists across app restarts.

### Session Auto-Discovery
`SessionManager.discoverSessions()` already scans the data directory for existing sessions. On app restart, existing sessions are automatically loaded without re-pairing.

### WorkManager (Optional)
For short-lived background tasks (sending a single message when app is dead), add a `WorkManager` worker that initializes the engine, sends, and shuts down. The Foreground Service handles the running-case.

## Testing

```bash
# Unit tests on Go bridge
cd wa-engine && go test ./mobile/...

# Android emulator test
cd msgly && npx cap run android

# Check logs
adb logcat -s WaEngine:V WaEngineBridge:V
```

## Summary

| Component | Technology | Purpose |
|-----------|-----------|---------|
| wa-engine | Go + CGO | WhatsApp protocol (whatsmeow) |
| JNI Bridge | Go (cgo) | Exposes Go API as C functions |
| .so library | Cross-compiled Go | Bundled in APK jniLibs |
| Capacitor Plugin | Java | Bridges web ↔ native |
| Foreground Service | Java (Android) | Keeps engine alive in background |
| TypeScript API | TS/JS | Web layer interface |