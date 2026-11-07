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
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(Base64.getDecoder().decode(publicKey));
            StringBuilder value = new StringBuilder();
            for (int i = 0; i < digest.length; i++) {
                if (i > 0 && i % 2 == 0) value.append(' ');
                value.append(String.format(java.util.Locale.ROOT, "%02X", digest[i] & 0xff));
            }
            return value.toString();
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 unavailable", e);
        }
    }

    private HostFingerprint() {}
}
