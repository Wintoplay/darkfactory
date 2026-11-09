package dev.darkfactory.mobile

import android.app.ActivityManager
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL
import java.nio.charset.StandardCharsets
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

/**
 * Polls the paired daemon's notification ring and turns new attention events
 * into local Android notifications. It is a foreground service so Android does
 * not freeze the poller when the app is in the background.
 *
 * The service talks to the daemon directly with the same pinned E2EE envelope
 * as the WebView proxy. No bearer token enters a URL, a log, or a notification.
 * There is no vendor push service and no outbound call except the paired host.
 */
class NotificationService : Service() {
    private lateinit var store: TokenStore
    private lateinit var manager: NotificationManager
    private lateinit var executor: ScheduledExecutorService
    private var scheduled = false
    private var hostId = ""
    private var label = ""
    private var cursor = 0L

    override fun onCreate() {
        super.onCreate()
        store = TokenStore(this)
        manager = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        createChannels()
        startForegroundCompat(foregroundNotification(""))
        executor = Executors.newSingleThreadScheduledExecutor { runnable ->
            Thread(runnable, "darkfactory-notify").apply { isDaemon = true }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        val connection = store.load()
        if (connection == null) {
            stopSelf()
            return START_NOT_STICKY
        }
        if (!scheduled) {
            scheduled = true
            executor.scheduleWithFixedDelay({ pollSafely() }, 0, POLL_SECONDS, TimeUnit.SECONDS)
        }
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        if (::executor.isInitialized) executor.shutdownNow()
        super.onDestroy()
    }

    private fun pollSafely() {
        try {
            pollOnce()
        } catch (_: RevokedException) {
            postEvent(
                JSONObject()
                    .put("id", 0)
                    .put("title", "Pairing expired")
                    .put("body", "This device was signed out. Open DarkFactory, forget the host, and pair again.")
            )
            stopSelf()
        } catch (_: SuiteDowngradeException) {
            postEvent(
                JSONObject()
                    .put("id", 0)
                    .put("title", "Update DarkFactory")
                    .put("body", "This host requires hybrid encryption. Update DarkFactory, forget the host, and pair again.")
            )
            stopSelf()
        } catch (error: Exception) {
            // The host may be asleep or Tailscale may be reconnecting. Keep the
            // foreground service alive and try again on the next interval.
            android.util.Log.w(TAG, "poll failed: ${error.javaClass.simpleName}: ${error.message}")
        }
    }

    private fun pollOnce() {
        val connection = store.load() ?: run {
            stopSelf()
            return
        }
        if (!connection.hasE2EE) {
            postEvent(
                JSONObject()
                    .put("id", 0)
                    .put("title", "Pairing required")
                    .put("body", "This saved host has no app-layer encryption key. Pair again.")
            )
            stopSelf()
            return
        }
        if (connection.hostId != hostId) {
            hostId = connection.hostId
            label = connection.label.ifEmpty { connection.base }
            cursor = preferences().getLong(cursorKey(hostId), 0L)
            manager.notify(FOREGROUND_ID, foregroundNotification(label))
        }

        val prefs = preferences()
        val bootstrapped = prefs.getBoolean(bootstrapKey(hostId), false)
        val page = fetch(connection, cursor, !bootstrapped)
        val next = page.optLong("next", cursor)
        val events = page.optJSONArray("events") ?: JSONArray()
        val previous = cursor
        android.util.Log.d(
            TAG,
            "poll host=$hostId next=$next cursor=$cursor events=${events.length()} " +
                "foreground=${appForeground()} canNotify=${canNotify()} baseline=${!bootstrapped}"
        )
        if (!appForeground() && canNotify()) {
            for (index in 0 until events.length()) {
                val event = events.optJSONObject(index) ?: continue
                if (event.optLong("id", 0) <= previous) continue
                // The daemon decides what may interrupt: replies and subagent
                // chats stay in the activity ring without waking the phone.
                // An older daemon has no field, so keep its behavior.
                if (!event.optBoolean("alert", true)) continue
                postEvent(event)
            }
        }
        cursor = next
        prefs.edit()
            .putLong(cursorKey(hostId), cursor)
            .putBoolean(bootstrapKey(hostId), true)
            .apply()
    }

    private fun fetch(connection: TokenStore.Connection, after: Long, baseline: Boolean): JSONObject {
        val cipher = E2EE.from(connection)
        val path = if (baseline) "/api/v1/notifications" else "/api/v1/notifications?after=$after"
        val request = cipher.sealRequest(
            "GET",
            path,
            mapOf("accept" to "application/json"),
            ByteArray(0)
        )
        var http: HttpURLConnection? = null
        try {
            val url = URL(connection.base.trimEnd('/') + "/api/v1/e2ee")
            http = url.openConnection() as HttpURLConnection
            http.requestMethod = "POST"
            http.connectTimeout = 10_000
            http.readTimeout = 20_000
            http.instanceFollowRedirects = false
            http.useCaches = false
            http.setRequestProperty("Content-Type", "application/json")
            http.setRequestProperty("Accept", "application/x-df-e2ee")
            http.setRequestProperty("X-DF-E2EE", "1")
            http.setRequestProperty("User-Agent", "DarkFactory-Android/0.1")
            val encoded = request.envelope.toByteArray(StandardCharsets.UTF_8)
            http.doOutput = true
            http.setFixedLengthStreamingMode(encoded.size)
            http.outputStream.use { upstream -> upstream.write(encoded) }

            val status = http.responseCode
            if (status == 401) throw RevokedException()
            if (status == 403) {
                val detail = try {
                    readErrorBody(http.errorStream)
                } catch (_: Exception) {
                    ""
                }
                if (detail.contains("e2ee_suite_downgrade")) throw SuiteDowngradeException()
            }
            if (status != 200) throw IOException("encrypted request returned HTTP $status")
            if (!http.contentType.orEmpty().startsWith("application/x-df-e2ee") &&
                !http.contentType.orEmpty().startsWith("application/octet-stream")
            ) {
                throw IOException("response is not an encrypted envelope")
            }

            val input = http.inputStream
            val body = ByteArrayOutputStream()
            EncryptedResponseReader.read(
                input,
                object : EncryptedResponseReader.Opener {
                    override fun openData(frame: ByteArray, index: Int): ByteArray = request.openFrame(frame, index)
                    override fun openEnd(frame: ByteArray, index: Int): ByteArray? = request.openEndFrame(frame, index)
                },
                object : EncryptedResponseReader.Listener {
                    override fun onHead(plain: ByteArray) {
                        val head = JSONObject(String(plain, StandardCharsets.UTF_8))
                        val inner = head.optInt("status", 500)
                        if (inner != 200) throw IOException("daemon returned HTTP $inner")
                    }

                    override fun onBody(plain: ByteArray) {
                        body.write(plain)
                    }
                },
                MAX_NOTIFICATION_FRAME,
                MAX_NOTIFICATION_BODY,
                request.hasAuthenticatedEndMarker
            )
            return JSONObject(String(body.toByteArray(), StandardCharsets.UTF_8))
        } finally {
            http?.disconnect()
        }
    }

    private fun postEvent(event: JSONObject) {
        if (!canNotify()) return
        val kind = event.optString("kind", "")
        val blocking = kind == "run.blocked" || kind == "run.waiting" ||
            kind == "opencode.permission" || kind == "opencode.question" ||
            kind == "session.lost"
        val channel = if (blocking) CHANNEL_ATTENTION else CHANNEL_ACTIVITY
        val title = safeText(event.optString("title", "DarkFactory"))
        val body = safeText(event.optString("body", ""))
        val open = openAppIntent()
        val publicVersion = Notification.Builder(this, CHANNEL_ATTENTION)
            .setSmallIcon(R.drawable.ic_stat_darkfactory)
            .setContentTitle("DarkFactory")
            .setContentText("An agent needs your attention.")
            .build()
        val builder = Notification.Builder(this, channel)
            .setSmallIcon(R.drawable.ic_stat_darkfactory)
            .setContentTitle(title.ifEmpty { "DarkFactory" })
            .setContentText(body)
            .setStyle(Notification.BigTextStyle().bigText(body))
            .setAutoCancel(true)
            .setContentIntent(open)
            .setVisibility(Notification.VISIBILITY_PRIVATE)
            .setPublicVersion(publicVersion)
            .setGroup(if (blocking) GROUP_ATTENTION else GROUP_ACTIVITY)
            .setWhen(System.currentTimeMillis())
        if (body.isNotEmpty()) builder.setTicker(body)
        val id = (if (blocking) ATTENTION_BASE else ACTIVITY_BASE) +
            ((event.optLong("id", 0) % 100_000L).toInt())
        manager.notify(id, builder.build())
    }

    private fun foregroundNotification(label: String): Notification {
        val text = if (label.isEmpty()) "Watching for agent attention" else "Watching $label"
        return Notification.Builder(this, CHANNEL_SERVICE)
            .setSmallIcon(R.drawable.ic_stat_darkfactory)
            .setContentTitle("DarkFactory")
            .setContentText(text)
            .setOngoing(true)
            .setShowWhen(false)
            .setContentIntent(openAppIntent())
            .addAction(0, "Stop", stopIntent())
            .build()
    }

    private fun openAppIntent(): PendingIntent {
        val intent = Intent(this, MainActivity::class.java).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        }
        return PendingIntent.getActivity(
            this,
            0,
            intent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
    }

    private fun stopIntent(): PendingIntent {
        val intent = Intent(this, NotificationService::class.java).setAction(ACTION_STOP)
        return PendingIntent.getService(
            this,
            1,
            intent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
    }

    private fun createChannels() {
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_SERVICE, "Background connection", NotificationManager.IMPORTANCE_LOW).apply {
                description = "Keeps the paired daemon reachable for agent notifications."
                setShowBadge(false)
            }
        )
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ATTENTION, "Agent attention", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "A blocked run, a permission request, a question, or a lost session."
                enableVibration(true)
            }
        )
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ACTIVITY, "Agent activity", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "A finished session or a chat reply. Informational, no vibration."
                enableVibration(false)
            }
        )
    }

    private fun startForegroundCompat(notification: Notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(
                FOREGROUND_ID,
                notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC
            )
        } else {
            startForeground(FOREGROUND_ID, notification)
        }
    }

    private fun appForeground(): Boolean {
        val manager = getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager
        val processes = manager.runningAppProcesses ?: return false
        return processes.any {
            it.processName == packageName && it.importance == ActivityManager.RunningAppProcessInfo.IMPORTANCE_FOREGROUND
        }
    }

    private fun canNotify(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return true
        return checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
    }

    private fun preferences() = getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)

    private fun cursorKey(host: String) = "cursor_$host"

    private fun bootstrapKey(host: String) = "bootstrap_$host"

    private fun safeText(value: String): String {
        val cleaned = value.map { if (it.code < 0x20 || it.code == 0x7f) ' ' else it }
            .joinToString("")
            .trim()
        return if (cleaned.length > 500) cleaned.take(500) + "…" else cleaned
    }

    /** Reads a refusal body for its error code; never trusted for content. */
    private fun readErrorBody(stream: InputStream?): String {
        if (stream == null) return ""
        stream.use { input ->
            val output = ByteArrayOutputStream()
            val buffer = ByteArray(4096)
            while (output.size() < 64 * 1024) {
                val read = input.read(buffer)
                if (read == -1) break
                output.write(buffer, 0, read)
            }
            return String(output.toByteArray(), StandardCharsets.UTF_8)
        }
    }

    private class RevokedException : IOException("device revoked")

    private class SuiteDowngradeException : IOException("host requires hybrid encryption")

    companion object {
        private const val MAX_NOTIFICATION_FRAME = 1024 * 1024
        private const val MAX_NOTIFICATION_BODY = 4L * 1024 * 1024
        private const val ACTION_STOP = "dev.darkfactory.mobile.STOP_NOTIFICATIONS"
        private const val CHANNEL_SERVICE = "darkfactory-service"
        private const val CHANNEL_ATTENTION = "darkfactory-attention"
        private const val CHANNEL_ACTIVITY = "darkfactory-activity"
        private const val GROUP_ATTENTION = "darkfactory-attention"
        private const val GROUP_ACTIVITY = "darkfactory-activity"
        private const val FOREGROUND_ID = 1
        private const val ATTENTION_BASE = 2000
        private const val ACTIVITY_BASE = 3000
        private const val POLL_SECONDS = 15L
        private const val PREFERENCES = "darkfactory-notify"
        private const val TAG = "DarkFactoryNotify"

        fun start(context: Context) {
            if (TokenStore(context).load() == null) return
            val intent = Intent(context, NotificationService::class.java)
            context.startForegroundService(intent)
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, NotificationService::class.java))
        }
    }
}
