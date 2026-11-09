package dev.darkfactory.mobile;

import java.security.GeneralSecurityException;
import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;

/**
 * AES-256-GCM as the daemon frames it: a 12-byte nonce followed by the sealed
 * bytes, authenticated against an AAD supplied by the caller.
 *
 * <p>This lives in Java rather than Kotlin so the JVM interop harness can open
 * the exact bytes the daemon sends; the app and the test cannot drift.
 */
public final class Envelope {
    public static final int NONCE_BYTES = 12;
    private static final int TAG_BITS = 128;

    /** The two fields the v1 and v2 envelope JSON carry separately. */
    public static final class Parts {
        public final byte[] nonce;
        public final byte[] ciphertext;

        Parts(byte[] nonce, byte[] ciphertext) {
            this.nonce = nonce;
            this.ciphertext = ciphertext;
        }
    }

    public static Parts sealParts(byte[] key, byte[] plaintext, byte[] aad) {
        try {
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            byte[] nonce = new byte[NONCE_BYTES];
            RANDOM.nextBytes(nonce);
            cipher.init(Cipher.ENCRYPT_MODE, new SecretKeySpec(key, "AES"), new GCMParameterSpec(TAG_BITS, nonce));
            cipher.updateAAD(aad);
            return new Parts(nonce, cipher.doFinal(plaintext));
        } catch (GeneralSecurityException error) {
            throw new IllegalStateException("AES-GCM seal failed", error);
        }
    }

    public static byte[] openParts(byte[] key, byte[] nonce, byte[] ciphertext, byte[] aad) {
        if (nonce.length != NONCE_BYTES) {
            throw new IllegalArgumentException("invalid AES-GCM nonce length");
        }
        try {
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(Cipher.DECRYPT_MODE, new SecretKeySpec(key, "AES"), new GCMParameterSpec(TAG_BITS, nonce));
            cipher.updateAAD(aad);
            return cipher.doFinal(ciphertext);
        } catch (GeneralSecurityException error) {
            throw new IllegalStateException("The encrypted reply did not authenticate", error);
        }
    }

    /** One blob: nonce || ciphertext, which is how frames and pair payloads travel. */
    public static byte[] seal(byte[] key, byte[] plaintext, byte[] aad) {
        Parts parts = sealParts(key, plaintext, aad);
        byte[] out = new byte[NONCE_BYTES + parts.ciphertext.length];
        System.arraycopy(parts.nonce, 0, out, 0, NONCE_BYTES);
        System.arraycopy(parts.ciphertext, 0, out, NONCE_BYTES, parts.ciphertext.length);
        return out;
    }

    public static byte[] open(byte[] key, byte[] sealed, byte[] aad) {
        if (sealed.length < NONCE_BYTES + 16) {
            throw new IllegalStateException("encrypted blob is too short");
        }
        byte[] nonce = new byte[NONCE_BYTES];
        byte[] ciphertext = new byte[sealed.length - NONCE_BYTES];
        System.arraycopy(sealed, 0, nonce, 0, NONCE_BYTES);
        System.arraycopy(sealed, NONCE_BYTES, ciphertext, 0, ciphertext.length);
        return openParts(key, nonce, ciphertext, aad);
    }

    private static final java.security.SecureRandom RANDOM = new java.security.SecureRandom();

    private Envelope() {}
}
