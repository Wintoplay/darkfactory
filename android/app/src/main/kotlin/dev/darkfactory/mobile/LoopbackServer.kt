package dev.darkfactory.mobile

import android.content.Context
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.HttpURLConnection
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.URL
import java.net.URLDecoder
import java.nio.charset.StandardCharsets
import java.security.SecureRandom
import java.util.Locale
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/**
 * The app-owned origin. It serves the bundled UI and proxies API calls to the
 * paired daemon through the encrypted envelope. The page is same-origin with
 * the proxy, so the existing fetch code needs no CORS relaxation, and the token
 * never enters JavaScript storage or a plaintext upstream request.
 */
internal class LoopbackServer(
    context: Context,
    private val base: String,
    private val e2ee: E2EE?,
    private val onLogout: ((String) -> Unit)?
) {
    private val context = context.applicationContext
    private val executor: ExecutorService = Executors.newCachedThreadPool()
    private var server: ServerSocket? = null

    /**
     * A per-launch loopback name. The socket binds 127.0.0.1, but WebView sends
     * this random name in Host, so another app on the device cannot guess the
     * proxy origin and use it as a confused deputy. The name is not a secret
     * against a root attacker; it is a boundary against an ordinary app.
     */
    val originHost: String = "df-" + randomHex(12) + ".localhost"

    @Volatile
    var port: Int = 0
        private set

    @Synchronized
    fun start(): Int {
        if (server != null) return port
        val socket = ServerSocket(0, 32, InetAddress.getByName("127.0.0.1"))
        server = socket
        port = socket.localPort
        Thread({ acceptLoop() }, "darkfactory-loopback").apply {
            isDaemon = true
            start()
        }
        return port
    }

    @Synchronized
    fun stop() {
        try {
            server?.close()
        } catch (_: IOException) {
            // Already closed.
        }
        server = null
        executor.shutdownNow()
    }

    private fun acceptLoop() {
        while (true) {
            val current = server ?: return
            try {
                val socket = current.accept()
                executor.execute { handle(socket) }
            } catch (_: IOException) {
                if (current.isClosed) return
            }
        }
    }

    private fun handle(socket: Socket) {
        try {
            socket.use { client ->
                client.soTimeout = 20_000
                val input = client.getInputStream()
                val output = client.getOutputStream()

                val requestLine = readLine(input) ?: return
                if (requestLine.isEmpty()) return
                val parts = requestLine.split(" ")
                if (parts.size != 3) {
                    writeError(output, 400, "invalid_request", "Malformed request line.")
                    return
                }
                val method = parts[0].uppercase(Locale.ROOT)
                val target = parts[1]
                if (!target.startsWith("/")) {
                    writeError(
                        output,
                        400,
                        "invalid_request",
                        "Only origin-form request targets are accepted."
                    )
                    return
                }

                val headers = readHeaders(input)
                if (headers == null) {
                    writeError(output, 431, "headers_too_large", "Request headers are too large.")
                    return
                }
                if (!validHost(headers["host"])) {
                    writeError(
                        output,
                        421,
                        "invalid_host",
                        "This origin accepts only its own loopback host."
                    )
                    return
                }
                val length = contentLength(headers)
                if (length < 0 || length > MAX_BODY) {
                    writeError(output, 413, "body_too_large", "Request body is too large.")
                    return
                }
                val body = readBody(input, length.toInt())

                if (method == "OPTIONS") {
                    writeEmpty(output, 204, null)
                    return
                }
                if (target.startsWith("/api/") || target == "/healthz") {
                    proxy(output, method, target, headers, body)
                    return
                }
                serveAsset(output, method, target)
            }
        } catch (_: Exception) {
            // The client may have opened and closed a preflight socket. There is
            // nothing useful to write after a broken connection.
        }
    }

    private fun serveAsset(output: OutputStream, method: String, target: String) {
        if (method != "GET" && method != "HEAD") {
            writeError(output, 405, "method_not_allowed", "The bundled UI is read-only.")
            return
        }
        var rawPath = target
        val query = rawPath.indexOf('?')
        if (query >= 0) rawPath = rawPath.substring(0, query)
        val decoded = try {
            URLDecoder.decode(rawPath, StandardCharsets.UTF_8.name())
        } catch (_: Exception) {
            writeError(output, 400, "invalid_path", "The asset path is not valid.")
            return
        }
        val path = if (decoded.isEmpty() || decoded == "/") "/index.html" else decoded
        if (path.contains("..") || path.contains("\\") || path.contains("//")) {
            writeError(output, 404, "not_found", "No such asset.")
            return
        }
        val bytes = try {
            context.assets.open("web$path").use { readAll(it, MAX_ASSET) }
        } catch (_: Exception) {
            writeError(output, 404, "not_found", "No such asset.")
            return
        }

        val responseHeaders = mutableMapOf(
            "Content-Type" to contentType(path),
            "Cache-Control" to "no-store",
            "X-Content-Type-Options" to "nosniff",
            "Referrer-Policy" to "no-referrer",
            "X-Frame-Options" to "DENY"
        )
        var body = bytes
        if (path == "/index.html") {
            val nonce = randomHex(16)
            body = String(bytes, StandardCharsets.UTF_8)
                .replace("{{CSP_NONCE}}", nonce)
                .toByteArray(StandardCharsets.UTF_8)
            responseHeaders["Content-Security-Policy"] =
                "default-src 'self'; style-src 'self' 'nonce-$nonce'; " +
                    "style-src-attr 'unsafe-inline'; frame-ancestors 'none'; " +
                    "base-uri 'none'; form-action 'self'"
        }
        writeBytes(output, 200, responseHeaders, body, method == "HEAD")
    }

    private fun proxy(
        output: OutputStream,
        method: String,
        target: String,
        headers: Map<String, String>,
        body: ByteArray
    ) {
        if (target.startsWith("/api/")) {
            val cipher = e2ee
            if (cipher == null) {
                // A saved row without a usable key must never fall back to a
                // plaintext bearer request. Pairing is the only recovery.
                writeError(
                    output,
                    502,
                    "e2ee_required",
                    "This host has no app-layer encryption key. Pair again."
                )
                return
            }
            proxyE2EE(output, method, target, headers, body, cipher)
            return
        }
        proxyLegacy(output, method, target, headers, body)
    }

    // Public daemon routes only. It deliberately sends no credentials: every
    // /api/ call goes through proxyE2EE.
    private fun proxyLegacy(
        output: OutputStream,
        method: String,
        target: String,
        headers: Map<String, String>,
        body: ByteArray
    ) {
        var connection: HttpURLConnection? = null
        var headWritten = false
        try {
            val http = URL(base + target).openConnection() as HttpURLConnection
            connection = http
            http.connectTimeout = 10_000
            var stream = target.endsWith("/stream") || target.endsWith("/opencode/events")
            http.readTimeout = if (stream) 0 else 30_000
            http.requestMethod = method
            http.instanceFollowRedirects = false
            http.useCaches = false
            http.setRequestProperty("Accept-Encoding", "identity")
            http.setRequestProperty("User-Agent", "DarkFactory-Android/0.1")
            copyHeader(headers, http, "content-type", "Content-Type")
            copyHeader(headers, http, "accept", "Accept")
            copyHeader(headers, http, "last-event-id", "Last-Event-ID")
            if (method != "GET" && method != "HEAD") {
                http.setRequestProperty("X-DF-Request", "1")
            }
            if (body.isNotEmpty()) {
                http.doOutput = true
                http.setFixedLengthStreamingMode(body.size)
                http.outputStream.use { upstream -> upstream.write(body) }
            }

            val status = http.responseCode
            val reason = http.responseMessage?.takeIf { it.isNotEmpty() } ?: reasonPhrase(status)
            val responseType = http.contentType
            if (responseType != null &&
                (responseType.startsWith("text/event-stream") ||
                    responseType.startsWith("application/octet-stream"))
            ) {
                stream = true
            }
            val responseHeaders = mutableMapOf(
                "Content-Type" to (responseType ?: "application/json; charset=utf-8"),
                "Cache-Control" to (http.getHeaderField("Cache-Control") ?: "no-store"),
                "X-Content-Type-Options" to "nosniff",
                "Referrer-Policy" to "no-referrer"
            )
            http.getHeaderField("X-DF-Redraw")?.let { responseHeaders["X-DF-Redraw"] = it }

            writeHead(output, status, reason, responseHeaders, true)
            headWritten = true

            val upstream = if (status >= 400) http.errorStream else http.inputStream
            upstream?.use { input ->
                val buffer = ByteArray(16 * 1024)
                while (true) {
                    val read = input.read(buffer)
                    if (read == -1) break
                    if (read == 0) continue
                    writeChunk(output, buffer, read)
                }
            }
            writeChunkEnd(output)
            output.flush()

            if (status == 401 ||
                (status == 204 && target == "/api/v1/open/logout")
            ) {
                onLogout?.invoke("This device was signed out. Pair again.")
            }
        } catch (_: Exception) {
            if (!headWritten) {
                writeError(output, 502, "daemon_unreachable", "The paired daemon is unreachable.")
            }
        } finally {
            connection?.disconnect()
        }
    }

    private fun proxyE2EE(
        output: OutputStream,
        method: String,
        target: String,
        headers: Map<String, String>,
        body: ByteArray,
        cipher: E2EE
    ) {
        var connection: HttpURLConnection? = null
        var headWritten = false
        try {
            val forwarded = mutableMapOf<String, String>()
            // The page origin is the app's random localhost host. Forwarding it
            // would make the daemon compare it with the daemon Host and refuse
            // every mutation. The envelope itself is the authenticated channel.
            for (name in listOf("content-type", "accept", "last-event-id", "user-agent")) {
                headers[name]?.takeIf { it.isNotEmpty() }?.let { forwarded[name] = it }
            }
            if (method != "GET" && method != "HEAD") forwarded["x-df-request"] = "1"
            val sealed = cipher.sealRequest(method, target, forwarded, body)

            val http = URL("$base/api/v1/e2ee").openConnection() as HttpURLConnection
            connection = http
            http.connectTimeout = 10_000
            val streaming = target.endsWith("/stream") || target.endsWith("/opencode/events")
            http.readTimeout = if (streaming) 0 else 30_000
            http.requestMethod = "POST"
            http.doOutput = true
            http.instanceFollowRedirects = false
            http.useCaches = false
            http.setRequestProperty("Content-Type", "application/json")
            http.setRequestProperty("Accept", "application/x-df-e2ee")
            http.setRequestProperty("X-DF-E2EE", "1")
            http.setRequestProperty("User-Agent", "DarkFactory-Android/0.1")
            val encoded = sealed.envelope.toByteArray(StandardCharsets.UTF_8)
            http.setFixedLengthStreamingMode(encoded.size)
            http.outputStream.use { upstream -> upstream.write(encoded) }

            val status = http.responseCode
            if (status != 200) {
                val detail = try {
                    readAll(http.errorStream, MAX_ERROR).toString(StandardCharsets.UTF_8)
                } catch (_: Exception) {
                    ""
                }
                val refusalCode = try {
                    JSONObject(detail).optString("code", "")
                } catch (_: Exception) {
                    ""
                }
                if (status == 401) {
                    onLogout?.invoke(
                        "This device was signed out or revoked. Forget the host and pair again."
                    )
                } else if (status == 403 && refusalCode == "e2ee_suite_downgrade") {
                    writeError(
                        output,
                        403,
                        "e2ee_suite_downgrade",
                        "This host requires hybrid encryption. Update DarkFactory, forget the host, and pair again."
                    )
                } else if (status == 429) {
                    // The daemon's envelope budget is transient. Pass the status
                    // through so the page can retry the same input batch instead
                    // of showing an identity failure it must not re-pair for.
                    writeError(
                        output,
                        429,
                        "e2ee_rate_limited",
                        "The host is rate-limiting encrypted requests. Retrying.",
                        retryAfter = "1"
                    )
                } else {
                    writeError(
                        output,
                        502,
                        "e2ee_failed",
                        if (detail.isBlank()) "Encrypted request failed (HTTP $status)." else detail
                    )
                }
                return
            }
            val type = http.contentType.orEmpty()
            if (!type.startsWith("application/x-df-e2ee")) {
                throw IOException("response is not an encrypted envelope")
            }

            val input = http.inputStream
            var logout = false
            var innerStatus = 0
            EncryptedResponseReader.read(
                input,
                object : EncryptedResponseReader.Opener {
                    override fun openData(frame: ByteArray, index: Int): ByteArray = sealed.openFrame(frame, index)
                    override fun openEnd(frame: ByteArray, index: Int): ByteArray? = sealed.openEndFrame(frame, index)
                },
                object : EncryptedResponseReader.Listener {
                    override fun onHead(plain: ByteArray) {
                        val head = JSONObject(String(plain, StandardCharsets.UTF_8))
                        innerStatus = head.optInt("status", 500)
                        val responseHeaders = mutableMapOf<String, String>()
                        head.optJSONObject("headers")?.let { objectHeaders ->
                            for (name in objectHeaders.keys()) {
                                responseHeaders[name] = objectHeaders.optString(name, "")
                            }
                        }
                        localWrite {
                            writeHead(output, innerStatus, reasonPhrase(innerStatus), responseHeaders, true)
                        }
                        headWritten = true
                        if (innerStatus == 401 ||
                            (innerStatus == 204 && target == "/api/v1/open/logout")
                        ) {
                            logout = true
                        }
                    }

                    override fun onBody(plain: ByteArray) {
                        if (plain.isNotEmpty()) {
                            localWrite { writeChunk(output, plain, plain.size) }
                        }
                    }
                },
                MAX_FRAME,
                -1,
                sealed.hasAuthenticatedEndMarker
            )
            if (!headWritten) throw IOException("encrypted response had no head frame")
            localWrite {
                writeChunkEnd(output)
                output.flush()
            }
            if (logout) onLogout?.invoke("This device was signed out. Pair again.")
        } catch (_: LocalClientGone) {
            // The WebView aborted its fetch (navigation, tab switch, or a
            // replaced stream). That is not a host-identity failure.
            return
        } catch (error: javax.crypto.AEADBadTagException) {
            onLogout?.invoke("The encrypted host identity or reply was invalid. Pair again.")
        } catch (error: org.json.JSONException) {
            onLogout?.invoke("The encrypted host identity or reply was invalid. Pair again.")
        } catch (error: IllegalStateException) {
            onLogout?.invoke("The encrypted host identity or reply was invalid. Pair again.")
        } catch (error: IOException) {
            // A framing or upstream I/O error after headers must leave the
            // local chunked body incomplete. The WebView can then treat the
            // response as failed and reconnect instead of accepting truncation.
            if (!headWritten) {
                try {
                    writeError(
                        output,
                        502,
                        "e2ee_failed",
                        "The paired daemon did not answer with a valid encrypted envelope."
                    )
                } catch (_: Exception) {
                    // The local client is gone too.
                }
            }
        } catch (error: Exception) {
            if (!headWritten) {
                try {
                    writeError(
                        output,
                        502,
                        "e2ee_failed",
                        "The paired daemon did not answer with a valid encrypted envelope."
                    )
                } catch (_: Exception) {
                    // The local client is gone too.
                }
            } else {
                onLogout?.invoke("The encrypted host identity or reply was invalid. Pair again.")
            }
        } finally {
            connection?.disconnect()
        }
    }

    private class LocalClientGone : IOException("local client closed")

    private fun localWrite(block: () -> Unit) {
        try {
            block()
        } catch (error: IOException) {
            throw LocalClientGone()
        }
    }

    private fun copyHeader(
        source: Map<String, String>,
        target: HttpURLConnection,
        sourceName: String,
        targetName: String
    ) {
        val value = source[sourceName]
        if (!value.isNullOrEmpty()) target.setRequestProperty(targetName, value)
    }

    private fun validHost(host: String?): Boolean {
        val value = host?.trim()?.lowercase(Locale.ROOT) ?: return false
        return value == "$originHost:$port"
    }

    private fun contentLength(headers: Map<String, String>): Long {
        val value = headers["content-length"]?.trim().orEmpty()
        if (value.isEmpty()) return 0
        return value.toLongOrNull() ?: -1
    }

    private fun readHeaders(input: InputStream): MutableMap<String, String>? {
        val headers = mutableMapOf<String, String>()
        var total = 0
        while (true) {
            val line = readLine(input) ?: return null
            total += line.length
            if (total > MAX_HEADERS) return null
            if (line.isEmpty()) return headers
            val colon = line.indexOf(':')
            if (colon <= 0) continue
            val name = line.substring(0, colon).trim().lowercase(Locale.ROOT)
            val value = line.substring(colon + 1).trim()
            headers[name] = headers[name]?.let { "$it,$value" } ?: value
        }
    }

    private fun readBody(input: InputStream, length: Int): ByteArray {
        val body = ByteArray(length)
        var offset = 0
        while (offset < length) {
            val read = input.read(body, offset, length - offset)
            if (read < 0) throw IOException("request body ended early")
            offset += read
        }
        return body
    }

    private fun readAll(input: InputStream, limit: Int): ByteArray {
        input.use { stream ->
            val output = ByteArrayOutputStream()
            val buffer = ByteArray(8192)
            var total = 0
            while (true) {
                val read = stream.read(buffer)
                if (read == -1) break
                total += read
                if (total > limit) throw IOException("asset too large")
                output.write(buffer, 0, read)
            }
            return output.toByteArray()
        }
    }

    private fun readLine(input: InputStream): String? {
        val line = ByteArrayOutputStream()
        var value = input.read()
        while (value != -1) {
            if (value == '\n'.code) break
            if (value != '\r'.code) line.write(value)
            if (line.size() > MAX_LINE) throw IOException("line too long")
            value = input.read()
        }
        if (value == -1 && line.size() == 0) return null
        return line.toString(StandardCharsets.ISO_8859_1.name())
    }

    private fun contentType(path: String): String {
        val lower = path.lowercase(Locale.ROOT)
        return when {
            lower.endsWith(".html") -> "text/html; charset=utf-8"
            lower.endsWith(".js") -> "text/javascript; charset=utf-8"
            lower.endsWith(".css") -> "text/css; charset=utf-8"
            lower.endsWith(".json") -> "application/json; charset=utf-8"
            lower.endsWith(".svg") -> "image/svg+xml"
            lower.endsWith(".png") -> "image/png"
            lower.endsWith(".ico") -> "image/x-icon"
            lower.endsWith(".woff2") -> "font/woff2"
            lower.endsWith(".woff") -> "font/woff"
            else -> "application/octet-stream"
        }
    }

    private fun writeBytes(
        output: OutputStream,
        status: Int,
        headers: Map<String, String>,
        body: ByteArray,
        headOnly: Boolean
    ) {
        writeHead(output, status, reasonPhrase(status), headers, false)
        if (!headOnly) output.write(body)
        output.flush()
    }

    private fun writeHead(
        output: OutputStream,
        status: Int,
        reason: String,
        headers: Map<String, String>,
        chunked: Boolean
    ) {
        val head = StringBuilder()
        head.append("HTTP/1.1 ").append(status).append(' ').append(reason).append("\r\n")
        for ((name, value) in headers) {
            if (name.equals("Content-Length", ignoreCase = true) ||
                name.equals("Transfer-Encoding", ignoreCase = true) ||
                name.equals("Connection", ignoreCase = true)
            ) {
                continue
            }
            head.append(name).append(": ").append(value).append("\r\n")
        }
        if (chunked) head.append("Transfer-Encoding: chunked\r\n")
        head.append("Connection: close\r\n\r\n")
        output.write(head.toString().toByteArray(StandardCharsets.ISO_8859_1))
    }

    private fun writeEmpty(output: OutputStream, status: Int, headers: Map<String, String>?) {
        writeHead(output, status, reasonPhrase(status), headers ?: emptyMap(), false)
        output.flush()
    }

    private fun writeChunk(output: OutputStream, buffer: ByteArray, length: Int) {
        output.write(Integer.toHexString(length).toByteArray(StandardCharsets.US_ASCII))
        output.write("\r\n".toByteArray(StandardCharsets.US_ASCII))
        output.write(buffer, 0, length)
        output.write("\r\n".toByteArray(StandardCharsets.US_ASCII))
        output.flush()
    }

    private fun writeChunkEnd(output: OutputStream) {
        output.write("0\r\n\r\n".toByteArray(StandardCharsets.US_ASCII))
    }

    private fun writeError(output: OutputStream, status: Int, code: String, detail: String, retryAfter: String? = null) {
        try {
            val body = JSONObject()
                .put("code", code)
                .put("detail", detail)
                .toString()
                .toByteArray(StandardCharsets.UTF_8)
            val headers = mutableMapOf(
                "Content-Type" to "application/json; charset=utf-8",
                "Cache-Control" to "no-store"
            )
            if (retryAfter != null) headers["Retry-After"] = retryAfter
            writeBytes(output, status, headers, body, false)
        } catch (_: Exception) {
            // Connection is already gone.
        }
    }

    private fun randomHex(bytes: Int): String {
        val raw = ByteArray(bytes)
        RANDOM.nextBytes(raw)
        return raw.joinToString("") {
            String.format(Locale.ROOT, "%02x", it.toInt() and 0xff)
        }
    }

    private fun reasonPhrase(status: Int): String = when (status) {
        200 -> "OK"
        201 -> "Created"
        204 -> "No Content"
        301 -> "Moved Permanently"
        302 -> "Found"
        304 -> "Not Modified"
        400 -> "Bad Request"
        401 -> "Unauthorized"
        403 -> "Forbidden"
        404 -> "Not Found"
        405 -> "Method Not Allowed"
        409 -> "Conflict"
        413 -> "Content Too Large"
        421 -> "Misdirected Request"
        429 -> "Too Many Requests"
        431 -> "Request Header Fields Too Large"
        500 -> "Internal Server Error"
        502 -> "Bad Gateway"
        503 -> "Service Unavailable"
        else -> "Status"
    }

    private companion object {
        const val MAX_HEADERS = 64 * 1024
        const val MAX_BODY = 16 * 1024 * 1024
        const val MAX_ASSET = 4 * 1024 * 1024
        const val MAX_LINE = 16 * 1024
        const val MAX_FRAME = 20 * 1024 * 1024
        const val MAX_ERROR = 1 * 1024 * 1024
        val RANDOM = SecureRandom()
    }
}
