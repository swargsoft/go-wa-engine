# wa-engine

A production-ready, cross-platform WhatsApp SDK built with Go and WhatsMeow, designed for Android and iOS integration via gomobile.

## Overview

wa-engine provides a clean, stable API for WhatsApp functionality that can be embedded in mobile applications. It handles:

- **Multi-account support** - up to 5 concurrent WhatsApp sessions
- **QR code authentication** with proper lifecycle (qr.updated, qr.expired, pairing.success/failed)
- **Session persistence** via SQLite - survives app kills and restarts
- **Thread-safe, idempotent operations** - safe to call from any thread
- **Automatic reconnection** with configurable backoff
- **Message sending/receiving** with media support (images)
- **Event-based architecture** with bounded queue and overflow handling

## Key Features (v1.2.0)

- **Multi-Session Support**: Up to 5 concurrent WhatsApp accounts
- **Session Isolation**: Each session has its own SQLite database, event queue, and lifecycle
- **Auto-Discovery**: Existing sessions are auto-loaded on app restart
- **Idempotent Lifecycle**: `Start()`, `Stop()`, `StartPairing()` are safe to call multiple times
- **App Restart Resilient**: Sessions persist in SQLite; auto-reconnects on app restart
- **Clear Pairing Flow**: Separate `StartPairing()` API with explicit events
- **Media Support**: `SendImageWithCaption()` with URL or base64 input
- **Event Overflow Handling**: Bounded queue (1000 events) with `event.dropped` notifications
- **Connection-Aware Polling**: Only polls for events when session is connected (battery optimization)
- **Stable Error Codes**: Locked API contract for error handling
- **Latest WhatsApp API**: Compatible with whatsmeow v0.0.0-20260107124630-ccfa04f8e445
- **Backward Compatible**: Legacy single-session API still works using "default" session

## Architecture

```
wa-engine/
├── core/                    # Core Go implementation
│   ├── session.go          # Multi-session manager (NEW)
│   ├── engine.go           # Per-session engine and lifecycle
│   ├── client.go           # WhatsMeow client wrapper with state machine
│   ├── send.go             # Message sending with media support
│   ├── events.go           # Event system with bounded queue
│   ├── storage.go          # Session persistence (SQLite)
│   └── errors.go           # Stable error codes
│
├── bindings/               # Platform-specific bindings
│   └── android/
│       ├── build-aar.sh    # AAR build script
│       └── README.md       # Android integration guide
│
├── waengine.go             # Public gomobile-compatible API
├── go.mod
└── README.md
```

## Storage Layout

Each session has its own isolated SQLite database:

```
dataDir/
├── default/
│   └── wa.db              # Default session database
├── work/
│   └── wa.db              # "work" session database
├── personal/
│   └── wa.db              # "personal" session database
└── ...
```

## Quick Start

### Building for Android

#### Method 1: Docker Build (Recommended)

Due to gomobile compatibility issues with newer Go versions, Docker provides a reliable build environment:

```bash
# Prerequisites: Docker Desktop installed and running

cd bindings/android

# Build Docker image with Go 1.24 and dependencies
docker build --platform=linux/amd64 -f Dockerfile.aar-build -t wa-engine-builder ..

# Run container to build AAR
docker run --platform=linux/amd64 --rm -v "$(pwd)/build:/workspace/bindings/android/build" wa-engine-builder

# Output: build/waengine.aar (46MB)
```

#### Method 2: Local Build

```bash
# Prerequisites
# - Go 1.24+ (required for whatsmeow API)
# - Android SDK with NDK
# - gomobile: go install golang.org/x/mobile/cmd/gomobile@latest

# Note: May encounter gomobile compatibility issues on macOS
# Recommend using Docker method above

cd bindings/android
./build-aar.sh

# Output: build/waengine.aar
```

### Integration

See [bindings/android/README.md](bindings/android/README.md) for detailed Android integration instructions.

## Multi-Session Usage

### Setting Up Multiple Accounts

```kotlin
// Kotlin (Android)

// Initialize once on app start
waengine.Init(dataDir)

// Set up "work" account
if (!waengine.IsPairedSession("work")) {
    waengine.StartPairingSession("work")
    // Poll PollEventSession("work") for QR codes
} else {
    waengine.StartSession("work")
}

// Set up "personal" account
if (!waengine.IsPairedSession("personal")) {
    waengine.StartPairingSession("personal")
    // Poll PollEventSession("personal") for QR codes
} else {
    waengine.StartSession("personal")
}

// Send from specific account
waengine.SendTextSession(jid, "work", "Hello from work account!")
waengine.SendTextSession(jid, "personal", "Hello from personal!")
```

### Polling Events Per Session

```kotlin
// Poll each session's event queue
val workEvent = waengine.PollEventSession("work")
val personalEvent = waengine.PollEventSession("personal")

// Process events...
```

### App Restart (Existing Sessions)

```kotlin
// Init automatically discovers existing sessions from disk
waengine.Init(dataDir)

// Get list of existing sessions
val sessionsJson = waengine.ListSessions()
// Returns: ["default", "work", "personal"]

// Reconnect each paired session
for (session in sessions) {
    if (waengine.IsPairedSession(session)) {
        waengine.StartSession(session)
    }
}
```

## Lifecycle & App Restart Handling

### First Time (No Session)

```
1. Init(dataDir)                    // Initialize session manager
2. StartPairingSession("work")      // Begin QR authentication
3. Poll PollEventSession("work")    // Get qr.updated events
4. Wait for pairing.success         // User scanned QR
5. Session stored in SQLite         // Automatic
```

### App Restart (Existing Sessions)

```
1. Init(dataDir)                    // Auto-discovers existing sessions
2. IsPairedSession("work") == true  // Check for existing session
3. StartSession("work")             // Auto-reconnects, no QR needed
4. Wait for connection.open         // Connected!
```

### Recommended Startup Flow

```kotlin
// Kotlin (Android)
waengine.Init(dataDir)

// Reconnect all paired sessions
val sessions = parseJsonArray(waengine.ListSessions())
for (session in sessions) {
    if (waengine.IsPairedSession(session)) {
        waengine.StartSession(session)
    }
}
```

## Public API

The public API is designed for gomobile compatibility, using only supported types (strings, primitives, JSON).

### Initialization

```go
// Initialize session manager (call once on app start)
Init(dataDir string) error

// Clean up all sessions
Destroy()
```

### Multi-Session Lifecycle

```go
// Start connection for a session (auto-reconnects if paired)
StartSession(sessionName string) error

// Begin QR pairing for a session
StartPairingSession(sessionName string) error

// Stop a session connection
StopSession(sessionName string) error

// Stop all sessions (call before app termination)
StopAll()

// Permanently remove a session and its data
RemoveSession(sessionName string) error
```

### Multi-Session Status

```go
// Check if session has stored authentication
IsPairedSession(sessionName string) bool

// Check if session is currently connected
IsConnectedSession(sessionName string) bool

// Get current QR code for session
GetQRSession(sessionName string) string

// Get session's JID
GetJIDSession(sessionName string) string
```

### Multi-Session Messaging

```go
// Send text message via specific session
SendTextSession(to string, sessionName string, text string) (string, error)

// Send image with caption via specific session
SendImageWithCaptionSession(to string, sessionName string, imageSource string, caption string) (string, error)
```

### Multi-Session Events

```go
// Poll events for a specific session
PollEventSession(sessionName string) string

// Get event queue size for a session
GetEventQueueSizeSession(sessionName string) int

// Clear events for a session
ClearEventsSession(sessionName string)
```

### Session Management

```go
// List all session names (JSON array)
ListSessions() string

// Get number of active sessions
GetSessionCount() int

// Get detailed info for a session (JSON)
GetSessionInfo(sessionName string) string

// Get info for all sessions (JSON)
GetAllSessionsInfo() string

// Logout and clear session auth
LogoutSession(sessionName string) error
```

### Legacy Single-Session API (Backward Compatible)

These functions use the "default" session name:

```go
NewEngine(dataDir string) error  // Alias for Init()
Start() error                    // Uses "default" session
StartPairing() error             // Uses "default" session
Stop()                           // Uses "default" session
IsPaired() bool                  // Uses "default" session
SendText(jid, text string) (string, error)  // Uses "default" session
PollEvent() string               // Uses "default" session
```

### Connection Lifecycle

```go
// Start WhatsApp connection (auto-reconnects if session exists)
// IDEMPOTENT: safe to call multiple times
Start() error

// Begin QR pairing flow (use when IsPaired() is false)
// Emits: qr.updated, qr.expired, pairing.success, pairing.failed
StartPairing() error

// Stop connection (IDEMPOTENT)
Stop()

// Check if session exists in SQLite (works across app restarts)
IsPaired() bool

// Check if currently connected to WhatsApp servers
IsConnected() bool

// Get current QR code for authentication
GetQR() string

// Logout and clear session
Logout() error
```

### Messaging

```go
// Send text message (returns message ID)
SendText(jid string, text string) (string, error)

// Send text message with session name (preferred API)
SendMessage(to string, sessionName string, message string) (string, error)

// Send image with caption
// imageSource: URL (http/https) or base64 data URI
SendImageWithCaption(to string, sessionName string, imageSource string, caption string) (string, error)

// Send reply to a message
SendTextReply(jid string, text string, quotedID string) (string, error)

// Send typing indicator
SetTyping(jid string, typing bool) error

// Mark messages as read
MarkRead(chatJID string, senderJID string, messageIDsJSON string) error
```

### Events

```go
// Poll for next event (non-blocking, returns JSON or empty string)
// Recommended polling interval: 50-100ms
PollEvent() string

// Get number of pending events
GetEventQueueSize() int

// Get queue statistics (size, max, pushed, polled, dropped)
GetEventQueueStats() string

// Clear all pending events
ClearEvents()
```

### Utilities

```go
// Format phone number to JID
FormatPhoneJID(phone string) string  // "1234567890" -> "1234567890@s.whatsapp.net"

// Format group ID to JID
FormatGroupJID(groupID string) string  // "xxx-xxx" -> "xxx-xxx@g.us"

// Validate JID format
ValidateJID(jid string) bool

// Get engine info as JSON
GetInfo() string
```

## Event System

Events are delivered via `PollEvent()` as JSON strings. This design is gomobile-compatible and allows flexible event handling on any platform.

### Event Queue Behavior

- **Bounded Queue**: Maximum 1000 events
- **Overflow Strategy**: Drop oldest events when full
- **Overflow Notification**: `event.dropped` event emitted with count
- **Thread-Safe**: Safe to poll from any thread

### Event Structure

```json
{
  "type": "event_type",
  "timestamp": 1234567890123,
  "data": { ... }
}
```

### Event Types

| Type                      | Description                    | Data Fields                                   |
| ------------------------- | ------------------------------ | --------------------------------------------- |
| `qr.updated`              | New QR code available          | `code`                                        |
| `qr.expired`              | QR code expired, request new   | -                                             |
| `pairing.success`         | Device successfully paired     | `jid`                                         |
| `pairing.failed`          | Pairing failed                 | `reason`                                      |
| `connection.open`         | Connected to WhatsApp          | -                                             |
| `connection.closed`       | Disconnected                   | `reason`                                      |
| `connection.reconnecting` | Reconnection in progress       | -                                             |
| `logged_in`               | Successfully authenticated     | `jid`, `push_name`, `platform`, `is_new_user` |
| `logged_out`              | Session ended                  | -                                             |
| `message.received`        | New message received           | See below                                     |
| `message.sent`            | Message sent successfully      | `id`, `chat_jid`, `text`                      |
| `message.failed`          | Message send failed            | `to`, `error`, `code`                         |
| `event.dropped`           | Events dropped due to overflow | `count`                                       |
| `error`                   | Error occurred                 | `code`, `message`, `details`                  |

### Message Data Fields

```json
{
  "id": "message_id",
  "chat_jid": "1234567890@s.whatsapp.net",
  "sender_jid": "1234567890@s.whatsapp.net",
  "sender_name": "John Doe",
  "text": "Hello!",
  "timestamp": 1234567890123,
  "is_from_me": false,
  "is_group": false,
  "quoted_id": "quoted_message_id",
  "quoted_text": "Original message",
  "has_media": false,
  "media_type": "image",
  "media_mime_type": "image/jpeg"
}
```

## Error Codes (Stable API)

These error codes are locked as a stable API contract.

| Code                        | Description                          |
| --------------------------- | ------------------------------------ |
| `ERR_NOT_INITIALIZED`       | Engine not initialized               |
| `ERR_ALREADY_RUNNING`       | Engine already running               |
| `ERR_NOT_RUNNING`           | Engine not running                   |
| `ERR_NOT_LOGGED_IN`         | No active session                    |
| `ERR_NOT_PAIRED`            | No stored session (use StartPairing) |
| `ERR_NOT_CONNECTED`         | Not connected to server              |
| `ERR_CONNECTION_FAILED`     | Connection failure                   |
| `ERR_SEND_FAILED`           | Message send failure                 |
| `ERR_INVALID_JID`           | Invalid JID format                   |
| `ERR_STORAGE_FAILED`        | Storage operation failure            |
| `ERR_QR_EXPIRED`            | QR code expired                      |
| `ERR_LOGGED_OUT`            | User logged out                      |
| `ERR_MEDIA_FAILED`          | Media operation failure              |
| `ERR_MEDIA_DOWNLOAD_FAILED` | Media download failure               |
| `ERR_MEDIA_TOO_LARGE`       | Media exceeds size limit             |
| `ERR_SESSION_INVALID`       | Session corrupted/invalid            |
| `ERR_CONCURRENCY_VIOLATION` | Concurrent operation conflict        |
| `ERR_SESSION_NOT_FOUND`     | Session name doesn't exist           |
| `ERR_MAX_SESSIONS_EXCEEDED` | Maximum 5 sessions limit reached     |
| `ERR_INTERNAL`              | Internal error                       |

## Usage Example (Multi-Session)

```
// Initialize session manager
Init("/path/to/data")

// Set up two accounts
StartPairingSession("work")
StartPairingSession("personal")

// Event loop - poll each session
while running:
    workEvent = PollEventSession("work")
    personalEvent = PollEventSession("personal")

    // Handle work session events
    if workEvent is not empty:
        handleEvent("work", workEvent)

    // Handle personal session events
    if personalEvent is not empty:
        handleEvent("personal", personalEvent)

    sleep(50ms)

// Send from specific accounts
SendTextSession(jid, "work", "Message from work!")
SendTextSession(jid, "personal", "Message from personal!")

// Send image via work account
SendImageWithCaptionSession(
    jid,
    "work",
    "https://example.com/image.jpg",
    "Check this out!"
)

// Cleanup
StopAll()
Destroy()
```

## Usage Example (Single Session - Legacy)

```
// Initialize
NewEngine("/path/to/data")

// Check session state
if IsPaired():
    // Existing session - reconnect
    Start()
else:
    // New device - need QR pairing
    StartPairing()

// Event loop
while running:
    event = PollEvent()
    if event is not empty:
        switch event.type:
            case "qr.updated":
                displayQR(event.data.code)
            case "pairing.success":
                print("Paired as", event.data.jid)
            case "connection.open":
                print("Connected!")
            case "message.received":
                handleMessage(event.data)
    sleep(50ms)

// Send text message
SendText("1234567890@s.whatsapp.net", "Hello!")

// Cleanup
Stop()
Destroy()
```

## Thread Safety

All public API functions are thread-safe and can be called from any thread:

- **RWMutex protection**: SessionManager and Storage operations protected
- **Atomic state management**: Engine state uses atomic operations
- **Idempotent operations**: Safe to call Start/Stop multiple times
- **Event queue**: Thread-safe push/poll operations per session

## Design Decisions

### Why gomobile?

gomobile provides native performance with a single codebase. The Go core runs directly on device without any JavaScript runtime overhead.

### Why polling instead of callbacks?

gomobile has limited support for callbacks. A polling-based event queue is:

- Fully compatible with gomobile bind
- Easy to integrate with any UI framework
- Non-blocking and predictable

### Why JSON for complex data?

gomobile only supports primitive types. JSON serialization allows:

- Passing complex structures across the language boundary
- Forward compatibility (new fields don't break old code)
- Easy parsing in any language

### Why separate StartPairing()?

Clear separation between:

- `Start()`: Reconnect with existing session
- `StartPairing()`: Begin new QR authentication

This prevents accidental QR generation when a session exists.

### Why limit to 5 sessions?

Resource constraints on mobile devices:

- Each session maintains a WebSocket connection
- Each session has its own SQLite database
- Each session has its own event queue
- 5 sessions provide sufficient multi-account support without exhausting resources

### Why auto-discover sessions?

Seamless app restart experience:

- Init() scans dataDir for existing session directories
- No manual session registration required
- Sessions survive app kills and updates

## Build Notes

### WhatsApp API Compatibility

This version is updated for the latest whatsmeow API (January 2026) with breaking changes:

- **Context Parameters**: All network operations now require `context.Context` as first parameter
- **Field Name Changes**: `StanzaId` → `StanzaID`, `Url` → `URL`, `FileEncSha256` → `FileEncSHA256`
- **Method Signatures**: `SendPresence()`, `SendChatPresence()`, `MarkRead()` now require context
- **Storage API**: `sqlstore.New()` now requires context and logger parameters
- **Error Handling**: `PairError.Error` is now a field, not a method

### Docker Build Environment

The Docker build uses:

- **Base Image**: `golang:1.24-bookworm` (Debian 12)
- **Java**: OpenJDK 17 (for Android SDK tools)
- **Android NDK**: 25.1.8937393
- **gomobile**: Latest version compatible with Go 1.24
- **Platform**: `linux/amd64` (for cross-platform compatibility)

Build time: ~8-12 minutes (first build), ~2-3 minutes (cached)

### Known Issues

- **gomobile on macOS**: Native gomobile build may fail with "unable to import bind" error on macOS with Go 1.24+. Use Docker build method.
- **ARM64 Host**: Docker must use `--platform=linux/amd64` to avoid architecture compatibility issues
- **Go Version**: Requires Go 1.24+ due to whatsmeow dependencies. Go 1.22 is no longer supported.

## Future Enhancements

- [ ] Video message support
- [ ] Document/file sending
- [ ] Group management (create, add members, etc.)
- [ ] Contact sync
- [ ] iOS bindings
- [ ] Message history retrieval
- [ ] Presence updates
- [ ] Business API features

## Dependencies

- [whatsmeow](https://github.com/tulir/whatsmeow) v0.0.0-20260107124630-ccfa04f8e445 - WhatsApp Web API implementation
- [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3) v1.14.32 - SQLite driver for session storage
- Go 1.24+ - Required for latest whatsmeow API compatibility

## License

See LICENSE file.

## Contributing

Contributions are welcome! Please ensure all code:

- Maintains gomobile compatibility
- Is thread-safe and idempotent where applicable
- Includes proper documentation
- Follows existing code style
