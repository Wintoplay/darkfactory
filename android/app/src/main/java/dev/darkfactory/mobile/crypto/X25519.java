package dev.darkfactory.mobile.crypto;

import java.security.SecureRandom;
import java.util.Arrays;

/** RFC 7748 operations delegated to Bouncy Castle's maintained implementation. */
public final class X25519 {
    private static final int KEY_BYTES = 32;

    public static final class KeyPair {
        public final byte[] privateKey;
        public final byte[] publicKey;

        KeyPair(byte[] privateKey, byte[] publicKey) {
            this.privateKey = privateKey;
            this.publicKey = publicKey;
        }
    }

    public static KeyPair generateKeyPair(SecureRandom random) {
        byte[] privateKey = new byte[KEY_BYTES];
        byte[] publicKey = new byte[KEY_BYTES];
        org.bouncycastle.math.ec.rfc7748.X25519.generatePrivateKey(random, privateKey);
        org.bouncycastle.math.ec.rfc7748.X25519.generatePublicKey(privateKey, 0, publicKey, 0);
        return new KeyPair(privateKey, publicKey);
    }

    public static byte[] publicKey(byte[] scalar) {
        requireLength(scalar, "X25519 scalar");
        byte[] publicKey = new byte[KEY_BYTES];
        org.bouncycastle.math.ec.rfc7748.X25519.generatePublicKey(scalar, 0, publicKey, 0);
        return publicKey;
    }

    public static byte[] scalarMult(byte[] scalar, byte[] point) {
        requireLength(scalar, "X25519 scalar");
        requireLength(point, "X25519 point");
        byte[] shared = new byte[KEY_BYTES];
        if (!org.bouncycastle.math.ec.rfc7748.X25519.calculateAgreement(
            scalar, 0, point, 0, shared, 0)) {
            Arrays.fill(shared, (byte) 0);
            throw new IllegalArgumentException("X25519 peer produced an all-zero shared secret");
        }
        return shared;
    }

    private static void requireLength(byte[] value, String name) {
        if (value == null || value.length != KEY_BYTES) {
            throw new IllegalArgumentException(name + " must be 32 bytes");
        }
    }

    private X25519() {}
}
