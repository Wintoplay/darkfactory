package dev.darkfactory.mobile;

import dev.darkfactory.mobile.crypto.Hkdf;
import java.nio.charset.StandardCharsets;

/**
 * The transcript, AAD and key schedule of the v2 envelope.
 *
 * <p>Every value here is a byte-for-byte restatement of
 * {@code internal/httpapi/e2ee.go}: a domain string, 0x00, the signed bundle
 * hash, then length-prefixed fields. The bundle hash and the suite therefore sit
 * in the HKDF salt, so a rewritten suite or a stripped bundle cannot produce a
 * key that opens the daemon's frames.
 */
public final class V2Protocol {
    public static final String INFO = "darkfactory-e2ee-v2";
    public static final String RESPONSE_END_MARKER = "DFE2EE-END-v2";

    private static final String TRANSCRIPT_DOMAIN = "darkfactory-e2ee-transcript-v2";
    private static final String PAIR_DOMAIN = "darkfactory-e2ee-pair-v2";
    private static final String PAIR_INIT_DOMAIN = "darkfactory-e2ee-pair-init-v2";

    /** HKDF over both secrets concatenated; neither half is optional. */
    public static byte[] combine(byte[] x25519Secret, byte[] mlkemSecret) {
        if (x25519Secret.length != 32 || mlkemSecret.length != 32) {
            throw new IllegalArgumentException("each v2 secret half must be 32 bytes");
        }
        byte[] combined = new byte[64];
        System.arraycopy(x25519Secret, 0, combined, 0, 32);
        System.arraycopy(mlkemSecret, 0, combined, 32, 32);
        return combined;
    }

    /** Returns the client-to-server and server-to-client keys, in that order. */
    public static byte[][] deriveKeys(byte[] inputKeyMaterial, byte[] transcript) {
        byte[] expanded = Hkdf.sha256(
            inputKeyMaterial,
            transcript,
            INFO.getBytes(StandardCharsets.UTF_8),
            64
        );
        byte[][] keys = new byte[2][];
        keys[0] = new byte[32];
        keys[1] = new byte[32];
        System.arraycopy(expanded, 0, keys[0], 0, 32);
        System.arraycopy(expanded, 32, keys[1], 0, 32);
        return keys;
    }

    /**
     * The per-request salt. It binds the suite, the host bundle, the device, the
     * request id and timestamp, and the exact ephemeral and ciphertext bytes.
     */
    public static byte[] requestTranscript(
        byte[] bundleHash,
        String deviceId,
        String requestId,
        long timestampMillis,
        byte[] ephemeralPublicKey,
        byte[] kemCiphertext
    ) {
        byte[] stamp = new byte[8];
        for (int i = 0; i < 8; i++) {
            stamp[i] = (byte) (timestampMillis >>> (56 - 8 * i));
        }
        return transcript(
            TRANSCRIPT_DOMAIN,
            bundleHash,
            utf8(deviceId),
            utf8(requestId),
            stamp,
            ephemeralPublicKey,
            kemCiphertext
        );
    }

    /**
     * The pairing salt. The device id is only known from the response, which is
     * safe: a rewritten id changes the salt, so the sealed payload fails to open.
     */
    public static byte[] pairTranscript(
        byte[] bundleHash,
        String hostId,
        String deviceId,
        byte[] ephemeralPublicKey,
        byte[] kemCiphertext
    ) {
        byte[] material = sha256(concat(ephemeralPublicKey, kemCiphertext));
        return transcript(PAIR_DOMAIN, bundleHash, utf8(hostId), utf8(deviceId), material);
    }

    /** Bundle-bound schedule for protecting the one-time code before a device id exists. */
    public static byte[] pairInitTranscript(
        byte[] bundleHash,
        String hostId,
        byte[] ephemeralPublicKey,
        byte[] kemCiphertext
    ) {
        byte[] material = sha256(concat(ephemeralPublicKey, kemCiphertext));
        return transcript(PAIR_INIT_DOMAIN, bundleHash, utf8(hostId), material);
    }

    public static byte[] derivePairInitKey(byte[] inputKeyMaterial, byte[] transcript) {
        return Hkdf.sha256(
            inputKeyMaterial,
            transcript,
            INFO.getBytes(StandardCharsets.UTF_8),
            32
        );
    }

    public static String requestAad(String deviceId, String requestId, long timestampMillis) {
        return "req|v2|" + deviceId + "|" + requestId + "|" + timestampMillis;
    }

    public static String responseAad(String deviceId, String requestId) {
        return "resp|v2|" + deviceId + "|" + requestId;
    }

    public static String responseEndAad(String deviceId, String requestId, int frameIndex) {
        return responseAad(deviceId, requestId) + "|" + frameIndex + "|end";
    }

    static byte[] transcript(String domain, byte[] bundleHash, byte[]... fields) {
        byte[] domainBytes = (domain + "\u0000").getBytes(StandardCharsets.UTF_8);
        int length = domainBytes.length + bundleHash.length;
        for (byte[] field : fields) {
            length += 4 + field.length;
        }
        byte[] out = new byte[length];
        int at = 0;
        System.arraycopy(domainBytes, 0, out, at, domainBytes.length);
        at += domainBytes.length;
        System.arraycopy(bundleHash, 0, out, at, bundleHash.length);
        at += bundleHash.length;
        for (byte[] field : fields) {
            out[at] = (byte) (field.length >>> 24);
            out[at + 1] = (byte) (field.length >>> 16);
            out[at + 2] = (byte) (field.length >>> 8);
            out[at + 3] = (byte) field.length;
            at += 4;
            System.arraycopy(field, 0, out, at, field.length);
            at += field.length;
        }
        return Hkdf.sha256(out);
    }

    private static byte[] utf8(String value) {
        return value.getBytes(StandardCharsets.UTF_8);
    }

    private static byte[] sha256(byte[] value) {
        return Hkdf.sha256(value);
    }

    private static byte[] concat(byte[] first, byte[] second) {
        byte[] out = new byte[first.length + second.length];
        System.arraycopy(first, 0, out, 0, first.length);
        System.arraycopy(second, 0, out, first.length, second.length);
        return out;
    }

    private V2Protocol() {}
}
