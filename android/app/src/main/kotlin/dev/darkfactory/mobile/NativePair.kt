package dev.darkfactory.mobile

import android.util.Base64
import dev.darkfactory.mobile.crypto.MlKem768
import dev.darkfactory.mobile.crypto.X25519
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.security.SecureRandom

/**
 * Pairing is the only request the native app makes before a token exists.
 *
 * The host key is fetched and verified before the code is sent. The UI makes
 * the user compare the displayed pin first; v2 then seals the one-time code to
 * the signed bundle and the staged ephemeral/KEM values.
 */
internal object NativePair {
    private const val MAX_RESPONSE = 1 shl 20
    private val RANDOM = SecureRandom()

    /** A reachable daemon that no longer presents the pinned key. */
    class HostIdentityChangedException(message: String) : IllegalStateException(message)

    internal class HostKeyResponse(
        val hostId: String,
        val publicKeyB64: String,
        val legacyFingerprint: String,
        val bundle: HostKey?,
        val bundleJson: String
    ) {
        val hybrid: Boolean get() = bundle != null
    }

    class PreparedPair internal constructor(
        internal val base: String,
        internal val code: String,
        internal val host: HostKeyResponse
    ) {
        val fingerprint: String
            get() = host.bundle?.fingerprintV2 ?: host.legacyFingerprint
        val suite: String
            get() = if (host.hybrid) HostKey.SUITE else "P-256 ECDH"
    }

    fun prepare(
        rawBase: String,
        rawCode: String,
        knownHosts: List<TokenStore.Connection>
    ): PreparedPair {
        val base = normalizeBase(rawBase)
        val code = rawCode.trim()
        if (code.isEmpty()) {
            throw IllegalArgumentException("Enter the pairing code from the desktop.")
        }
        val host = fetchHostKey(base)
        if (!host.hybrid) {
            throw IllegalStateException(
                "This host does not publish a verified hybrid key bundle. Update the daemon before pairing."
            )
        }
        checkKnownPins(base, host, knownHosts)
        return PreparedPair(base, code, host)
    }

    fun complete(
        prepared: PreparedPair,
        knownHosts: List<TokenStore.Connection>
    ): TokenStore.Connection {
        checkKnownPins(prepared.base, prepared.host, knownHosts)
        return pairHybrid(prepared.base, prepared.code, prepared.host)
    }

    private fun checkKnownPins(
        base: String,
        host: HostKeyResponse,
        knownHosts: List<TokenStore.Connection>
    ) {
        val matching = knownHosts.filter { existing ->
            existing.hostId == host.hostId ||
                (existing.hostPublicKey.isNotEmpty() && sameKey(existing.hostPublicKey, host.publicKeyB64)) ||
                runCatching { normalizeBase(existing.base) == base }.getOrDefault(false)
        }
        if (matching.size > 1) {
            throw IllegalStateException("More than one saved host matches this identity. Explicitly Forget duplicate rows first.")
        }
        for (existing in matching) {
            HostPinPolicy.requireHostId(existing.hostId, host.hostId)
            if (!existing.hasE2EE) {
                throw IllegalStateException("This endpoint already has an unusable saved pin. Explicitly Forget it before pairing again.")
            }
            HostPinPolicy.requireReplacement(
                existing.hostPublicKey,
                existing.suite,
                existing.fingerprint,
                existing.fingerprintV2,
                host.publicKeyB64,
                host.bundle?.suite.orEmpty(),
                host.bundle?.fingerprintV2.orEmpty(),
                host.bundle?.fingerprintV2.orEmpty()
            )
            if (existing.isHybrid) {
                val oldBundle = HostBundle.stored(existing.hostBundle)
                oldBundle.requireStoredPin(
                    existing.hostPublicKey,
                    existing.suite,
                    existing.fingerprintV2
                )
                val nextBundle = host.bundle
                    ?: throw IllegalStateException("The saved hybrid host now lacks its signed bundle.")
                HostPinPolicy.requireBundleAdvance(
                    oldBundle.epoch,
                    oldBundle.bundleHash,
                    nextBundle.epoch,
                    nextBundle.bundleHash
                )
            }
        }
    }

    /**
     * Refuses a changed host key before the WebView loads. For a hybrid row the
     * bundle must still be signed by the pinned P-256 key and must still name
     * the pinned suite; the returned row carries the current bundle so a
     * rotation (same pin, new X25519/ML-KEM half) is picked up without re-pairing.
     */
    fun verifyHost(connection: TokenStore.Connection): TokenStore.Connection {
        val host = fetchHostKey(connection.base, allowLegacy = !connection.isHybrid)
        if (host.hostId.isNotEmpty() && connection.hostId.isNotEmpty() &&
            host.hostId != connection.hostId
        ) {
            throw HostIdentityChangedException(
                "Host identity changed. Pair again and verify the new fingerprint."
            )
        }
        if (connection.isHybrid) {
            val bundle = host.bundle ?: throw HostIdentityChangedException(
                "This host stopped offering the hybrid key exchange. Update it and pair again."
            )
            if (bundle.publicKeyB64 != connection.hostPublicKey) {
                throw HostIdentityChangedException(
                    "Host identity changed. Pair again and verify the new fingerprint."
                )
            }
            if (bundle.suite != connection.suite || bundle.fingerprintV2 != connection.fingerprintV2) {
                throw HostIdentityChangedException(
                    "The host's key bundle no longer matches the pinned suite. Pair again."
                )
            }
            val storedBundle = try {
                HostBundle.stored(connection.hostBundle).also {
                    it.requireStoredPin(connection.hostPublicKey, connection.suite, connection.fingerprintV2)
                }
            } catch (error: Exception) {
                throw HostIdentityChangedException("The saved host bundle is unusable. Explicitly Forget it before pairing again.")
            }
            try {
                HostPinPolicy.requireBundleAdvance(
                    storedBundle.epoch,
                    storedBundle.bundleHash,
                    bundle.epoch,
                    bundle.bundleHash
                )
            } catch (error: Exception) {
                throw HostIdentityChangedException(error.message ?: "The host key bundle rolled back.")
            }
            return if (host.bundleJson != connection.hostBundle) {
                connection.copy(hostBundle = host.bundleJson)
            } else {
                connection
            }
        }
        if (host.bundle != null) {
            // The daemon was upgraded while this row is still v1. It can keep
            // using the legacy suite only if the pinned P-256 key is unchanged.
            if (!sameKey(host.publicKeyB64, connection.hostPublicKey)) {
                throw HostIdentityChangedException(
                    "Host identity changed. Pair again and verify the new fingerprint."
                )
            }
            return connection
        }
        if (!sameKey(host.publicKeyB64, connection.hostPublicKey)) {
            throw HostIdentityChangedException(
                "Host identity changed. Pair again and verify the new fingerprint."
            )
        }
        return connection
    }

    private fun pairHybrid(base: String, code: String, host: HostKeyResponse): TokenStore.Connection {
        val bundle = host.bundle ?: throw IllegalStateException("The host key bundle is missing.")
        val ephemeral = X25519.generateKeyPair(RANDOM)
        val encapsulation = MlKem768.encapsulate(bundle.mlkem)
        val sharedDh = X25519.scalarMult(ephemeral.privateKey, bundle.x25519)
        val request = JSONObject()
            .put("v", E2EE.VERSION_V2)
            .put("suite", HostKey.SUITE)
            .put("epk", base64(ephemeral.publicKey))
            .put("kem", base64(encapsulation.ciphertext))
        val initTranscript = V2Protocol.pairInitTranscript(
            bundle.bundleHash,
            host.hostId,
            ephemeral.publicKey,
            encapsulation.ciphertext
        )
        val initKey = V2Protocol.derivePairInitKey(
            V2Protocol.combine(sharedDh, encapsulation.sharedKey),
            initTranscript
        )
        request.put(
            "sealedCode",
            base64(Envelope.seal(initKey, code.toByteArray(StandardCharsets.UTF_8), initTranscript))
        )
        val response = postJson("$base/api/v1/pair", request.toString())

        val deviceId = response.optString("deviceId", "")
        val hostId = response.optString("hostId", "")
        val sealed = response.optString("sealedPayload", "")
        if (sealed.isEmpty() || deviceId.isEmpty()) {
            throw IllegalStateException("The daemon did not seal the pairing payload; update it and pair again.")
        }
        if (hostId.isNotEmpty() && hostId != host.hostId) {
            throw HostIdentityChangedException("The pairing response named a different host.")
        }
        if (response.optString("token", "").isNotEmpty() ||
            response.optString("sealedToken", "").isNotEmpty()
        ) {
            throw IllegalStateException("The daemon answered a hybrid pair with a legacy token. Update it.")
        }

        val inputKeyMaterial = V2Protocol.combine(sharedDh, encapsulation.sharedKey)
        val transcript = V2Protocol.pairTranscript(
            bundle.bundleHash,
            host.hostId,
            deviceId,
            ephemeral.publicKey,
            encapsulation.ciphertext
        )
        val keys = V2Protocol.deriveKeys(inputKeyMaterial, transcript)
        val plain = Envelope.open(
            keys[1],
            Base64.decode(sealed, Base64.NO_WRAP),
            transcript
        )
        val payload = try {
            JSONObject(String(plain, StandardCharsets.UTF_8))
        } catch (error: Exception) {
            throw IllegalStateException("The sealed pairing payload is not readable.", error)
        }
        val token = payload.optString("token", "")
        if (token.isEmpty()) {
            throw IllegalStateException("The daemon returned no device token.")
        }
        // Everything the client will trust is repeated inside the AEAD, so a
        // rewritten response cannot relabel this session as another host or suite.
        if (payload.optString("deviceId", "") != deviceId ||
            payload.optString("hostId", "") != host.hostId ||
            payload.optString("suite", "") != bundle.suite
        ) {
            throw HostIdentityChangedException("The sealed pairing payload did not match the host.")
        }

        return TokenStore.Connection(
            base = base,
            hostId = host.hostId,
            token = token,
            deviceId = deviceId,
            hostPublicKey = bundle.publicKeyB64,
            fingerprint = bundle.fingerprintV2,
            suite = bundle.suite,
            hostBundle = host.bundleJson,
            fingerprintV2 = bundle.fingerprintV2,
            label = hostLabel(base),
            scope = response.optString("scope", "")
        )
    }

    private fun fetchHostKey(base: String, allowLegacy: Boolean = false): HostKeyResponse {
        val response = getJson("$base/api/v1/e2ee/host-key")
        val hostId = response.optString("hostId", "")
        val publicKey = response.optString("publicKey", "")
        if (hostId.isEmpty() || publicKey.isEmpty()) {
            throw IllegalStateException("The daemon returned no host identity.")
        }
        val maxVersion = response.optInt("maxVersion", E2EE.VERSION_V1)
        val advertisedSuite = response.optString("suite", "")
        if (maxVersion < E2EE.VERSION_V2 || advertisedSuite != HostKey.SUITE) {
            if (!allowLegacy || maxVersion >= E2EE.VERSION_V2 || advertisedSuite == HostKey.SUITE) {
                throw IllegalStateException(
                    "This host does not advertise the required hybrid suite. Update the daemon before pairing."
                )
            }
            val fingerprint = HostFingerprint.verified(publicKey, response.optString("fingerprint", ""))
            return HostKeyResponse(hostId, publicKey, fingerprint, null, "")
        }
        // Either maxVersion or suite names v2. Requiring the bundle in both
        // cases is what stops a rewritten maxVersion from reading as legacy.
        val bundleObject = response.optJSONObject("bundle")
            ?: throw IllegalStateException(
                "This host claims the hybrid suite without publishing its signed key bundle."
            )
        val bundle = HostBundle.parse(bundleObject)
        val expectedPin = HostKey.fingerprintV2(publicKey, bundle.suite)
        if (expectedPin != bundle.fingerprintV2) {
            throw HostIdentityChangedException(
                "The host key bundle does not match the advertised identity."
            )
        }
        val advertised = response.optString("fingerprintV2", "")
        if (advertised.isNotEmpty() && advertised != expectedPin) {
            throw HostIdentityChangedException("The host advertised an inconsistent fingerprint.")
        }
        return HostKeyResponse(
            hostId,
            publicKey,
            HostFingerprint.verified(publicKey, response.optString("fingerprint", "")),
            bundle,
            bundleObject.toString()
        )
    }

    private fun getJson(url: String): JSONObject {
        val connection = openConnection(url)
        connection.connectTimeout = 10_000
        connection.readTimeout = 10_000
        connection.requestMethod = "GET"
        connection.instanceFollowRedirects = false
        connection.useCaches = false
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("User-Agent", "DarkFactory-Android/0.1")
        try {
            val status = connection.responseCode
            val stream = if (status >= 400) connection.errorStream else connection.inputStream
            val text = readAll(stream)
            if (status !in 200..299) throw IllegalStateException(refusal(text, status))
            return try {
                JSONObject(text)
            } catch (error: Exception) {
                throw IllegalStateException("The host did not answer a valid JSON document.", error)
            }
        } catch (error: IllegalStateException) {
            throw error
        } catch (error: IOException) {
            throw IllegalStateException("The host is unreachable from this phone.", error)
        } catch (error: Exception) {
            throw IllegalStateException("The host did not answer a valid response.", error)
        } finally {
            connection.disconnect()
        }
    }

    private fun postJson(url: String, body: String): JSONObject {
        val connection = openConnection(url)
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
        val encoded = body.toByteArray(StandardCharsets.UTF_8)
        connection.setFixedLengthStreamingMode(encoded.size)
        try {
            connection.outputStream.use { output -> output.write(encoded) }
            val status = connection.responseCode
            val stream = if (status >= 400) connection.errorStream else connection.inputStream
            val text = readAll(stream)
            if (status !in 200..299) throw IllegalStateException(refusal(text, status))
            return try {
                JSONObject(text)
            } catch (error: Exception) {
                throw IllegalStateException("The host did not answer a valid JSON document.", error)
            }
        } catch (error: IllegalStateException) {
            throw error
        } catch (error: IOException) {
            throw IllegalStateException("The host is unreachable from this phone.", error)
        } catch (error: Exception) {
            throw IllegalStateException("The host did not answer a valid response.", error)
        } finally {
            connection.disconnect()
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

    private fun sameKey(left: String, right: String): Boolean {
        return try {
            MessageDigest.isEqual(
                Base64.decode(left, Base64.NO_WRAP),
                Base64.decode(right, Base64.NO_WRAP)
            )
        } catch (_: Exception) {
            false
        }
    }

    private fun base64(value: ByteArray): String = Base64.encodeToString(value, Base64.NO_WRAP)

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
            val code = body.optString("code", "")
            if (code == "e2ee_suite_downgrade") {
                return "This host requires hybrid encryption. Update DarkFactory, forget the host, and pair again."
            }
            val detail = body.optString("detail", "")
            if (detail.isNotEmpty()) return detail
            val hint = body.optString("hint", "")
            if (hint.isNotEmpty()) return hint
            if (code.isNotEmpty()) return code
        } catch (_: Exception) {
            // Fall through to the status text.
        }
        return "Pairing failed (HTTP $status)."
    }
}
