package dev.darkfactory.mobile.pq;

import dev.darkfactory.mobile.HostKey;
import dev.darkfactory.mobile.HostPinPolicy;
import dev.darkfactory.mobile.EncryptedResponseReader;
import dev.darkfactory.mobile.Envelope;
import dev.darkfactory.mobile.V2Protocol;
import dev.darkfactory.mobile.crypto.Hkdf;
import dev.darkfactory.mobile.crypto.MlKem768;
import dev.darkfactory.mobile.crypto.X25519;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.security.SecureRandom;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Properties;
import org.bouncycastle.crypto.digests.SHA3Digest;
import org.bouncycastle.crypto.digests.SHAKEDigest;

/**
 * Runs the app's crypto and protocol classes on a JVM and checks them against
 * vectors produced by the daemon's Go implementation.
 *
 * <p>This is not an Android test. It runs the production wire classes on a JVM,
 * compares them with the Go derivation, and has Go decapsulate the ciphertext
 * this run produced. It also tests bundle pins, stored replacement policy,
 * authenticated stream completion, and bounded frame parsing.
 */
public final class PqCheck {
    public static void main(String[] args) throws Exception {
        if (args.length != 1) {
            throw new IllegalArgumentException("usage: PqCheck WORKDIR");
        }
        Path dir = Paths.get(args[0]);
        Map<String, String> input = load(dir.resolve("input.properties"));
        Map<String, String> vectors = load(dir.resolve("vectors.properties"));

        checkHkdf();
        checkHashes(vectors);
        byte[] alicePrivate = hex(vectors.get("alicePrivate"));
        byte[] bobPrivate = hex(vectors.get("bobPrivate"));
        checkX25519(vectors, alicePrivate, bobPrivate);
        HostKey host = checkHostKey(input);
        checkMlKemRejectsUnreducedKey(input);

        byte[] ssKEM;
        byte[] kemCiphertext;
        MlKem768.Encapsulation encapsulation = MlKem768.encapsulate(host.mlkem);
        ssKEM = encapsulation.sharedKey;
        kemCiphertext = encapsulation.ciphertext;

        X25519.KeyPair ephemeral = X25519.generateKeyPair(new SecureRandom());
        byte[] ssDH = X25519.scalarMult(ephemeral.privateKey, host.x25519);

        byte[] ikm = V2Protocol.combine(ssDH, ssKEM);
        String deviceId = input.get("deviceId");
        String requestId = input.get("requestId");
        long timestamp = Long.parseLong(input.get("timestamp"));

        byte[] requestTranscript = V2Protocol.requestTranscript(
            host.bundleHash, deviceId, requestId, timestamp, ephemeral.publicKey, kemCiphertext);
        byte[][] keys = V2Protocol.deriveKeys(ikm, requestTranscript);
        checkBundleAndPin(host);
        checkPinnedReplacementPolicy();
        checkResponseFrameReader(keys[1], deviceId, requestId);
        byte[] pairTranscript = V2Protocol.pairTranscript(
            host.bundleHash, input.get("hostId"), deviceId, ephemeral.publicKey, kemCiphertext);
        byte[][] pairKeys = V2Protocol.deriveKeys(ikm, pairTranscript);
        byte[] pairInitTranscript = V2Protocol.pairInitTranscript(
            host.bundleHash, input.get("hostId"), ephemeral.publicKey, kemCiphertext);
        byte[] pairInitKey = V2Protocol.derivePairInitKey(ikm, pairInitTranscript);

        Map<String, String> out = new LinkedHashMap<>();
        out.put("epkPub", b64(ephemeral.publicKey));
        out.put("kemCt", b64(kemCiphertext));
        out.put("ts", Long.toString(timestamp));
        out.put("ssDH", hexOf(ssDH));
        out.put("ssKEM", hexOf(ssKEM));
        // Extra ciphertexts exercise compression and rounding at different
        // coefficients; check-pq.sh has Go decapsulate every one.
        for (int i = 2; i <= 8; i++) {
            MlKem768.Encapsulation extra = MlKem768.encapsulate(host.mlkem);
            out.put("kemCt" + i, b64(extra.ciphertext));
            out.put("ssKEM" + i, hexOf(extra.sharedKey));
        }
        out.put("bundleHash", hexOf(host.bundleHash));
        out.put("requestTranscript", hexOf(requestTranscript));
        out.put("pairTranscript", hexOf(pairTranscript));
        out.put("c2s", hexOf(keys[0]));
        out.put("s2c", hexOf(keys[1]));
        out.put("pairS2c", hexOf(pairKeys[1]));
        out.put("pairInitTranscript", hexOf(pairInitTranscript));
        out.put("pairInitKey", hexOf(pairInitKey));
        out.put("requestAad", V2Protocol.requestAad(deviceId, requestId, timestamp));
        out.put("responseAad", V2Protocol.responseAad(deviceId, requestId));
        out.put("fingerprintV2", host.fingerprintV2);
        store(dir.resolve("proto.properties"), out);
        System.out.println("pqcheck: java v2 schedule computed");
        measure(host);
    }

    /** Reports the per-request cost; the numbers go into the review document. */
    private static void measure(HostKey host) {
        int rounds = 20;
        for (int i = 0; i < 3; i++) {
            MlKem768.encapsulate(host.mlkem);
            X25519.publicKey(new byte[32]);
        }
        long start = System.nanoTime();
        for (int i = 0; i < rounds; i++) {
            MlKem768.encapsulate(host.mlkem);
        }
        long kemNanos = (System.nanoTime() - start) / rounds;
        byte[] bobPrivate = new byte[32];
        new SecureRandom().nextBytes(bobPrivate);
        byte[] bobPublic = X25519.publicKey(bobPrivate);
        for (int i = 0; i < 3; i++) {
            X25519.scalarMult(bobPrivate, bobPublic);
        }
        start = System.nanoTime();
        for (int i = 0; i < rounds; i++) {
            X25519.scalarMult(bobPrivate, bobPublic);
        }
        long x25519Nanos = (System.nanoTime() - start) / rounds;
        System.out.printf(
            java.util.Locale.ROOT,
            "pqcheck: mlkem-768 encapsulate %.2f ms, x25519 %.2f ms%n",
            kemNanos / 1e6,
            x25519Nanos / 1e6
        );
    }

    private static void checkHashes(Map<String, String> vectors) {
        for (int i = 0; i < 3; i++) {
            byte[] message = hex(vectors.get("input_" + i));
            require(hexOf(digest(new SHA3Digest(256), message, 32)).equals(vectors.get("sha3_256_" + i)),
                "SHA3-256 vector " + i);
            require(hexOf(digest(new SHA3Digest(512), message, 64)).equals(vectors.get("sha3_512_" + i)),
                "SHA3-512 vector " + i);
            require(hexOf(shake(128, message, 32)).equals(vectors.get("shake128_" + i)),
                "SHAKE128 vector " + i);
            require(hexOf(shake(256, message, 64)).equals(vectors.get("shake256_" + i)),
                "SHAKE256 vector " + i);
        }
    }

    private static void checkHkdf() {
        byte[] ikm = new byte[22];
        java.util.Arrays.fill(ikm, (byte) 0x0b);
        byte[] salt = hex("000102030405060708090a0b0c");
        byte[] info = hex("f0f1f2f3f4f5f6f7f8f9");
        require(hexOf(Hkdf.sha256(ikm, salt, info, 42)).equals(
            "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865"),
            "RFC 5869 HKDF-SHA256 test case 1");
    }

    private static byte[] digest(SHA3Digest digest, byte[] message, int length) {
        digest.update(message, 0, message.length);
        byte[] output = new byte[length];
        digest.doFinal(output, 0);
        return output;
    }

    private static byte[] shake(int strength, byte[] message, int length) {
        SHAKEDigest digest = new SHAKEDigest(strength);
        digest.update(message, 0, message.length);
        byte[] output = new byte[length];
        digest.doFinal(output, 0, length);
        return output;
    }

    private static void checkX25519(Map<String, String> vectors, byte[] alicePrivate, byte[] bobPrivate) {
        byte[] alicePublic = X25519.publicKey(alicePrivate);
        byte[] bobPublic = X25519.publicKey(bobPrivate);
        require(hexOf(alicePublic).equals(vectors.get("alicePublic")), "X25519 alice public key");
        require(hexOf(bobPublic).equals(vectors.get("bobPublic")), "X25519 bob public key");
        byte[] shared = X25519.scalarMult(alicePrivate, bobPublic);
        require(hexOf(shared).equals(vectors.get("shared")), "X25519 shared secret");
        require(hexOf(X25519.scalarMult(bobPrivate, alicePublic)).equals(vectors.get("shared")),
            "X25519 shared secret (other direction)");
    }

    private static HostKey checkHostKey(Map<String, String> input) {
        HostKey host = HostKey.verify(
            input.get("hostPublicKey"),
            Integer.parseInt(input.get("epoch")),
            input.get("suite"),
            input.get("hostX25519"),
            input.get("hostMlkem"),
            input.get("signature")
        );
        require(host.fingerprintV2.equals(input.get("fingerprintV2")), "bundle pin");
        require(hexOf(host.bundleHash).equals(input.get("bundleHash")), "bundle hash");

        tamper(input, "signature", "tampered signature");
        tamper(input, "suite", "tampered suite");
        tamper(input, "hostX25519", "tampered x25519 key");
        tamper(input, "hostMlkem", "tampered mlkem key");
        return host;
    }

    private static void tamper(Map<String, String> input, String field, String what) {
        Map<String, String> copy = new LinkedHashMap<>(input);
        String value = copy.get(field);
        char head = value.charAt(0) == 'A' ? 'B' : 'A';
        copy.put(field, head + value.substring(1));
        try {
            HostKey.verify(
                copy.get("hostPublicKey"),
                Integer.parseInt(copy.get("epoch")),
                copy.get("suite"),
                copy.get("hostX25519"),
                copy.get("hostMlkem"),
                copy.get("signature")
            );
        } catch (IllegalStateException expected) {
            return;
        }
        throw new AssertionError("accepted a " + what);
    }

    private static void checkMlKemRejectsUnreducedKey(Map<String, String> input) {
        byte[] corrupt = java.util.Base64.getDecoder().decode(input.get("hostMlkem"));
        corrupt[0] = (byte) 0xff;
        corrupt[1] = (byte) 0xff;
        try {
            MlKem768.encapsulate(corrupt);
        } catch (IllegalArgumentException expected) {
            return;
        }
        throw new AssertionError("accepted an ML-KEM key with an unreduced coefficient");
    }

    private static void checkBundleAndPin(HostKey host) {
        expectFailure(() -> HostKey.requireBundleVersion(1), "bundle version downgrade");
        expectFailure(() -> HostKey.requireBundleVersion(3), "unknown bundle version");
        expectFailure(() -> host.requireStoredPin(host.publicKeyB64, "legacy", host.fingerprintV2),
            "stored suite mismatch");
        expectFailure(() -> host.requireStoredPin("changed-key", host.suite, host.fingerprintV2),
            "stored key mismatch");
        expectFailure(() -> host.requireStoredPin(host.publicKeyB64, host.suite, "changed-pin"),
            "stored fingerprint mismatch");
        host.requireStoredPin(host.publicKeyB64, host.suite, host.fingerprintV2);
    }

    private static void checkPinnedReplacementPolicy() {
        String key = "same-key";
        String legacy = "legacy-fingerprint";
        String hybrid = "hybrid-fingerprint";
        HostPinPolicy.requireHostId("host-id", "host-id");
        expectFailure(() -> HostPinPolicy.requireHostId("host-id", "changed-id"),
            "host id relabeling");
        HostPinPolicy.requireReplacement(key, "", legacy, "", key, "", legacy, "");
        HostPinPolicy.requireReplacement(key, "", legacy, "", key, HostKey.SUITE, hybrid, hybrid);
        HostPinPolicy.requireReplacement(key, HostKey.SUITE, hybrid, hybrid,
            key, HostKey.SUITE, hybrid, hybrid);
        expectFailure(() -> HostPinPolicy.requireReplacement(key, HostKey.SUITE, hybrid, hybrid,
            key, "", legacy, ""), "hybrid suite downgrade");
        expectFailure(() -> HostPinPolicy.requireReplacement(key, "", legacy, "",
            "changed-key", "", "changed-fingerprint", ""), "host identity replacement");
        expectFailure(() -> HostPinPolicy.requireReplacement(key, "", legacy, "",
            key, "unknown-suite", "fingerprint", ""), "unknown suite replacement");
        expectFailure(() -> HostPinPolicy.requireReplacement("", "", "", "",
            key, "", legacy, ""), "replacement of unusable pin");
        byte[] currentBundle = new byte[32];
        byte[] nextBundle = new byte[32];
        currentBundle[0] = 1;
        nextBundle[0] = 2;
        HostPinPolicy.requireBundleAdvance(7, currentBundle, 7, currentBundle.clone());
        HostPinPolicy.requireBundleAdvance(7, currentBundle, 8, nextBundle);
        expectFailure(() -> HostPinPolicy.requireBundleAdvance(8, nextBundle, 7, currentBundle),
            "lower bundle epoch");
        expectFailure(() -> HostPinPolicy.requireBundleAdvance(7, currentBundle, 7, nextBundle),
            "changed bundle at same epoch");
    }

    private static void checkResponseFrameReader(byte[] key, String deviceId, String requestId) throws Exception {
        String aad = V2Protocol.responseAad(deviceId, requestId);
        byte[] head = "{\"status\":200}".getBytes(StandardCharsets.UTF_8);
        byte[] body = "state".getBytes(StandardCharsets.UTF_8);
        byte[] valid = framed(
            Envelope.seal(key, head, (aad + "|0").getBytes(StandardCharsets.UTF_8)),
            Envelope.seal(key, body, (aad + "|1").getBytes(StandardCharsets.UTF_8)),
            Envelope.seal(key, V2Protocol.RESPONSE_END_MARKER.getBytes(StandardCharsets.US_ASCII),
                V2Protocol.responseEndAad(deviceId, requestId, 2).getBytes(StandardCharsets.UTF_8))
        );
        java.util.List<String> observed = new java.util.ArrayList<>();
        EncryptedResponseReader.read(new java.io.ByteArrayInputStream(valid),
            responseOpener(key, aad, deviceId, requestId), capture(observed), 1024, 16, true);
        require(observed.equals(java.util.Arrays.asList("head:{\"status\":200}", "body:state")),
            "authenticated response stream contents");

        byte[] missingEnd = framed(
            Envelope.seal(key, head, (aad + "|0").getBytes(StandardCharsets.UTF_8)),
            Envelope.seal(key, body, (aad + "|1").getBytes(StandardCharsets.UTF_8))
        );
        expectReaderFailure(missingEnd, key, aad, deviceId, requestId, 1024, 16, true,
            "missing authenticated terminator");

        byte[] wrongMarker = Envelope.seal(key, "wrong".getBytes(StandardCharsets.UTF_8),
            V2Protocol.responseEndAad(deviceId, requestId, 1).getBytes(StandardCharsets.UTF_8));
        expectReaderFailure(framed(
            Envelope.seal(key, head, (aad + "|0").getBytes(StandardCharsets.UTF_8)), wrongMarker
        ), key, aad, deviceId, requestId, 1024, 16, true, "wrong authenticated terminator");

        byte[] trailing = concat(valid, frame(Envelope.seal(key, new byte[0],
            (aad + "|3").getBytes(StandardCharsets.UTF_8))));
        expectReaderFailure(trailing, key, aad, deviceId, requestId, 1024, 16, true,
            "frame after terminator");

        byte[] oversizedBody = framed(
            Envelope.seal(key, head, (aad + "|0").getBytes(StandardCharsets.UTF_8)),
            Envelope.seal(key, new byte[17], (aad + "|1").getBytes(StandardCharsets.UTF_8))
        );
        expectReaderFailure(oversizedBody, key, aad, deviceId, requestId, 1024, 16, true,
            "aggregate response body limit");
        expectBadLength(new byte[] {(byte) 0xff, (byte) 0xff, (byte) 0xff, (byte) 0xff},
            key, aad, deviceId, requestId, false);
        expectBadLength(new byte[] {0x7f, (byte) 0xff, (byte) 0xff, (byte) 0xff},
            key, aad, deviceId, requestId, true);
    }

    private static EncryptedResponseReader.Opener responseOpener(
        byte[] key, String aad, String deviceId, String requestId
    ) {
        return new EncryptedResponseReader.Opener() {
            @Override public byte[] openData(byte[] frame, int index) {
                return Envelope.open(key, frame, (aad + "|" + index).getBytes(StandardCharsets.UTF_8));
            }
            @Override public byte[] openEnd(byte[] frame, int index) {
                try {
                    return Envelope.open(key, frame, V2Protocol.responseEndAad(deviceId, requestId, index)
                        .getBytes(StandardCharsets.UTF_8));
                } catch (IllegalStateException unauthenticatedAsTerminator) {
                    return null;
                }
            }
        };
    }

    private static EncryptedResponseReader.Listener capture(java.util.List<String> observed) {
        return new EncryptedResponseReader.Listener() {
            @Override public void onHead(byte[] plain) {
                observed.add("head:" + new String(plain, StandardCharsets.UTF_8));
            }
            @Override public void onBody(byte[] plain) {
                observed.add("body:" + new String(plain, StandardCharsets.UTF_8));
            }
        };
    }

    private static void expectReaderFailure(byte[] bytes, byte[] key, String aad, String deviceId,
        String requestId, int maxFrame, long maxBody, boolean requireEnd, String what) throws Exception {
        try {
            EncryptedResponseReader.read(new java.io.ByteArrayInputStream(bytes),
                responseOpener(key, aad, deviceId, requestId), capture(new java.util.ArrayList<>()),
                maxFrame, maxBody, requireEnd);
        } catch (Exception expected) {
            return;
        }
        throw new AssertionError("accepted " + what);
    }

    private static void expectBadLength(byte[] prefix, byte[] key, String aad, String deviceId,
        String requestId, boolean requireEnd) throws Exception {
        byte[] head = frame(Envelope.seal(key, "{}".getBytes(StandardCharsets.UTF_8),
            (aad + "|0").getBytes(StandardCharsets.UTF_8)));
        expectReaderFailure(concat(head, prefix), key, aad, deviceId, requestId, 1024, 16,
            requireEnd, "bad unsigned encrypted frame length");
    }

    private static byte[] framed(byte[]... frames) {
        byte[] result = new byte[0];
        for (byte[] value : frames) result = concat(result, frame(value));
        return result;
    }

    private static byte[] frame(byte[] value) {
        byte[] result = new byte[value.length + 4];
        result[0] = (byte) (value.length >>> 24);
        result[1] = (byte) (value.length >>> 16);
        result[2] = (byte) (value.length >>> 8);
        result[3] = (byte) value.length;
        System.arraycopy(value, 0, result, 4, value.length);
        return result;
    }

    private static byte[] concat(byte[] first, byte[] second) {
        byte[] result = new byte[first.length + second.length];
        System.arraycopy(first, 0, result, 0, first.length);
        System.arraycopy(second, 0, result, first.length, second.length);
        return result;
    }

    private static void expectFailure(Runnable action, String what) {
        try {
            action.run();
        } catch (RuntimeException expected) {
            return;
        }
        throw new AssertionError("accepted " + what);
    }

    private static void require(boolean condition, String what) {
        if (!condition) {
            throw new AssertionError(what + " did not match the Go vector");
        }
    }

    private static Map<String, String> load(Path path) throws IOException {
        Properties properties = new Properties();
        try (InputStream input = Files.newInputStream(path)) {
            properties.load(input);
        }
        Map<String, String> values = new LinkedHashMap<>();
        for (String name : properties.stringPropertyNames()) {
            values.put(name, properties.getProperty(name));
        }
        return values;
    }

    private static void store(Path path, Map<String, String> values) throws IOException {
        StringBuilder builder = new StringBuilder();
        for (Map.Entry<String, String> entry : values.entrySet()) {
            builder.append(entry.getKey()).append('=').append(entry.getValue()).append('\n');
        }
        try (OutputStream output = Files.newOutputStream(path)) {
            output.write(builder.toString().getBytes(StandardCharsets.UTF_8));
        }
    }

    private static byte[] hex(String value) {
        int length = value.length() / 2;
        byte[] out = new byte[length];
        for (int i = 0; i < length; i++) {
            out[i] = (byte) Integer.parseInt(value.substring(2 * i, 2 * i + 2), 16);
        }
        return out;
    }

    private static String hexOf(byte[] value) {
        StringBuilder builder = new StringBuilder(value.length * 2);
        for (byte b : value) {
            builder.append(String.format(java.util.Locale.ROOT, "%02x", b & 0xff));
        }
        return builder.toString();
    }

    private static String b64(byte[] value) {
        return java.util.Base64.getEncoder().encodeToString(value);
    }

    private PqCheck() {}
}
