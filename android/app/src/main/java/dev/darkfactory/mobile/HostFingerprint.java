package dev.darkfactory.mobile;

import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.Base64;

final class HostFingerprint {
    static String verified(String publicKey, String supplied) {
        String computed = of(publicKey);
        if (!computed.equals(supplied)) {
            throw new IllegalStateException("The host fingerprint does not match its public key.");
        }
        return computed;
    }

    static String of(String publicKey) {
        try {
            return group(MessageDigest.getInstance("SHA-256").digest(Base64.getDecoder().decode(publicKey)));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 unavailable", e);
        }
    }

    /**
     * Formats a digest the way the desktop Pair phone dialog does: uppercase hex
     * in groups of four characters. v2 hashes a domain-separated string instead
     * of the raw SPKI, but the displayed shape is identical.
     */
    static String group(byte[] digest) {
        StringBuilder value = new StringBuilder();
        for (int i = 0; i < digest.length; i++) {
            if (i > 0 && i % 2 == 0) value.append(' ');
            value.append(String.format(java.util.Locale.ROOT, "%02X", digest[i] & 0xff));
        }
        return value.toString();
    }

    private HostFingerprint() {}
}
