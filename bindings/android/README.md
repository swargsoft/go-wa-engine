# Android AAR Binding for wa-engine

This directory contains the build script and documentation for generating an Android AAR from the wa-engine Go source code.

## Prerequisites

1. **Go 1.21+**

   ```bash
   # Check version
   go version
   ```

2. **Android SDK**

   - Install via Android Studio or command line
   - Set `ANDROID_HOME` environment variable:
     ```bash
     export ANDROID_HOME=$HOME/Library/Android/sdk  # macOS
     export ANDROID_HOME=$HOME/Android/Sdk          # Linux
     ```

3. **Android NDK**

   - Install via Android Studio SDK Manager, or:
     ```bash
     sdkmanager --install "ndk;25.2.9519653"
     ```

4. **gomobile**
   ```bash
   go install golang.org/x/mobile/cmd/gomobile@latest
   gomobile init
   ```

## Building the AAR

```bash
# From this directory
./build-aar.sh

# Or specify output directory
./build-aar.sh /path/to/output
```

The script will generate `waengine.aar` in the `build/` directory (or specified output directory).

## Integration in Android Studio

### 1. Copy AAR to Project

```bash
cp build/waengine.aar /path/to/your/android/app/libs/
```

### 2. Update build.gradle (app level)

```gradle
android {
    // ...
}

dependencies {
    implementation files('libs/waengine.aar')
    // ... other dependencies
}
```

### 3. Sync Project

Click "Sync Now" in Android Studio or run:

```bash
./gradlew sync
```

## Usage in Kotlin

```kotlin
import waengine.Waengine

class WhatsAppManager(private val context: Context) {

    private var pollingJob: Job? = null

    fun initialize() {
        // Initialize engine with data directory
        val dataDir = "${context.filesDir.absolutePath}/whatsapp"
        val error = Waengine.newEngine(dataDir)
        if (error != null) {
            Log.e("WA", "Failed to initialize: ${error.message}")
            return
        }
    }

    fun connect() {
        // Start connection
        val error = Waengine.start()
        if (error != null) {
            Log.e("WA", "Failed to start: ${error.message}")
            return
        }

        // Start polling for events
        startEventPolling()
    }

    fun disconnect() {
        pollingJob?.cancel()
        Waengine.stop()
    }

    private fun startEventPolling() {
        pollingJob = CoroutineScope(Dispatchers.IO).launch {
            while (isActive) {
                val eventJson = Waengine.pollEvent()
                if (eventJson.isNotEmpty()) {
                    handleEvent(eventJson)
                }
                delay(50) // Poll every 50ms
            }
        }
    }

    private suspend fun handleEvent(json: String) {
        withContext(Dispatchers.Main) {
            val event = JSONObject(json)
            when (event.getString("type")) {
                "qr.updated" -> {
                    val qrCode = event.getJSONObject("data").getString("code")
                    // Display QR code to user
                    showQRCode(qrCode)
                }
                "connection.open" -> {
                    Log.i("WA", "Connected to WhatsApp")
                }
                "logged_in" -> {
                    val jid = event.getJSONObject("data").getString("jid")
                    Log.i("WA", "Logged in as: $jid")
                }
                "message.received" -> {
                    val data = event.getJSONObject("data")
                    val text = data.optString("text", "")
                    val sender = data.getString("sender_jid")
                    // Handle received message
                    onMessageReceived(sender, text)
                }
                "error" -> {
                    val data = event.getJSONObject("data")
                    Log.e("WA", "Error: ${data.getString("message")}")
                }
            }
        }
    }

    fun sendMessage(phone: String, text: String): String? {
        val jid = Waengine.formatPhoneJID(phone)
        return try {
            val (msgId, error) = Waengine.sendText(jid, text)
            if (error != null) {
                Log.e("WA", "Send failed: ${error.message}")
                null
            } else {
                msgId
            }
        } catch (e: Exception) {
            Log.e("WA", "Send exception: ${e.message}")
            null
        }
    }

    fun isLoggedIn(): Boolean = Waengine.isLoggedIn()

    fun getInfo(): String = Waengine.getInfo()
}
```

## Usage in Java

```java
import waengine.Waengine;

public class WhatsAppManager {

    private Context context;
    private Handler handler = new Handler(Looper.getMainLooper());
    private boolean polling = false;

    public WhatsAppManager(Context context) {
        this.context = context;
    }

    public void initialize() throws Exception {
        String dataDir = context.getFilesDir().getAbsolutePath() + "/whatsapp";
        Exception error = Waengine.newEngine(dataDir);
        if (error != null) {
            throw error;
        }
    }

    public void connect() throws Exception {
        Exception error = Waengine.start();
        if (error != null) {
            throw error;
        }
        startPolling();
    }

    public void disconnect() {
        polling = false;
        Waengine.stop();
    }

    private void startPolling() {
        polling = true;
        new Thread(() -> {
            while (polling) {
                String event = Waengine.pollEvent();
                if (!event.isEmpty()) {
                    handler.post(() -> handleEvent(event));
                }
                try {
                    Thread.sleep(50);
                } catch (InterruptedException e) {
                    break;
                }
            }
        }).start();
    }

    private void handleEvent(String json) {
        try {
            JSONObject event = new JSONObject(json);
            String type = event.getString("type");

            switch (type) {
                case "qr.updated":
                    String qrCode = event.getJSONObject("data").getString("code");
                    // Display QR code
                    break;
                case "connection.open":
                    // Connected
                    break;
                case "message.received":
                    JSONObject data = event.getJSONObject("data");
                    String text = data.optString("text", "");
                    String sender = data.getString("sender_jid");
                    // Handle message
                    break;
            }
        } catch (JSONException e) {
            Log.e("WA", "JSON parse error", e);
        }
    }

    public String sendMessage(String phone, String text) {
        String jid = Waengine.formatPhoneJID(phone);
        try {
            return Waengine.sendText(jid, text);
        } catch (Exception e) {
            Log.e("WA", "Send failed", e);
            return null;
        }
    }
}
```

## API Reference

### Initialization

| Function                      | Description                           |
| ----------------------------- | ------------------------------------- |
| `Waengine.newEngine(dataDir)` | Initialize engine with data directory |
| `Waengine.destroy()`          | Clean up and release resources        |

### Connection

| Function                | Description                |
| ----------------------- | -------------------------- |
| `Waengine.start()`      | Start WhatsApp connection  |
| `Waengine.stop()`       | Stop connection            |
| `Waengine.isLoggedIn()` | Check if authenticated     |
| `Waengine.getQR()`      | Get current QR code string |
| `Waengine.logout()`     | Clear session and logout   |

### Messaging

| Function                                            | Description           |
| --------------------------------------------------- | --------------------- |
| `Waengine.sendText(jid, text)`                      | Send text message     |
| `Waengine.sendTextReply(jid, text, quotedId)`       | Send reply            |
| `Waengine.setTyping(jid, typing)`                   | Send typing indicator |
| `Waengine.markRead(chatJid, senderJid, messageIds)` | Mark messages as read |

### Events

| Function                       | Description                           |
| ------------------------------ | ------------------------------------- |
| `Waengine.pollEvent()`         | Get next event as JSON (non-blocking) |
| `Waengine.getEventQueueSize()` | Get pending event count               |
| `Waengine.clearEvents()`       | Clear all pending events              |

### Utilities

| Function                           | Description             |
| ---------------------------------- | ----------------------- |
| `Waengine.formatPhoneJID(phone)`   | Convert phone to JID    |
| `Waengine.formatGroupJID(groupId)` | Convert group ID to JID |
| `Waengine.validateJID(jid)`        | Check if JID is valid   |
| `Waengine.getInfo()`               | Get engine info as JSON |
| `Waengine.getState()`              | Get engine state        |
| `Waengine.getConnectionState()`    | Get connection state    |

## Event Types

| Event               | Data Fields                                               | Description               |
| ------------------- | --------------------------------------------------------- | ------------------------- |
| `qr.updated`        | `code`                                                    | New QR code for scanning  |
| `connection.open`   | -                                                         | Connected to WhatsApp     |
| `connection.closed` | `reason`                                                  | Disconnected              |
| `logged_in`         | `jid`, `push_name`, `is_new_user`                         | Authentication successful |
| `logged_out`        | -                                                         | Session ended             |
| `message.received`  | `id`, `chat_jid`, `sender_jid`, `text`, `timestamp`, etc. | New message               |
| `message.sent`      | `id`, `chat_jid`, `text`                                  | Message sent successfully |
| `error`             | `code`, `message`, `details`                              | Error occurred            |

## Troubleshooting

### Build Errors

**"gomobile: command not found"**

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
export PATH=$PATH:$(go env GOPATH)/bin
```

**"Android NDK not found"**

```bash
# Install NDK via sdkmanager
$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager --install "ndk;25.2.9519653"
```

**"cgo: C compiler not found"**

- macOS: `xcode-select --install`
- Linux: `sudo apt install build-essential`

### Runtime Errors

**"Engine not initialized"**

- Call `Waengine.newEngine()` before other functions

**"Not logged in"**

- Wait for `logged_in` event before sending messages
- Check `Waengine.isLoggedIn()` before operations

**"Storage failed"**

- Ensure data directory is writable
- Check available disk space

## Notes

- The AAR includes native libraries for arm64-v8a, armeabi-v7a, x86, and x86_64
- Minimum Android API level: 21 (Android 5.0)
- The engine runs on a background thread; events should be processed on the main thread for UI updates
