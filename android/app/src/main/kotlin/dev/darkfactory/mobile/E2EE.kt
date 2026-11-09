package dev.darkfactory.mobile

import android.util.Base64
import dev.darkfactory.mobile.crypto.Hkdf
import dev.darkfactory.mobile.crypto.MlKem768
import dev.darkfactory.mobile.crypto.X25519
import org.json.JSONObject
import java.nio.charset.StandardCharsets
import java.security.KeyFactory
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.PrivateKey
import java.security.PublicKey
import java.security.SecureRandom
import java.security.spec.ECGenParameterSpec
import java.security.spec.PKCS8EncodedKeySpec
import java.security.spec.X509EncodedKeySpec
import javax.crypto.KeyAgreement

/**
 * Application-layer encryption for the native proxy.
 *
 * <p>Two suites exist. The legacy v1 derives directional AES-GCM keys from a
 * per-pair P-256 client key and the host's P-256 key. The v2 suite
 * (`x25519-mlkem768-v1`) sends a fresh X25519 ephemeral key and an ML-KEM-768
 * ciphertext with every request and derives the keys from both shared secrets,
 * so breaking the envelope needs a classical <em>and</em> a quantum attack. The
 * suite is a property of the stored host row; it is never negotiated per
 * message, and a v2 row refuses to fall back.
 */
internal sealed class E2EE {
    abstract val deviceId: String

    /**
     * One sealed request. The daemon derives response keys from the request's
     * own key material, so the opener travels with the request instead of being
     * cached on the cipher.
     */
    class SealedRequest internal constructor(
        val envelope: String,
        private val opener: (ByteArray, Int) -> ByteArray,
        private val endOpener: ((ByteArray, Int) -> ByteArray?)? = null
    ) {
        fun openFrame(frame: ByteArray, index: Int): ByteArray = opener(frame, index)
        fun openEndFrame(frame: ByteArray, index: Int): ByteArray? = endOpener?.invoke(frame, index)
        val hasAuthenticatedEndMarker: Boolean get() = endOpener != null
    }

    abstract fun sealRequest(
        method: String,
        path: String,
        headers: Map<String, String>,
        body: ByteArray
    ): SealedRequest

    /** The legacy P-256 suite, paired before v2 existed. */
    class V1(
        clientPrivate: PrivateKey,
        hostPublic: PublicKey,
        clientPublicKeyB64: String,
        hostPublicKeyB64: String,
        override val deviceId: String,
        private val token: String
    ) : E2EE() {
        private val clientToServer: ByteArray
        private val serverToClient: ByteArray

        init {
            val keys = derive(clientPrivate, hostPublic, hostPublicKeyB64, clientPublicKeyB64)
            clientToServer = keys[0]
            serverToClient = keys[1]
        }

        override fun sealRequest(
            method: String,
            path: String,
            headers: Map<String, String>,
            body: ByteArray
        ): SealedRequest {
            val requestId = randomHex(16)
            val timestamp = System.currentTimeMillis()
            val plain = requestJson(method, path, headers, body, token)
                .toByteArray(StandardCharsets.UTF_8)
            val aad = "req|$deviceId|$requestId|$timestamp".toByteArray(StandardCharsets.UTF_8)
            val parts = Envelope.sealParts(clientToServer, plain, aad)
            val envelope = JSONObject()
                .put("v", VERSION_V1)
                .put("deviceId", deviceId)
                .put("id", requestId)
                .put("ts", timestamp)
                .put("nonce", base64(parts.nonce))
                .put("ciphertext", base64(parts.ciphertext))
                .toString()
            return SealedRequest(envelope, { frame, index ->
                Envelope.open(
                    serverToClient,
                    frame,
                    "resp|$deviceId|$requestId|$index".toByteArray(StandardCharsets.UTF_8)
                )
            })
        }
    }

    /** The hybrid X25519 + ML-KEM-768 suite. */
    class V2(
        private val hostKey: HostKey,
        override val deviceId: String,
        private val token: String
    ) : E2EE() {
        override fun sealRequest(
            method: String,
            path: String,
            headers: Map<String, String>,
            body: ByteArray
        ): SealedRequest {
            val requestId = randomHex(16)
            val timestamp = System.currentTimeMillis()

            val ephemeral = X25519.generateKeyPair(RANDOM)
            val encapsulation = MlKem768.encapsulate(hostKey.mlkem)
            val sharedDh = X25519.scalarMult(ephemeral.privateKey, hostKey.x25519)
            val inputKeyMaterial = V2Protocol.combine(sharedDh, encapsulation.sharedKey)
            val transcript = V2Protocol.requestTranscript(
                hostKey.bundleHash,
                deviceId,
                requestId,
                timestamp,
                ephemeral.publicKey,
                encapsulation.ciphertext
            )
            val keys = V2Protocol.deriveKeys(inputKeyMaterial, transcript)

            val plain = requestJson(method, path, headers, body, token)
                .toByteArray(StandardCharsets.UTF_8)
            val aad = V2Protocol.requestAad(deviceId, requestId, timestamp)
                .toByteArray(StandardCharsets.UTF_8)
            val parts = Envelope.sealParts(keys[0], plain, aad)
            val envelope = JSONObject()
                .put("v", VERSION_V2)
                .put("deviceId", deviceId)
                .put("id", requestId)
                .put("ts", timestamp)
                .put("suite", HostKey.SUITE)
                .put("epk", base64(ephemeral.publicKey))
                .put("kem", base64(encapsulation.ciphertext))
                .put("nonce", base64(parts.nonce))
                .put("ciphertext", base64(parts.ciphertext))
                .toString()
            val responseAad = V2Protocol.responseAad(deviceId, requestId)
            return SealedRequest(
                envelope,
                { frame, index ->
                    Envelope.open(
                        keys[1],
                        frame,
                        "$responseAad|$index".toByteArray(StandardCharsets.UTF_8)
                    )
                },
                { frame, index ->
                    try {
                        Envelope.open(
                            keys[1],
                            frame,
                            V2Protocol.responseEndAad(deviceId, requestId, index)
                                .toByteArray(StandardCharsets.UTF_8)
                        )
                    } catch (_: IllegalStateException) {
                        null
                    }
                }
            )
        }
    }

    internal companion object {
        const val VERSION_V1 = 1
        const val VERSION_V2 = 2
        private const val KEY_INFO = "darkfactory-e2ee-v1"
        private val RANDOM = SecureRandom()

        /** Builds the cipher for a stored row, or fails closed. */
        fun from(connection: TokenStore.Connection): E2EE {
            when (connection.suite) {
                HostKey.SUITE -> {
                    val host = HostBundle.stored(connection.hostBundle)
                    host.requireStoredPin(
                        connection.hostPublicKey,
                        connection.suite,
                        connection.fingerprintV2
                    )
                    if (connection.fingerprint != connection.fingerprintV2) {
                        throw IllegalStateException("The stored fingerprint does not match the hybrid suite pin.")
                    }
                    return V2(host, connection.deviceId, connection.token)
                }
                "" -> {
                    HostFingerprint.verified(connection.hostPublicKey, connection.fingerprint)
                    return V1(
                        privateKey(connection.clientPrivateKey),
                        publicKey(connection.hostPublicKey),
                        connection.clientPublicKey,
                        connection.hostPublicKey,
                        connection.deviceId,
                        connection.token
                    )
                }
                else -> throw IllegalStateException("The stored host suite is unsupported.")
            }
        }

        fun generateKeyPair(): KeyPair {
            val generator = KeyPairGenerator.getInstance("EC")
            generator.initialize(ECGenParameterSpec("secp256r1"))
            return generator.generateKeyPair()
        }

        fun publicKey(raw: String): PublicKey {
            val der = Base64.decode(raw, Base64.NO_WRAP)
            return KeyFactory.getInstance("EC").generatePublic(X509EncodedKeySpec(der))
        }

        fun privateKey(raw: String): PrivateKey {
            val der = Base64.decode(raw, Base64.NO_WRAP)
            return KeyFactory.getInstance("EC").generatePrivate(PKCS8EncodedKeySpec(der))
        }

        fun encode(key: java.security.Key): String =
            Base64.encodeToString(key.encoded, Base64.NO_WRAP)

        fun derive(
            clientPrivate: PrivateKey,
            hostPublic: PublicKey,
            hostPublicB64: String,
            clientPublicB64: String
        ): Array<ByteArray> {
            val agreement = KeyAgreement.getInstance("ECDH")
            agreement.init(clientPrivate)
            agreement.doPhase(hostPublic, true)
            val shared = agreement.generateSecret()
            val salt = Hkdf.sha256(
                "$hostPublicB64|$clientPublicB64".toByteArray(StandardCharsets.UTF_8)
            )
            val derived = Hkdf.sha256(
                shared,
                salt,
                KEY_INFO.toByteArray(StandardCharsets.UTF_8),
                64
            )
            return arrayOf(derived.copyOfRange(0, 32), derived.copyOfRange(32, 64))
        }

        fun openPairToken(sealedB64: String, key: ByteArray, aad: String): String {
            val raw = Base64.decode(sealedB64, Base64.NO_WRAP)
            val plain = Envelope.open(key, raw, aad.toByteArray(StandardCharsets.UTF_8))
            return String(plain, StandardCharsets.UTF_8)
        }

        fun requestJson(
            method: String,
            path: String,
            headers: Map<String, String>,
            body: ByteArray,
            token: String
        ): String {
            val headerObject = JSONObject()
            for ((name, value) in headers) headerObject.put(name, value)
            return JSONObject()
                .put("method", method)
                .put("path", path)
                .put("headers", headerObject)
                .put("body", base64(body))
                .put("token", token)
                .toString()
        }

        fun base64(value: ByteArray): String = Base64.encodeToString(value, Base64.NO_WRAP)

        fun randomHex(bytes: Int): String {
            val raw = ByteArray(bytes)
            RANDOM.nextBytes(raw)
            return raw.joinToString("") { String.format(java.util.Locale.ROOT, "%02x", it.toInt() and 0xff) }
        }
    }
}
