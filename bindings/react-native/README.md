# WaEngine React Native Module

React Native native module for the WaEngine WhatsApp SDK. Supports multiple concurrent WhatsApp sessions with automatic event polling.

## Features

- **Multi-Session Support**: Up to 5 concurrent WhatsApp accounts
- **Automatic Event Polling**: Events are polled from Go SDK and emitted to JS
- **Lifecycle Aware**: Polling pauses when app goes background
- **Thread-Safe**: All operations run off the JS thread
- **Type-Safe**: Full TypeScript definitions included

## Installation

### 1. Build the Go AAR

First, build the WaEngine AAR for Android:

```bash
cd /path/to/wa-engine/bindings/android
./build-aar.sh
```

### 2. Add AAR to Android Project

Copy the generated `waengine.aar` to your React Native project:

```bash
cp build/waengine.aar your-rn-app/android/app/libs/
```

### 3. Update build.gradle

In `android/app/build.gradle`:

```gradle
android {
    // ...
}

dependencies {
    implementation fileTree(dir: 'libs', include: ['*.aar'])

    // Required for lifecycle observer
    implementation "androidx.lifecycle:lifecycle-process:2.6.2"

    // ... other dependencies
}
```

### 4. Copy Native Module Files

Copy the Kotlin files to your project:

```bash
mkdir -p your-rn-app/android/app/src/main/java/com/waengine/
cp android/WaEngineModule.kt your-rn-app/android/app/src/main/java/com/waengine/
cp android/WaEnginePackage.kt your-rn-app/android/app/src/main/java/com/waengine/
```

### 5. Register the Package

In `MainApplication.kt`:

```kotlin
import com.waengine.WaEnginePackage

class MainApplication : Application(), ReactApplication {
    override val reactNativeHost: ReactNativeHost = object : DefaultReactNativeHost(this) {
        override fun getPackages(): List<ReactPackage> =
            PackageList(this).packages.apply {
                add(WaEnginePackage())  // Add this line
            }
        // ...
    }
}
```

Or in `MainApplication.java`:

```java
import com.waengine.WaEnginePackage;

@Override
protected List<ReactPackage> getPackages() {
    List<ReactPackage> packages = new PackageList(this).getPackages();
    packages.add(new WaEnginePackage());  // Add this line
    return packages;
}
```

### 6. Copy TypeScript Module

```bash
cp src/WaEngine.ts your-rn-app/src/native/WaEngine.ts
```

## Usage

### Basic Setup

```typescript
import WaEngine, {
  WaEngineEvent,
  MessageReceivedData,
} from './native/WaEngine';
import { useEffect, useState } from 'react';
import RNFS from 'react-native-fs';

function App() {
  const [isReady, setIsReady] = useState(false);
  const [qrCode, setQrCode] = useState<string | null>(null);

  useEffect(() => {
    // Initialize on app start
    const init = async () => {
      const dataDir = `${RNFS.DocumentDirectoryPath}/waengine`;
      await WaEngine.init(dataDir);
      setIsReady(true);
    };
    init();

    // Cleanup on unmount
    return () => {
      WaEngine.stopAll();
    };
  }, []);

  useEffect(() => {
    if (!isReady) return;

    // Listen for events from all sessions
    const subscription = WaEngine.addListener((event: WaEngineEvent) => {
      console.log(`[${event.sessionName}] ${event.type}:`, event.data);

      switch (event.type) {
        case 'qr.updated':
          if (event.sessionName === 'work') {
            setQrCode((event.data as any).code);
          }
          break;

        case 'pairing.success':
          console.log('Paired!', event.data);
          setQrCode(null);
          break;

        case 'message.received':
          const msg = event.data as MessageReceivedData;
          console.log(`New message from ${msg.sender_name}: ${msg.text}`);
          break;
      }
    });

    return () => subscription.remove();
  }, [isReady]);

  // ... rest of component
}
```

### Multi-Session Example

```typescript
import WaEngine from './native/WaEngine';

// Setup multiple accounts
async function setupAccounts() {
  await WaEngine.init(dataDir);

  // Check which accounts are already paired
  const sessions = await WaEngine.listSessions();
  console.log('Existing sessions:', sessions);

  // Start "work" account
  if (await WaEngine.isPaired('work')) {
    await WaEngine.startSession('work');
    console.log('Work account reconnected');
  } else {
    await WaEngine.startPairing('work');
    console.log('Scan QR for work account');
  }

  // Start "personal" account
  if (await WaEngine.isPaired('personal')) {
    await WaEngine.startSession('personal');
  } else {
    await WaEngine.startPairing('personal');
  }
}

// Send from specific account
async function sendFromWork(phone: string, message: string) {
  const jid = WaEngine.formatPhoneJID(phone);
  const result = await WaEngine.sendMessage(jid, 'work', message);
  console.log('Message sent, ID:', result.messageId);
}

// Listen to specific session only
const workSubscription = WaEngine.addSessionListener('work', (event) => {
  // Only receives events from 'work' session
  console.log('Work event:', event.type);
});
```

### Sending Media

```typescript
// Send image from URL
await WaEngine.sendImageWithCaption(
  jid,
  'work',
  'https://example.com/image.jpg',
  'Check this out!'
);

// Send image from base64
await WaEngine.sendImageWithCaption(
  jid,
  'work',
  'data:image/jpeg;base64,/9j/4AAQ...',
  'Here is the photo'
);
```

### Session Management

```typescript
// Get all session info
const info = await WaEngine.getAllSessionsInfo();
console.log(`${info.count}/${info.max_sessions} sessions active`);

info.sessions.forEach((session) => {
  console.log(`${session.name}: connected=${session.is_connected}`);
});

// Stop specific session
await WaEngine.stopSession('work');

// Stop all sessions (call before app exit)
await WaEngine.stopAll();
```

## Event Types

| Event               | Description           | Data Fields             |
| ------------------- | --------------------- | ----------------------- |
| `qr.updated`        | New QR code available | `code: string`          |
| `qr.expired`        | QR code expired       | -                       |
| `pairing.success`   | Successfully paired   | `jid: string`           |
| `pairing.failed`    | Pairing failed        | `reason: string`        |
| `connection.open`   | Connected to WhatsApp | -                       |
| `connection.closed` | Disconnected          | `reason?: string`       |
| `message.received`  | New message           | See MessageReceivedData |
| `message.sent`      | Message sent          | `id: string`            |
| `message.failed`    | Send failed           | `to, error, code`       |
| `logged_out`        | Session logged out    | -                       |
| `error`             | Error occurred        | `code, message`         |

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     React Native (JS)                        │
│                                                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐          │
│  │  Session A  │  │  Session B  │  │  Session C  │   UI     │
│  │   Events    │  │   Events    │  │   Events    │          │
│  └──────▲──────┘  └──────▲──────┘  └──────▲──────┘          │
│         │                │                │                  │
│         └────────────────┼────────────────┘                  │
│                          │                                   │
│              NativeEventEmitter                              │
│              "wa-engine-event"                               │
└─────────────────────────────┬───────────────────────────────┘
                              │
┌─────────────────────────────▼───────────────────────────────┐
│                  WaEngineModule (Kotlin)                     │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │           ScheduledExecutorService                    │   │
│  │                                                       │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐   │   │
│  │  │ Poll Task A │  │ Poll Task B │  │ Poll Task C │   │   │
│  │  │  (500ms)    │  │  (500ms)    │  │  (500ms)    │   │   │
│  │  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘   │   │
│  └─────────│────────────────│────────────────│──────────┘   │
│            │                │                │               │
│            ▼                ▼                ▼               │
│  ┌─────────────────────────────────────────────────────┐    │
│  │              Go SDK (waengine.aar)                   │    │
│  │                                                      │    │
│  │  ┌─────────┐   ┌─────────┐   ┌─────────┐            │    │
│  │  │Engine A │   │Engine B │   │Engine C │            │    │
│  │  │ SQLite  │   │ SQLite  │   │ SQLite  │            │    │
│  │  │  Queue  │   │  Queue  │   │  Queue  │            │    │
│  │  └─────────┘   └─────────┘   └─────────┘            │    │
│  └─────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

## Lifecycle Notes

### Polling Behavior

- **Start**: Polling begins automatically after `startSession()` or `startPairing()`
- **Connection-Aware**: Polling only runs when session is actually connected (saves battery)
- **Background**: Polling pauses when app goes to background (battery saving)
- **Foreground**: Polling resumes when app returns to foreground
- **Stop**: Polling stops immediately on `stopSession()` or `stopAll()`

### Connection-Aware Polling

The polling loop checks `isConnectedSession()` before each poll. This means:

- No wasted CPU/battery when session is disconnecting
- No useless poll calls during network outages
- Polling automatically resumes when connection is restored

You can monitor polling behavior using the debug hook:

```typescript
import { useWaEngineDebug } from './hooks';

function DebugPanel({ sessionName }: { sessionName: string }) {
  const { stats, isPolling, isAppForeground } = useWaEngineDebug(
    sessionName,
    1000
  );

  return (
    <View>
      <Text>Queue size: {stats?.queueSize ?? 0}</Text>
      <Text>Is polling: {isPolling ? 'Yes' : 'No'}</Text>
      <Text>App foreground: {isAppForeground ? 'Yes' : 'No'}</Text>
      <Text>Total polled: {stats?.totalPolled ?? 0}</Text>
      <Text>Dropped events: {stats?.dropped ?? 0}</Text>
    </View>
  );
}
```

Or via the API directly:

```typescript
const stats = await WaEngine.getEventQueueStats('work');
console.log('Queue stats:', stats);
// {
//   queue: { queueSize: 0, totalEnqueued: 42, totalPolled: 42, dropped: 0 },
//   isPolling: true,
//   isAppForeground: true,
//   activePollingTasks: 2
// }
```

### Why Polling Instead of Callbacks?

The Go SDK uses gomobile, which has limited callback support. Polling from Kotlin to Go is:

- Fully compatible with gomobile bind
- Predictable timing (500ms intervals)
- Easy to pause/resume for lifecycle
- No complex callback registration across language boundaries

### Thread Safety

- All Go SDK calls run on a `ScheduledExecutorService` (3 threads)
- JS thread is never blocked
- `ConcurrentHashMap` manages session → task mapping
- `AtomicBoolean` tracks foreground state

## Troubleshooting

### Module not found

```
Error: WaEngineModule not found
```

- Ensure `WaEnginePackage` is added to `MainApplication`
- Rebuild the app: `npx react-native run-android`

### AAR not found

```
Error: Failed to resolve: waengine
```

- Check `waengine.aar` is in `android/app/libs/`
- Ensure `fileTree` is in build.gradle dependencies

### Events not received

- Check `addListener` is called after `init()`
- Verify session was started with `startSession()` or `startPairing()`
- Check logs: `adb logcat | grep WaEngineModule`

## License

See main wa-engine LICENSE file.
