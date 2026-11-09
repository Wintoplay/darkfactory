package dev.darkfactory.mobile;

import dev.darkfactory.mobile.crypto.Hkdf;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.KeyFactory;
import java.security.MessageDigest;
import java.security.PublicKey;
import java.security.Signature;
import java.security.spec.X509EncodedKeySpec;
import java.util.Base64;

/**
 * The daemon's signed v2 key bundle.
 *
 * <p>The P-256 key is the pin. X25519 and ML-KEM-768 are advertised inside the
 * bundle and signed by that key, so rotating them is a bundle change, not a new
 * host. Everything here is pure Java and byte-array based: the Android JSON
 * layer only extracts strings, and the JVM interop test compiles this class
 * unchanged.
 */
public final class HostKey {
    public static final String SUITE = "x25519-mlkem768-v1";
    public static final int MLKEM_PUBLIC_BYTES = 1184;
    public static final int X25519_BYTES = 32;

    private static final String BUNDLE_DOMAIN = "darkfactory-e2ee-bundle-v2";
    private static final String PIN_DOMAIN = "df-e2ee-v2";

    public final String publicKeyB64;
    public final String suite;
    public final int epoch;
    public final byte[] x25519;
    public final byte[] mlkem;
    public final byte[] bundleHash;
    public final String fingerprintV2;

    private HostKey(
        String publicKeyB64,
        String suite,
        int epoch,
        byte[] x25519,
        byte[] mlkem,
        byte[] bundleHash,
        String fingerprintV2
    ) {
        this.publicKeyB64 = publicKeyB64;
        this.suite = suite;
        this.epoch = epoch;
        this.x25519 = x25519;
        this.mlkem = mlkem;
        this.bundleHash = bundleHash;
        this.fingerprintV2 = fingerprintV2;
    }

    public static void requireBundleVersion(int version) {
        if (version != 2) {
            throw new IllegalStateException("The host uses an unsupported key bundle version.");
        }
    }

    public void requireStoredPin(String expectedPublicKey, String expectedSuite, String expectedFingerprint) {
        if (!publicKeyB64.equals(expectedPublicKey) || !suite.equals(expectedSuite) ||
            !fingerprintV2.equals(expectedFingerprint)) {
            throw new IllegalStateException("The stored host identity or suite pin does not match its bundle.");
        }
    }

    /**
     * Verifies the bundle signature with the P-256 key it carries and returns
     * the key material. A bundle that names any other suite, has the wrong key
     * sizes, or fails ECDSA is rejected before a request is sealed.
     */
    public static HostKey verify(
        String publicKeyB64,
        int epoch,
        String suite,
        String x25519B64,
        String mlkemB64,
        String signatureB64
    ) {
        if (!SUITE.equals(suite)) {
            throw new IllegalStateException("The host does not offer the hybrid key exchange.");
        }
        if (epoch < 1) {
            throw new IllegalStateException("The host key bundle epoch is invalid.");
        }
        byte[] p256Der = decode(publicKeyB64, "host public key");
        byte[] x25519 = decode(x25519B64, "host x25519 key");
        byte[] mlkem = decode(mlkemB64, "host mlkem key");
        byte[] signature = decode(signatureB64, "host bundle signature");
        if (x25519.length != X25519_BYTES) {
            throw new IllegalStateException("The host x25519 key has the wrong size.");
        }
        if (mlkem.length != MLKEM_PUBLIC_BYTES) {
            throw new IllegalStateException("The host ML-KEM-768 key has the wrong size.");
        }
        if (p256Der.length == 0 || signature.length == 0) {
            throw new IllegalStateException("The host bundle is incomplete.");
        }

        byte[] canonical = canonical(p256Der, epoch, suite, x25519, mlkem);
        try {
            PublicKey publicKey = KeyFactory.getInstance("EC")
                .generatePublic(new X509EncodedKeySpec(p256Der));
            Signature verifier = Signature.getInstance("SHA256withECDSA");
            verifier.initVerify(publicKey);
            verifier.update(canonical);
            if (!verifier.verify(signature)) {
                throw new IllegalStateException("The host key bundle signature is not valid.");
            }
        } catch (GeneralSecurityException error) {
            throw new IllegalStateException("The host key bundle is not usable.", error);
        }

        String canonicalB64 = Base64.getEncoder().encodeToString(p256Der);
        return new HostKey(
            canonicalB64,
            suite,
            epoch,
            x25519,
            mlkem,
            Hkdf.sha256(canonical),
            fingerprintV2(canonicalB64, suite)
        );
    }

    /** The v2 pin: the identity and the suite, but not the rotating halves. */
    public static String fingerprintV2(String publicKeyB64, String suite) {
        String canonical = Base64.getEncoder().encodeToString(decode(publicKeyB64, "host public key"));
        String material = PIN_DOMAIN + "\u0000" + canonical + "\u0000" + suite;
        return HostFingerprint.group(
            Hkdf.sha256(material.getBytes(StandardCharsets.UTF_8)));
    }

    /** The exact byte string the daemon signs: domain, epoch, length-prefixed fields. */
    static byte[] canonical(byte[] p256Der, int epoch, String suite, byte[] x25519, byte[] mlkem) {
        byte[] domain = (BUNDLE_DOMAIN + "\u0000").getBytes(StandardCharsets.UTF_8);
        byte[] suiteBytes = suite.getBytes(StandardCharsets.UTF_8);
        int length = domain.length + 4
            + 2 + suiteBytes.length
            + 2 + p256Der.length
            + 2 + x25519.length
            + 2 + mlkem.length;
        byte[] out = new byte[length];
        int at = 0;
        System.arraycopy(domain, 0, out, at, domain.length);
        at += domain.length;
        out[at] = (byte) (epoch >>> 24);
        out[at + 1] = (byte) (epoch >>> 16);
        out[at + 2] = (byte) (epoch >>> 8);
        out[at + 3] = (byte) epoch;
        at += 4;
        at = writeLengthPrefixed(out, at, suiteBytes);
        at = writeLengthPrefixed(out, at, p256Der);
        at = writeLengthPrefixed(out, at, x25519);
        writeLengthPrefixed(out, at, mlkem);
        return out;
    }

    private static int writeLengthPrefixed(byte[] out, int offset, byte[] field) {
        if (field.length > 0xffff) {
            throw new IllegalArgumentException("host bundle field is too large");
        }
        out[offset] = (byte) (field.length >>> 8);
        out[offset + 1] = (byte) field.length;
        System.arraycopy(field, 0, out, offset + 2, field.length);
        return offset + 2 + field.length;
    }

    private static byte[] decode(String value, String what) {
        try {
            return Base64.getDecoder().decode(value);
        } catch (IllegalArgumentException error) {
            throw new IllegalStateException("The " + what + " is not valid base64.", error);
        }
    }

}
