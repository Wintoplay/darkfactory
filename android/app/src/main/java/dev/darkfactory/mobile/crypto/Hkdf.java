package dev.darkfactory.mobile.crypto;

import java.security.MessageDigest;
import org.bouncycastle.crypto.digests.SHA256Digest;
import org.bouncycastle.crypto.generators.HKDFBytesGenerator;
import org.bouncycastle.crypto.params.HKDFParameters;

/**
 * HKDF with HMAC-SHA-256 (RFC 5869). The daemon uses Go's {@code crypto/hkdf},
 * so both sides must expand exactly the same way: extract then expand, with the
 * info string passed through unchanged.
 */
public final class Hkdf {
    private static final int HASH_BYTES = 32;

    public static byte[] sha256(byte[] inputKeyMaterial, byte[] salt, byte[] info, int length) {
        if (length <= 0 || length > 255 * HASH_BYTES) {
            throw new IllegalArgumentException("invalid HKDF output length");
        }
        byte[] output = new byte[length];
        HKDFBytesGenerator generator = new HKDFBytesGenerator(new SHA256Digest());
        generator.init(new HKDFParameters(inputKeyMaterial, salt, info));
        generator.generateBytes(output, 0, output.length);
        return output;
    }

    public static byte[] sha256(byte[] message) {
        try {
            return MessageDigest.getInstance("SHA-256").digest(message);
        } catch (java.security.GeneralSecurityException error) {
            throw new IllegalStateException("SHA-256 unavailable", error);
        }
    }

    private Hkdf() {}
}
