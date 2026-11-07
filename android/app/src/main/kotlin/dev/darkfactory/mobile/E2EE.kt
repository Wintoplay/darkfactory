package dev.darkfactory.mobile

import android.util.Base64
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
import javax.crypto.Cipher
import javax.crypto.KeyAgreement
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 * Application-layer encryption for the native proxy. The daemon and the app
 * derive directional AES-GCM keys from P-256 ECDH; the host public key is
 * pinned after pairing. A Tailscale peer that can rewrite TLS still sees only
 * the envelope route, opaque frames, and no bearer token.
 */
internal class E2EE(
    private val clientPrivate: PrivateKey,
    private val hostPublic: PublicKey,
    val clientPublicKeyB64: String,
    val hostPublicKeyB64: String,
    val deviceId: String,
    val token: String
) {
    private val clientToServer: ByteArray
    private val serverToClient: ByteArray

    init {
        val pair = derive(clientPrivate, hostPublic, hostPublicKeyB64, clientPublicKeyB64)
        clientToServer = pair.first
        serverToClient = pair.second
    }

    fun sealRequest(
        method: String,
        path: String,
        headers: Map<String, String>,
        body: ByteArray
    ): Pair<String, String> {
        val requestId = randomHex(16)
        val timestamp = System.currentTimeMillis()
        val headerObject = JSONObject()
        for ((name, value) in headers) headerObject.put(name, value)
        val plain = JSONObject()
            .put("method", method)
            .put("path", path)
            .put("headers", headerObject)
            .put("body", Base64.encodeToString(body, Base64.NO_WRAP))
            .put("token", token)
            .toString()
            .toByteArray(StandardCharsets.UTF_8)
        val aad = "req|$deviceId|$requestId|$timestamp".toByteArray(StandardCharsets.UTF_8)
        val sealed = seal(clientToServer, plain, aad)
        val envelope = JSONObject()
            .put("v", VERSION)
            .put("deviceId", deviceId)
            .put("id", requestId)
            .put("ts", timestamp)
            .put("nonce", Base64.encodeToString(sealed.first, Base64.NO_WRAP))
            .put("ciphertext", Base64.encodeToString(sealed.second, Base64.NO_WRAP))
            .toString()
        return Pair(envelope, requestId)
    }

    fun openFrame(frame: ByteArray, requestId: String, index: Int): ByteArray {
        if (frame.size < NONCE_BYTES + 16) throw IllegalStateException("encrypted frame is too short")
        val nonce = frame.copyOfRange(0, NONCE_BYTES)
        val sealed = frame.copyOfRange(NONCE_BYTES, frame.size)
        val aad = "resp|$deviceId|$requestId|$index".toByteArray(StandardCharsets.UTF_8)
        return open(serverToClient, nonce, sealed, aad)
    }

    companion object {
        private const val VERSION = 1
        private const val NONCE_BYTES = 12
        private const val TAG_BITS = 128
        private const val KEY_INFO = "darkfactory-e2ee-v1"
        private val RANDOM = SecureRandom()

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
        ): Pair<ByteArray, ByteArray> {
            val agreement = KeyAgreement.getInstance("ECDH")
            agreement.init(clientPrivate)
            agreement.doPhase(hostPublic, true)
            val shared = agreement.generateSecret()
            val salt = sha256("$hostPublicB64|$clientPublicB64".toByteArray(StandardCharsets.UTF_8))
            val derived = hkdf(shared, salt, KEY_INFO.toByteArray(StandardCharsets.UTF_8), 64)
            return Pair(derived.copyOfRange(0, 32), derived.copyOfRange(32, 64))
        }

        fun openPairToken(sealedB64: String, key: ByteArray, aad: String): String {
            val raw = Base64.decode(sealedB64, Base64.NO_WRAP)
            if (raw.size < NONCE_BYTES + 16) throw IllegalStateException("sealed token is too short")
            val nonce = raw.copyOfRange(0, NONCE_BYTES)
            val sealed = raw.copyOfRange(NONCE_BYTES, raw.size)
            return String(open(key, nonce, sealed, aad.toByteArray(StandardCharsets.UTF_8)), StandardCharsets.UTF_8)
        }

        private fun seal(key: ByteArray, plain: ByteArray, aad: ByteArray): Pair<ByteArray, ByteArray> {
            val nonce = ByteArray(NONCE_BYTES)
            RANDOM.nextBytes(nonce)
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(TAG_BITS, nonce))
            cipher.updateAAD(aad)
            return Pair(nonce, cipher.doFinal(plain))
        }

        private fun open(key: ByteArray, nonce: ByteArray, sealed: ByteArray, aad: ByteArray): ByteArray {
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(TAG_BITS, nonce))
            cipher.updateAAD(aad)
            return cipher.doFinal(sealed)
        }

        private fun sha256(raw: ByteArray): ByteArray {
            val digest = java.security.MessageDigest.getInstance("SHA-256")
            return digest.digest(raw)
        }

        private fun hkdf(secret: ByteArray, salt: ByteArray, info: ByteArray, length: Int): ByteArray {
            val prk = hmac(salt, secret)
            val output = ByteArray(length)
            var offset = 0
            var block = ByteArray(0)
            var counter = 1
            while (offset < length) {
                val mac = Mac.getInstance("HmacSHA256")
                mac.init(SecretKeySpec(prk, "HmacSHA256"))
                mac.update(block)
                mac.update(info)
                mac.update(counter.toByte())
                block = mac.doFinal()
                val take = minOf(block.size, length - offset)
                System.arraycopy(block, 0, output, offset, take)
                offset += take
                counter++
            }
            return output
        }

        private fun hmac(key: ByteArray, data: ByteArray): ByteArray {
            val mac = Mac.getInstance("HmacSHA256")
            mac.init(SecretKeySpec(key, "HmacSHA256"))
            return mac.doFinal(data)
        }

        private fun randomHex(bytes: Int): String {
            val raw = ByteArray(bytes)
            RANDOM.nextBytes(raw)
            return raw.joinToString("") { String.format(java.util.Locale.ROOT, "%02x", it.toInt() and 0xff) }
        }
    }
}
