package com.waengine

import android.util.Log
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import com.facebook.react.bridge.*
import com.facebook.react.modules.core.DeviceEventManagerModule
import org.json.JSONArray
import org.json.JSONObject
import waengine.Waengine
import java.util.concurrent.*
import java.util.concurrent.atomic.AtomicBoolean

/**
 * WaEngineModule - React Native Native Module for WhatsApp SDK
 *
 * ARCHITECTURE DECISIONS:
 * 
 * 1. POLLING STRATEGY:
 *    - One ScheduledFuture per active session (not one executor per session)
 *    - Shared ScheduledExecutorService with configurable thread pool
 *    - 500ms polling interval (configurable)
 *    - Graceful handling of empty poll results
 *
 * 2. LIFECYCLE MANAGEMENT:
 *    - ProcessLifecycleOwner observes app foreground/background
 *    - Polling pauses when app goes background (saves battery)
 *    - Polling resumes when app returns to foreground
 *    - All polling stops on module destroy
 *
 * 3. THREAD SAFETY:
 *    - ConcurrentHashMap for session → polling task mapping
 *    - AtomicBoolean for foreground state
 *    - All Go SDK calls happen on executor threads, never on JS thread
 *
 * 4. EVENT EMISSION:
 *    - All events emitted via RCTDeviceEventEmitter
 *    - Event name: "wa-engine-event"
 *    - Payload includes sessionName for JS-side routing
 *
 * 5. ERROR HANDLING:
 *    - Go errors wrapped in JS-friendly format
 *    - Promises used for all async operations
 *    - No raw Go errors exposed to JS
 */
class WaEngineModule(private val reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext), DefaultLifecycleObserver {

    companion object {
        private const val TAG = "WaEngineModule"
        private const val MODULE_NAME = "WaEngineModule"
        private const val EVENT_NAME = "wa-engine-event"
        
        // Polling configuration
        private const val POLL_INTERVAL_MS = 500L
        private const val EXECUTOR_POOL_SIZE = 3
    }

    // Shared executor for all polling tasks
    // Using scheduled executor allows precise timing without busy loops
    private val executor: ScheduledExecutorService = Executors.newScheduledThreadPool(
        EXECUTOR_POOL_SIZE,
        ThreadFactory { r ->
            Thread(r, "WaEngine-Poller").apply {
                isDaemon = true
                priority = Thread.NORM_PRIORITY - 1 // Slightly lower priority
            }
        }
    )

    // Maps sessionName -> ScheduledFuture for polling task
    // ConcurrentHashMap ensures thread-safe access from multiple threads
    private val pollingTasks = ConcurrentHashMap<String, ScheduledFuture<*>>()

    // Tracks whether app is in foreground
    // AtomicBoolean for thread-safe reads/writes
    private val isInForeground = AtomicBoolean(true)

    // Tracks if module has been destroyed
    private val isDestroyed = AtomicBoolean(false)

    // Tracks initialized state
    private var isInitialized = false

    init {
        // Register lifecycle observer on main thread
        reactContext.runOnUiQueueThread {
            ProcessLifecycleOwner.get().lifecycle.addObserver(this)
        }
    }

    override fun getName(): String = MODULE_NAME

    // =========================================================================
    // LIFECYCLE OBSERVER - App Foreground/Background
    // =========================================================================

    /**
     * Called when app comes to foreground.
     * Resumes polling for all active sessions.
     */
    override fun onStart(owner: LifecycleOwner) {
        Log.d(TAG, "App entered foreground")
        isInForeground.set(true)
        resumeAllPolling()
    }

    /**
     * Called when app goes to background.
     * Pauses polling to save battery.
     */
    override fun onStop(owner: LifecycleOwner) {
        Log.d(TAG, "App entered background")
        isInForeground.set(false)
        pauseAllPolling()
    }

    // =========================================================================
    // INITIALIZATION
    // =========================================================================

    /**
     * Initialize the WhatsApp engine with a data directory.
     * Must be called before any other method.
     *
     * @param dataDir App-private directory for SQLite databases
     */
    @ReactMethod
    fun init(dataDir: String, promise: Promise) {
        executor.execute {
            try {
                if (isDestroyed.get()) {
                    promise.reject("ERR_DESTROYED", "Module has been destroyed")
                    return@execute
                }

                Waengine.init(dataDir)
                isInitialized = true
                Log.d(TAG, "WaEngine initialized with dataDir: $dataDir")
                promise.resolve(null)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to initialize: ${e.message}", e)
                promise.reject("ERR_INIT_FAILED", wrapError(e))
            }
        }
    }

    // =========================================================================
    // SESSION LIFECYCLE
    // =========================================================================

    /**
     * Start a session (reconnects if already paired).
     * Automatically starts polling for this session.
     *
     * @param sessionName Unique identifier for this WhatsApp account
     */
    @ReactMethod
    fun startSession(sessionName: String, promise: Promise) {
        executor.execute {
            try {
                ensureInitialized()
                
                Waengine.startSession(sessionName)
                Log.d(TAG, "Session started: $sessionName")
                
                // Start polling for this session
                startPollingForSession(sessionName)
                
                promise.resolve(null)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to start session $sessionName: ${e.message}", e)
                promise.reject("ERR_START_SESSION", wrapError(e))
            }
        }
    }

    /**
     * Start QR pairing for a new session.
     * Automatically starts polling to receive QR code events.
     *
     * @param sessionName Unique identifier for this WhatsApp account
     */
    @ReactMethod
    fun startPairing(sessionName: String, promise: Promise) {
        executor.execute {
            try {
                ensureInitialized()
                
                Waengine.startPairingSession(sessionName)
                Log.d(TAG, "Pairing started: $sessionName")
                
                // Start polling to receive QR code events
                startPollingForSession(sessionName)
                
                promise.resolve(null)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to start pairing $sessionName: ${e.message}", e)
                promise.reject("ERR_START_PAIRING", wrapError(e))
            }
        }
    }

    /**
     * Stop a session and its polling loop.
     *
     * @param sessionName Session to stop
     */
    @ReactMethod
    fun stopSession(sessionName: String, promise: Promise) {
        executor.execute {
            try {
                // Stop polling first (even if Go call fails)
                stopPollingForSession(sessionName)
                
                Waengine.stopSession(sessionName)
                Log.d(TAG, "Session stopped: $sessionName")
                
                promise.resolve(null)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to stop session $sessionName: ${e.message}", e)
                promise.reject("ERR_STOP_SESSION", wrapError(e))
            }
        }
    }

    /**
     * Stop all sessions and all polling loops.
     */
    @ReactMethod
    fun stopAll(promise: Promise) {
        executor.execute {
            try {
                // Stop all polling loops
                stopAllPolling()
                
                Waengine.stopAll()
                Log.d(TAG, "All sessions stopped")
                
                promise.resolve(null)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to stop all sessions: ${e.message}", e)
                promise.reject("ERR_STOP_ALL", wrapError(e))
            }
        }
    }

    // =========================================================================
    // SESSION STATE QUERIES
    // =========================================================================

    /**
     * Check if a session has stored credentials (paired with WhatsApp).
     */
    @ReactMethod
    fun isPaired(sessionName: String, promise: Promise) {
        executor.execute {
            try {
                val paired = Waengine.isPairedSession(sessionName)
                promise.resolve(paired)
            } catch (e: Exception) {
                Log.e(TAG, "isPaired failed: ${e.message}", e)
                promise.reject("ERR_IS_PAIRED", wrapError(e))
            }
        }
    }

    /**
     * Check if a session is currently connected to WhatsApp servers.
     */
    @ReactMethod
    fun isConnected(sessionName: String, promise: Promise) {
        executor.execute {
            try {
                val connected = Waengine.isConnectedSession(sessionName)
                promise.resolve(connected)
            } catch (e: Exception) {
                Log.e(TAG, "isConnected failed: ${e.message}", e)
                promise.reject("ERR_IS_CONNECTED", wrapError(e))
            }
        }
    }

    /**
     * Get list of all session names.
     * Returns a JSON array string.
     */
    @ReactMethod
    fun listSessions(promise: Promise) {
        executor.execute {
            try {
                val sessionsJson = Waengine.listSessions()
                val sessions = parseJsonArray(sessionsJson)
                promise.resolve(sessions)
            } catch (e: Exception) {
                Log.e(TAG, "listSessions failed: ${e.message}", e)
                promise.reject("ERR_LIST_SESSIONS", wrapError(e))
            }
        }
    }

    /**
     * Get detailed info for all sessions.
     */
    @ReactMethod
    fun getAllSessionsInfo(promise: Promise) {
        executor.execute {
            try {
                val infoJson = Waengine.getAllSessionsInfo()
                val info = parseJsonToMap(infoJson)
                promise.resolve(info)
            } catch (e: Exception) {
                Log.e(TAG, "getAllSessionsInfo failed: ${e.message}", e)
                promise.reject("ERR_GET_ALL_INFO", wrapError(e))
            }
        }
    }

    /**
     * Get event queue statistics for a specific session.
     * Useful for debugging polling behavior and queue backpressure.
     *
     * @param sessionName Which session to get stats for
     *
     * RETURNS:
     * {
     *   queueSize: number,      // Current events waiting in queue
     *   totalEnqueued: number,  // Total events added since session start
     *   totalPolled: number,    // Total events successfully polled
     *   dropped: number         // Events dropped due to queue full
     * }
     */
    @ReactMethod
    fun getEventQueueStats(sessionName: String, promise: Promise) {
        executor.execute {
            try {
                val statsJson = Waengine.getEventQueueStatsSession(sessionName)
                val stats = parseJsonToMap(statsJson)
                
                // Add native-side polling info
                val isPolling = pollingTasks.containsKey(sessionName)
                val isAppForeground = isInForeground.get()
                
                val result = Arguments.createMap().apply {
                    putMap("queue", stats)
                    putBoolean("isPolling", isPolling)
                    putBoolean("isAppForeground", isAppForeground)
                    putInt("activePollingTasks", pollingTasks.size)
                }
                
                promise.resolve(result)
            } catch (e: Exception) {
                Log.e(TAG, "getEventQueueStats failed: ${e.message}", e)
                promise.reject("ERR_GET_QUEUE_STATS", wrapError(e))
            }
        }
    }

    // =========================================================================
    // MESSAGING
    // =========================================================================

    /**
     * Send a text message via a specific session.
     *
     * @param to Recipient JID (e.g., "1234567890@s.whatsapp.net")
     * @param sessionName Which account to send from
     * @param message Text content
     */
    @ReactMethod
    fun sendMessage(to: String, sessionName: String, message: String, promise: Promise) {
        executor.execute {
            try {
                ensureInitialized()
                
                val messageId = Waengine.sendTextSession(to, sessionName, message)
                Log.d(TAG, "Message sent via $sessionName to $to, id: $messageId")
                
                val result = Arguments.createMap().apply {
                    putString("messageId", messageId)
                }
                promise.resolve(result)
            } catch (e: Exception) {
                Log.e(TAG, "sendMessage failed: ${e.message}", e)
                promise.reject("ERR_SEND_MESSAGE", wrapError(e))
            }
        }
    }

    /**
     * Send an image with caption via a specific session.
     *
     * @param to Recipient JID
     * @param sessionName Which account to send from
     * @param image URL or base64 data URI
     * @param caption Optional caption text
     */
    @ReactMethod
    fun sendImageWithCaption(
        to: String,
        sessionName: String,
        image: String,
        caption: String,
        promise: Promise
    ) {
        executor.execute {
            try {
                ensureInitialized()
                
                val messageId = Waengine.sendImageWithCaptionSession(to, sessionName, image, caption)
                Log.d(TAG, "Image sent via $sessionName to $to, id: $messageId")
                
                val result = Arguments.createMap().apply {
                    putString("messageId", messageId)
                }
                promise.resolve(result)
            } catch (e: Exception) {
                Log.e(TAG, "sendImageWithCaption failed: ${e.message}", e)
                promise.reject("ERR_SEND_IMAGE", wrapError(e))
            }
        }
    }

    // =========================================================================
    // POLLING LOGIC
    // =========================================================================

    /**
     * Start polling for a specific session.
     * Creates a scheduled task that polls every POLL_INTERVAL_MS.
     *
     * DESIGN NOTES:
     * - Uses scheduleWithFixedDelay (not scheduleAtFixedRate) to prevent
     *   task accumulation if polling takes longer than interval
     * - Polling task is a Runnable, not a loop - executor handles scheduling
     * - Each poll result is emitted individually to JS
     */
    private fun startPollingForSession(sessionName: String) {
        // Don't start if already polling or destroyed
        if (pollingTasks.containsKey(sessionName) || isDestroyed.get()) {
            Log.d(TAG, "Polling already active or module destroyed for: $sessionName")
            return
        }

        Log.d(TAG, "Starting polling for session: $sessionName")

        val pollTask = Runnable {
            // Skip if paused (background) or destroyed
            if (!isInForeground.get() || isDestroyed.get()) {
                return@Runnable
            }

            // CONNECTION-AWARE: Only poll if session is actually connected
            // This saves battery by avoiding useless poll calls when:
            // - Session is disconnecting/disconnected
            // - Session failed to connect
            // - Network is unavailable
            try {
                if (!Waengine.isConnectedSession(sessionName)) {
                    return@Runnable
                }
            } catch (e: Exception) {
                // Session may not exist anymore - stop polling
                Log.d(TAG, "Session $sessionName not found, skipping poll")
                return@Runnable
            }

            try {
                val eventJson = Waengine.pollEventSession(sessionName)
                
                // Empty string means no events available
                if (eventJson.isNullOrEmpty()) {
                    return@Runnable
                }

                // Parse and emit event
                emitEvent(sessionName, eventJson)
                
            } catch (e: Exception) {
                // Log but don't crash - session may have been stopped
                Log.w(TAG, "Poll error for $sessionName: ${e.message}")
            }
        }

        // Schedule with fixed delay - waits POLL_INTERVAL_MS after each execution completes
        val future = executor.scheduleWithFixedDelay(
            pollTask,
            0, // Initial delay
            POLL_INTERVAL_MS,
            TimeUnit.MILLISECONDS
        )

        pollingTasks[sessionName] = future
    }

    /**
     * Stop polling for a specific session.
     * Cancels the scheduled task and removes from map.
     */
    private fun stopPollingForSession(sessionName: String) {
        val future = pollingTasks.remove(sessionName)
        if (future != null) {
            future.cancel(false) // Don't interrupt if running
            Log.d(TAG, "Stopped polling for session: $sessionName")
        }
    }

    /**
     * Pause all polling (used when app goes background).
     * Tasks remain scheduled but skip execution when isInForeground is false.
     *
     * NOTE: We don't cancel tasks here because we want to resume quickly
     * when app returns to foreground. The polling tasks check isInForeground
     * at the start of each execution.
     */
    private fun pauseAllPolling() {
        Log.d(TAG, "Pausing all polling (${pollingTasks.size} sessions)")
        // isInForeground is already set to false by onStop()
        // Polling tasks will skip execution
    }

    /**
     * Resume all polling (used when app returns to foreground).
     * Tasks were never cancelled, they just start executing again.
     */
    private fun resumeAllPolling() {
        Log.d(TAG, "Resuming all polling (${pollingTasks.size} sessions)")
        // isInForeground is already set to true by onStart()
        // Polling tasks will resume execution
    }

    /**
     * Stop all polling tasks and clear the map.
     * Used by stopAll() and onCatalystInstanceDestroy().
     */
    private fun stopAllPolling() {
        Log.d(TAG, "Stopping all polling (${pollingTasks.size} sessions)")
        
        pollingTasks.forEach { (sessionName, future) ->
            future.cancel(false)
            Log.d(TAG, "Cancelled polling for: $sessionName")
        }
        pollingTasks.clear()
    }

    // =========================================================================
    // EVENT EMISSION
    // =========================================================================

    /**
     * Emit an event to JavaScript via RCTDeviceEventEmitter.
     *
     * @param sessionName Session that generated this event
     * @param eventJson Raw JSON string from Go SDK
     *
     * EVENT PAYLOAD FORMAT:
     * {
     *   sessionName: string,  // Which session this event belongs to
     *   type: string,         // Event type (e.g., "qr.updated", "message.received")
     *   data: object          // Event-specific data
     * }
     */
    private fun emitEvent(sessionName: String, eventJson: String) {
        try {
            val eventObj = JSONObject(eventJson)
            
            // Build the JS event payload
            val payload = Arguments.createMap().apply {
                putString("sessionName", sessionName)
                putString("type", eventObj.optString("type", "unknown"))
                
                // Convert data object to WritableMap
                val dataObj = eventObj.optJSONObject("data")
                if (dataObj != null) {
                    putMap("data", jsonObjectToWritableMap(dataObj))
                } else {
                    putMap("data", Arguments.createMap())
                }
                
                // Include timestamp if present
                if (eventObj.has("timestamp")) {
                    putDouble("timestamp", eventObj.optDouble("timestamp"))
                }
            }

            // Emit to JS
            reactContext
                .getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
                ?.emit(EVENT_NAME, payload)

            Log.v(TAG, "Emitted event: ${eventObj.optString("type")} for $sessionName")
            
        } catch (e: Exception) {
            Log.e(TAG, "Failed to emit event: ${e.message}", e)
        }
    }

    // =========================================================================
    // CLEANUP
    // =========================================================================

    /**
     * Called when React Native destroys this module.
     * Cleans up all resources: polling tasks, executor, lifecycle observer.
     */
    override fun onCatalystInstanceDestroy() {
        Log.d(TAG, "onCatalystInstanceDestroy - cleaning up")
        
        isDestroyed.set(true)
        
        // Stop all polling
        stopAllPolling()
        
        // Shutdown executor gracefully
        executor.shutdown()
        try {
            if (!executor.awaitTermination(2, TimeUnit.SECONDS)) {
                executor.shutdownNow()
            }
        } catch (e: InterruptedException) {
            executor.shutdownNow()
        }
        
        // Remove lifecycle observer
        reactContext.runOnUiQueueThread {
            ProcessLifecycleOwner.get().lifecycle.removeObserver(this)
        }
        
        super.onCatalystInstanceDestroy()
    }

    // =========================================================================
    // HELPER METHODS
    // =========================================================================

    /**
     * Ensure the SDK has been initialized.
     * @throws IllegalStateException if not initialized
     */
    private fun ensureInitialized() {
        if (!isInitialized) {
            throw IllegalStateException("WaEngine not initialized. Call init() first.")
        }
        if (isDestroyed.get()) {
            throw IllegalStateException("WaEngine module has been destroyed.")
        }
    }

    /**
     * Wrap an exception into a user-friendly error message.
     * Extracts the actual error from Go SDK error format.
     */
    private fun wrapError(e: Exception): String {
        val message = e.message ?: "Unknown error"
        // Go errors often have format "[ERR_CODE] message"
        // Extract just the message part for cleaner JS errors
        return if (message.startsWith("[")) {
            message.substringAfter("] ", message)
        } else {
            message
        }
    }

    /**
     * Parse a JSON array string into a WritableArray.
     */
    private fun parseJsonArray(json: String): WritableArray {
        val result = Arguments.createArray()
        try {
            val arr = JSONArray(json)
            for (i in 0 until arr.length()) {
                result.pushString(arr.getString(i))
            }
        } catch (e: Exception) {
            Log.e(TAG, "Failed to parse JSON array: ${e.message}")
        }
        return result
    }

    /**
     * Parse a JSON object string into a WritableMap.
     */
    private fun parseJsonToMap(json: String): WritableMap {
        return try {
            val obj = JSONObject(json)
            jsonObjectToWritableMap(obj)
        } catch (e: Exception) {
            Log.e(TAG, "Failed to parse JSON object: ${e.message}")
            Arguments.createMap()
        }
    }

    /**
     * Convert a JSONObject to a WritableMap for React Native.
     * Handles nested objects and arrays recursively.
     */
    private fun jsonObjectToWritableMap(json: JSONObject): WritableMap {
        val map = Arguments.createMap()
        
        json.keys().forEach { key ->
            when (val value = json.opt(key)) {
                null, JSONObject.NULL -> map.putNull(key)
                is Boolean -> map.putBoolean(key, value)
                is Int -> map.putInt(key, value)
                is Long -> map.putDouble(key, value.toDouble())
                is Double -> map.putDouble(key, value)
                is String -> map.putString(key, value)
                is JSONObject -> map.putMap(key, jsonObjectToWritableMap(value))
                is JSONArray -> map.putArray(key, jsonArrayToWritableArray(value))
                else -> map.putString(key, value.toString())
            }
        }
        
        return map
    }

    /**
     * Convert a JSONArray to a WritableArray for React Native.
     */
    private fun jsonArrayToWritableArray(json: JSONArray): WritableArray {
        val array = Arguments.createArray()
        
        for (i in 0 until json.length()) {
            when (val value = json.opt(i)) {
                null, JSONObject.NULL -> array.pushNull()
                is Boolean -> array.pushBoolean(value)
                is Int -> array.pushInt(value)
                is Long -> array.pushDouble(value.toDouble())
                is Double -> array.pushDouble(value)
                is String -> array.pushString(value)
                is JSONObject -> array.pushMap(jsonObjectToWritableMap(value))
                is JSONArray -> array.pushArray(jsonArrayToWritableArray(value))
                else -> array.pushString(value.toString())
            }
        }
        
        return array
    }

    // =========================================================================
    // JS EVENT LISTENER TRACKING (Required for RN New Architecture)
    // =========================================================================

    /**
     * Called when JS adds a listener for our events.
     * Required for NativeEventEmitter on new architecture.
     */
    @ReactMethod
    fun addListener(eventName: String) {
        // Keep track of listeners if needed for optimization
        Log.d(TAG, "JS listener added for: $eventName")
    }

    /**
     * Called when JS removes listeners.
     * Required for NativeEventEmitter on new architecture.
     */
    @ReactMethod
    fun removeListeners(count: Int) {
        Log.d(TAG, "JS removed $count listeners")
    }
}
