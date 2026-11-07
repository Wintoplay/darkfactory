package dev.darkfactory.mobile

import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.nio.charset.StandardCharsets
import java.security.MessageDigest

/** Pairing is the only request the native app makes before a token exists. */
internal object NativePair {
    private const val MAX_RESPONSE = 1 shl 20

    /** A reachable daemon that no longer presents the pinned P-256 key. */
    class HostIdentityChangedException(message: String) : IllegalStateException(message)

    fun pair(rawBase: String, rawCode: String): TokenStore.Connection {
        val base = normalizeBase(rawBase)
        val code = rawCode.trim()
        if (code.isEmpty()) {
            throw IllegalArgumentException("Enter the pairing code from the desktop.")
        }

        val keyPair = E2EE.generateKeyPair()
        val clientPublicKey = E2EE.encode(keyPair.public)
        val clientPrivateKey = E2EE.encode(keyPair.private)
        val connection = openConnection("$base/api/v1/pair")
        connection.connectTimeout = 10_000
        connection.readTimeout = 15_000
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.instanceFollowRedirects = false
        connection.useCaches = false
        connection.setRequestProperty("Content-Type", "application/json")
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("X-DF-Request", "1")
        connection.setRequestProperty("User-Agent", "DarkFactory-Android/0.1")
        val body = JSONObject()
            .put("code", code)
            .put("clientPublicKey", clientPublicKey)
            .toString()
            .toByteArray(StandardCharsets.UTF_8)
        connection.setFixedLengthStreamingMode(body.size)
        try {
            connection.outputStream.use { output -> output.write(body) }
            val status = connection.responseCode
            val stream = if (status >= 400) connection.errorStream else connection.inputStream
            val text = readAll(stream)
            if (status !in 200..299) {
                throw IllegalStateException(refusal(text, status))
            }

            val response = JSONObject(text)
            val hostId = response.optString("hostId", "")
            val deviceId = response.optString("deviceId", "")
            val hostPublicKey = response.optString("hostPublicKey", "")
            val sealedToken = response.optString("sealedToken", "")
            val token = response.optString("token", "")
            val fingerprint = response.optString("fingerprint", "")
            if (hostId.isEmpty()) throw IllegalStateException("The daemon returned no host identity.")
            if (sealedToken.isNotEmpty()) {
                if (deviceId.isEmpty() || hostPublicKey.isEmpty()) {
                    throw IllegalStateException("The daemon returned an incomplete encrypted session.")
                }
                val hostPublic = E2EE.publicKey(hostPublicKey)
                val computedFingerprint = HostFingerprint.verified(hostPublicKey, fingerprint)
                val keys = E2EE.derive(keyPair.private, hostPublic, hostPublicKey, clientPublicKey)
                val plainToken = E2EE.openPairToken(
                    sealedToken,
                    keys.second,
                    "pair|$hostId|$deviceId|$clientPublicKey"
                )
                if (plainToken.isEmpty()) throw IllegalStateException("The daemon returned no device token.")
                return TokenStore.Connection(
                    base = base,
                    hostId = hostId,
                    token = plainToken,
                    deviceId = deviceId,
                    hostPublicKey = hostPublicKey,
                    clientPrivateKey = clientPrivateKey,
                    clientPublicKey = clientPublicKey,
                    fingerprint = computedFingerprint,
                    label = hostLabel(base),
                    scope = response.optString("scope", "")
                )
            }
            // A remote native client never falls back to a plaintext token in
            // the pairing response. The daemon is upgraded with the app.
            if (token.isNotEmpty()) {
                throw IllegalStateException("The daemon returned an unsealed token; update it and pair again.")
            }
            throw IllegalStateException("The daemon did not negotiate app-layer encryption; update it and pair again.")
        } catch (error: IllegalStateException) {
            throw error
        } catch (error: IOException) {
            throw IllegalStateException("The host is unreachable from this phone.", error)
        } catch (error: Exception) {
            throw IllegalStateException("The host did not answer a valid pairing response.", error)
        } finally {
            connection.disconnect()
        }
    }

    /**
     * Refuse a changed host key before the WebView loads. A stored E2EE
     * connection is useless after a rotation; silently falling back to bearer
     * traffic would defeat the pin.
     */
    fun verifyHost(connection: TokenStore.Connection) {
        if (!connection.hasE2EE) return
        val request = openConnection("${connection.base}/api/v1/e2ee/host-key")
        request.connectTimeout = 10_000
        request.readTimeout = 10_000
        request.requestMethod = "GET"
        request.instanceFollowRedirects = false
        request.useCaches = false
        request.setRequestProperty("Accept", "application/json")
        request.setRequestProperty("User-Agent", "DarkFactory-Android/0.1")
        try {
            val status = request.responseCode
            val stream = if (status >= 400) request.errorStream else request.inputStream
            val text = readAll(stream)
            if (status != 200) {
                throw IllegalStateException(
                    "This host could not prove its pinned key (HTTP $status)."
                )
            }
            val body = JSONObject(text)
            val publicKey = body.optString("publicKey", "")
            val hostId = body.optString("hostId", "")
            if (hostId.isNotEmpty() && hostId != connection.hostId) {
                throw HostIdentityChangedException(
                    "Host identity changed. Pair again and verify the new fingerprint."
                )
            }
            if (publicKey.isEmpty() ||
                !MessageDigest.isEqual(
                    publicKey.toByteArray(StandardCharsets.UTF_8),
                    connection.hostPublicKey.toByteArray(StandardCharsets.UTF_8)
                )
            ) {
                throw HostIdentityChangedException(
                    "Host identity changed. Pair again and verify the new fingerprint."
                )
            }
        } catch (error: HostIdentityChangedException) {
            throw error
        } catch (error: IllegalStateException) {
            throw error
        } catch (error: IOException) {
            throw IllegalStateException("The host is unreachable from this phone.", error)
        } catch (error: Exception) {
            throw IllegalStateException("The host did not answer a valid key check.", error)
        } finally {
            request.disconnect()
        }
    }

    private fun openConnection(url: String): HttpURLConnection {
        return try {
            URL(url).openConnection() as HttpURLConnection
        } catch (error: IOException) {
            throw IllegalStateException("The host is unreachable from this phone.", error)
        } catch (error: Exception) {
            throw IllegalStateException("The host URL is not usable.", error)
        }
    }

    fun normalizeBase(rawBase: String): String {
        var value = rawBase.trim()
        if (value.isEmpty()) throw IllegalArgumentException("Enter the daemon URL.")
        if (!value.contains("://")) value = "https://$value"
        val uri = try {
            URI(value)
        } catch (error: Exception) {
            throw IllegalArgumentException("That daemon URL is not valid.", error)
        }
        val scheme = uri.scheme?.lowercase() ?: ""
        if (scheme != "https" && scheme != "http") {
            throw IllegalArgumentException("Use an https:// daemon URL.")
        }
        val host = uri.host
        if (host.isNullOrEmpty()) throw IllegalArgumentException("The daemon URL has no host.")
        if (uri.userInfo != null) {
            throw IllegalArgumentException("Do not put credentials in the daemon URL.")
        }
        val path = uri.path.orEmpty()
        if (path.isNotEmpty() && path != "/") {
            throw IllegalArgumentException("Use the daemon root URL, without a path.")
        }
        if (scheme == "http" && !isLoopbackOrEmulator(host)) {
            throw IllegalArgumentException(
                "Plain http is allowed only on loopback or the Android emulator. Use Tailscale HTTPS."
            )
        }
        val authority = if (uri.port == -1) host else "$host:${uri.port}"
        return "$scheme://$authority"
    }

    fun hostLabel(base: String): String {
        return try {
            val authority = URI(base).authority
            if (authority.isNullOrEmpty()) base else authority
        } catch (_: Exception) {
            base
        }
    }

    private fun isLoopbackOrEmulator(host: String): Boolean {
        return host == "127.0.0.1" ||
            host == "::1" ||
            host == "[::1]" ||
            host.equals("localhost", ignoreCase = true) ||
            host == "10.0.2.2"
    }

    private fun readAll(stream: InputStream?): String {
        if (stream == null) return ""
        stream.use { input ->
            val output = ByteArrayOutputStream()
            val buffer = ByteArray(8192)
            var total = 0
            while (true) {
                val read = input.read(buffer)
                if (read == -1) break
                total += read
                if (total > MAX_RESPONSE) {
                    throw IllegalStateException("The daemon response was too large.")
                }
                output.write(buffer, 0, read)
            }
            return output.toByteArray().toString(StandardCharsets.UTF_8)
        }
    }

    private fun refusal(raw: String, status: Int): String {
        try {
            val body = JSONObject(raw)
            val detail = body.optString("detail", "")
            if (detail.isNotEmpty()) return detail
            val hint = body.optString("hint", "")
            if (hint.isNotEmpty()) return hint
            val code = body.optString("code", "")
            if (code.isNotEmpty()) return code
        } catch (_: Exception) {
            // Fall through to the status text.
        }
        return "Pairing failed (HTTP $status)."
    }
}
