# wa-engine

A production-ready, cross-platform WhatsApp server built with Go and WhatsMeow, designed for desktop and server deployments (macOS, Linux, Windows).

## Overview

wa-engine provides a clean, stable HTTP API for WhatsApp functionality. It handles:

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
│   ├── session.go          # Multi-session manager
│   ├── engine.go           # Per-session engine and lifecycle
│   ├── client.go           # WhatsMeow client wrapper with state machine
│   ├── send.go             # Message sending with media support
│   ├── events.go           # Event system with bounded queue
│   ├── storage.go          # Session persistence (SQLite)
│   └── errors.go           # Stable error codes
│
├── cmd/server/             # HTTP server entry point
│   └── main.go
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

Build and run the server:

```bash
# Build for macOS
make mac

# Run the server
./build/waengine --port 8080 --data ./wa-data
```

Once running, open the PWA in your browser and connect to `http://localhost:8080`.

## Usage

### Start the Server

```bash
./build/waengine --port 8080 --data ./wa-data
```

### REST API

The server exposes a REST API to manage WhatsApp sessions. See `cmd/server/main.go` for the full API reference.

### Multi-Session Support

The server supports up to 5 concurrent WhatsApp accounts. Create and manage sessions via the REST API:
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

```bash
# Poll events for a session
curl http://localhost:8080/api/sessions/work/events
```

### App Restart (Existing Sessions)

Sessions persist in SQLite across restarts. On restart, the server auto-discovers existing sessions from disk.

## Lifecycle & App Restart Handling

### First Time (No Session)

```
1. POST /api/sessions/work/pair    Start QR pairing
2. GET  /api/sessions/work/qr      Poll for QR code
3. User scans QR in WhatsApp
4. Session stored in SQLite
```

### App Restart (Existing Sessions)

```
1. Server starts, auto-discovers sessions from disk
2. POST /api/sessions/work/start   Reconnect paired session
3. Wait for connection.open event
```

## REST API

The server exposes the following endpoints. All requests use `Content-Type: application/json`.
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

Events are delivered via `PollEvent()` as JSON strings.

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

The server and core library are thread-safe:

- **RWMutex protection**: SessionManager and Storage operations protected
- **Atomic state management**: Engine state uses atomic operations
- **Idempotent operations**: Safe to call Start/Stop multiple times
- **Event queue**: Thread-safe push/poll operations per session

## Design Decisions

### Why polling instead of callbacks?

A polling-based event queue is:

- Easy to integrate with any UI framework
- Non-blocking and predictable

### Why JSON for complex data?

JSON serialization allows:

- Forward compatibility (new fields don't break old code)
- Easy parsing in any language

### Why separate StartPairing()?

Clear separation between:

- `Start()`: Reconnect with existing session
- `StartPairing()`: Begin new QR authentication

This prevents accidental QR generation when a session exists.

### Why limit to 5 sessions?

Resource constraints:

- Each session maintains a WebSocket connection
- Each session has its own SQLite database
- Each session has its own event queue
- 5 sessions provide sufficient multi-account support without exhausting resources

### Why auto-discover sessions?

Seamless restart experience:

- Session manager scans dataDir for existing session directories
- No manual session registration required
- Sessions survive restarts

## Build Notes

### WhatsApp API Compatibility

This version is updated for the latest whatsmeow API (January 2026) with breaking changes:

- **Context Parameters**: All network operations now require `context.Context` as first parameter
- **Field Name Changes**: `StanzaId` → `StanzaID`, `Url` → `URL`, `FileEncSha256` → `FileEncSHA256`
- **Method Signatures**: `SendPresence()`, `SendChatPresence()`, `MarkRead()` now require context
- **Storage API**: `sqlstore.New()` now requires context and logger parameters
- **Error Handling**: `PairError.Error` is now a field, not a method

### Known Issues

- **Go Version**: Requires Go 1.24+ due to whatsmeow dependencies. Go 1.22 is no longer supported.

## Future Enhancements

- [ ] Video message support
- [ ] Document/file sending
- [ ] Group management (create, add members, etc.)
- [ ] Contact sync

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

- Follows existing code style and patterns
- Is thread-safe and idempotent where applicable
- Includes proper documentation
- Follows existing code style
