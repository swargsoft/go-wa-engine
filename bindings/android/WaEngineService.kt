// WaEngineService.kt
// Thin Android foreground service that starts the wa-engine HTTP server.
//
// DROP THIS FILE into your Android app at:
//   app/src/main/java/com/yourapp/WaEngineService.kt
//
// REGISTER in AndroidManifest.xml inside <application>:
//   <service
//       android:name=".WaEngineService"
//       android:enabled="true"
//       android:exported="false"
//       android:foregroundServiceType="dataSync" />
//
// Also add permission:
//   <uses-permission android:name="android.permission.FOREGROUND_SERVICE" />
//   <uses-permission android:name="android.permission.FOREGROUND_SERVICE_DATA_SYNC" />
//
// START from your Activity or Application class:
//   val intent = Intent(this, WaEngineService::class.java)
//   ContextCompat.startForegroundService(this, intent)
//
// The PWA then connects to http://localhost:8080 from any browser on the device.

package com.yourapp

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import waengine.Waengine

class WaEngineService : Service() {

    private val TAG = "WaEngineService"
    private val CHANNEL_ID = "wa_engine_channel"
    private val NOTIFICATION_ID = 1001
    private val PORT = 8080L  // gomobile exports int as long

    private val serviceScope = CoroutineScope(Dispatchers.IO + SupervisorJob())

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForeground(NOTIFICATION_ID, buildNotification())

        val dataDir = "${filesDir.absolutePath}/wa-engine"

        // StartHTTPServer blocks until StopHTTPServer is called,
        // so run it on a background coroutine.
        serviceScope.launch {
            Log.i(TAG, "Starting wa-engine on port $PORT, data: $dataDir")
            val err = Waengine.startHTTPServer(PORT, dataDir)
            if (err.isNotEmpty()) {
                Log.e(TAG, "wa-engine error: $err")
            }
        }

        // STICKY: Android restarts this service after it's killed
        return START_STICKY
    }

    override fun onDestroy() {
        Log.i(TAG, "Stopping wa-engine")
        Waengine.stopHTTPServer()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    // --- Notification ---

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "WA Engine",
                NotificationManager.IMPORTANCE_LOW   // silent — no sound or vibration
            ).apply {
                description = "WhatsApp engine running in background"
                setShowBadge(false)
            }
            val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
            nm.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(): Notification {
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("WA Engine")
            .setContentText("Running on localhost:$PORT")
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setOngoing(true)               // can't be swiped away
            .setSilent(true)                // no sound
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }
}
