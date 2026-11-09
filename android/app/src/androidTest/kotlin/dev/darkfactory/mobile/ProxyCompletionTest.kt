package dev.darkfactory.mobile

import android.test.InstrumentationTestCase
import android.util.Base64
import dev.darkfactory.mobile.crypto.X25519
import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.security.KeyPairGenerator
import java.security.SecureRandom
import java.security.Signature
import java.security.spec.ECGenParameterSpec
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import org.bouncycastle.crypto.generators.MLKEMKeyPairGenerator
import org.bouncycastle.crypto.kems.MLKEMExtractor
import org.bouncycastle.crypto.params.MLKEMKeyGenerationParameters
import org.bouncycastle.crypto.params.MLKEMParameters
import org.bouncycastle.crypto.params.MLKEMPrivateKeyParameters
import org.bouncycastle.crypto.params.MLKEMPublicKeyParameters
import org.json.JSONObject

/** Verify that the actual local HTTP proxy does not turn truncated v2 into success. */
@Suppress("DEPRECATION")
class ProxyCompletionTest : InstrumentationTestCase() {
    fun testMissingAuthenticatedEndAbortsLocalChunkedResponse() {
        val response = proxyRoundTrip(false)
        assertTrue("Authenticated head must have reached the client", response.startsWith("HTTP/1.1 200"))
        assertTrue("Authenticated body must have reached the client", response.contains("{}"))
        assertFalse("Truncation must not produce a successful chunked EOF", response.endsWith("0\r\n\r\n"))
    }

    fun testAuthenticatedEndCompletesLocalChunkedResponse() {
        val response = proxyRoundTrip(true)
        assertTrue(response.startsWith("HTTP/1.1 200"))
        assertTrue(response.contains("{}"))
        assertTrue("A valid end marker must complete the response", response.endsWith("0\r\n\r\n"))
    }

    private fun proxyRoundTrip(includeEnd: Boolean): String {
        val random = SecureRandom()
        val hostDh = X25519.generateKeyPair(random)
        val generator = MLKEMKeyPairGenerator()
        generator.init(MLKEMKeyGenerationParameters(random, MLKEMParameters.ml_kem_768))
        val hostKem = generator.generateKeyPair()
        val kemPublic = (hostKem.public as MLKEMPublicKeyParameters).encoded
        val signer = KeyPairGenerator.getInstance("EC")
        signer.initialize(ECGenParameterSpec("secp256r1"))
        val identity = signer.generateKeyPair()
        val canonical = HostKey.canonical(identity.public.encoded, 1, HostKey.SUITE,
            hostDh.publicKey, kemPublic)
        val signature = Signature.getInstance("SHA256withECDSA")
        signature.initSign(identity.private)
        signature.update(canonical)
        val host = HostKey.verify(b64(identity.public.encoded), 1, HostKey.SUITE,
            b64(hostDh.publicKey), b64(kemPublic), b64(signature.sign()))
        val upstream = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))
        val executor = Executors.newSingleThreadExecutor()
        val future = executor.submit<Unit> {
            upstream.accept().use { socket ->
                socket.soTimeout = 10000
                val input = socket.getInputStream()
                val header = ByteArrayOutputStream()
                while (!header.toString("US-ASCII").endsWith("\r\n\r\n")) {
                    val byte = input.read()
                    check(byte >= 0 && header.size() < 16384)
                    header.write(byte)
                }
                val length = Regex("(?im)^Content-Length: ([0-9]+)").find(header.toString("US-ASCII"))!!
                    .groupValues[1].toInt()
                check(length in 1..(4 * 1024 * 1024))
                val raw = ByteArray(length)
                java.io.DataInputStream(input).readFully(raw)
                val request = JSONObject(String(raw, Charsets.UTF_8))
                val epk = decode(request.getString("epk"))
                val kem = decode(request.getString("kem"))
                val dh = X25519.scalarMult(hostDh.privateKey, epk)
                val secret = MLKEMExtractor(hostKem.private as MLKEMPrivateKeyParameters).extractSecret(kem)
                val id = request.getString("id")
                val device = request.getString("deviceId")
                val transcript = V2Protocol.requestTranscript(host.bundleHash, device, id,
                    request.getLong("ts"), epk, kem)
                val key = V2Protocol.deriveKeys(V2Protocol.combine(dh, secret), transcript)[1]
                val wire = ByteArrayOutputStream()
                val frames = DataOutputStream(wire)
                fun frame(plain: String, index: Int, end: Boolean = false) {
                    val aad = "resp|v2|$device|$id|$index" + if (end) "|end" else ""
                    val sealed = Envelope.seal(key, plain.toByteArray(), aad.toByteArray())
                    frames.writeInt(sealed.size)
                    frames.write(sealed)
                }
                frame("{\"status\":200,\"headers\":{\"Content-Type\":\"application/json\"}}", 0)
                frame("{}", 1)
                if (includeEnd) frame(V2Protocol.RESPONSE_END_MARKER, 2, true)
                val bytes = wire.toByteArray()
                socket.getOutputStream().apply {
                    write(("HTTP/1.1 200 OK\r\nContent-Type: application/x-df-e2ee; v=2\r\n" +
                        "Content-Length: ${bytes.size}\r\nConnection: close\r\n\r\n").toByteArray())
                    write(bytes)
                    flush()
                }
            }
        }
        val proxy = LoopbackServer(instrumentation.targetContext,
            "http://127.0.0.1:${upstream.localPort}", E2EE.V2(host, "fixture-device", "fixture-token"), null)
        try {
            proxy.start()
            val response = Socket("127.0.0.1", proxy.port).use { socket ->
                socket.soTimeout = 15000
                socket.getOutputStream().write(("GET /api/v1/state HTTP/1.1\r\n" +
                    "Host: ${proxy.originHost}:${proxy.port}\r\nConnection: close\r\n\r\n").toByteArray())
                socket.getInputStream().readBytes().toString(Charsets.UTF_8)
            }
            future.get(10, TimeUnit.SECONDS)
            return response
        } finally {
            proxy.stop()
            upstream.close()
            executor.shutdownNow()
        }
    }

    private fun b64(value: ByteArray): String = Base64.encodeToString(value, Base64.NO_WRAP)
    private fun decode(value: String): ByteArray = Base64.decode(value, Base64.NO_WRAP)
}
