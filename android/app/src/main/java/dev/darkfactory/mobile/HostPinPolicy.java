package dev.darkfactory.mobile;

import java.security.MessageDigest;

/** Registry replacement rules for a host identity that has already been pinned. */
public final class HostPinPolicy {
    public static void requireHostId(String oldHostId, String newHostId) {
        if (oldHostId == null || oldHostId.isEmpty() || !oldHostId.equals(newHostId)) {
            throw new IllegalStateException("The host id changed for an existing pin. Explicitly Forget it before pairing again.");
        }
    }

    public static void requireBundleAdvance(
        int oldEpoch,
        byte[] oldBundleHash,
        int newEpoch,
        byte[] newBundleHash
    ) {
        if (oldEpoch < 1 || newEpoch < 1 || oldBundleHash == null || newBundleHash == null ||
            oldBundleHash.length != 32 || newBundleHash.length != 32) {
            throw new IllegalStateException("A stored host bundle is invalid. Explicitly Forget it before pairing again.");
        }
        if (newEpoch < oldEpoch ||
            (newEpoch == oldEpoch && !MessageDigest.isEqual(oldBundleHash, newBundleHash))) {
            throw new IllegalStateException("The host key bundle rolled back. Verify and explicitly Forget before pairing again.");
        }
    }

    public static void requireReplacement(
        String oldPublicKey,
        String oldSuite,
        String oldFingerprint,
        String oldFingerprintV2,
        String newPublicKey,
        String newSuite,
        String newFingerprint,
        String newFingerprintV2
    ) {
        if (oldPublicKey == null || oldPublicKey.isEmpty() ||
            oldFingerprint == null || oldFingerprint.isEmpty()) {
            throw new IllegalStateException("The saved host pin is unusable. Explicitly Forget it before pairing again.");
        }
        if (newPublicKey == null || newPublicKey.isEmpty() ||
            newFingerprint == null || newFingerprint.isEmpty()) {
            throw new IllegalStateException("A replacement host must have a verified identity pin.");
        }
        if (!oldPublicKey.equals(newPublicKey)) {
            throw new IllegalStateException("This host identity is already pinned. Explicitly Forget it before replacing it.");
        }
        if (!oldSuite.isEmpty() && !HostKey.SUITE.equals(oldSuite)) {
            throw new IllegalStateException("The saved host suite is unusable. Explicitly Forget it before pairing again.");
        }
        if (!newSuite.isEmpty() && !HostKey.SUITE.equals(newSuite)) {
            throw new IllegalStateException("The replacement host offers an unsupported suite.");
        }
        if (HostKey.SUITE.equals(newSuite) &&
            (newFingerprintV2 == null || newFingerprintV2.isEmpty() ||
                !newFingerprint.equals(newFingerprintV2))) {
            throw new IllegalStateException("The replacement hybrid fingerprint is incomplete.");
        }
        if (HostKey.SUITE.equals(oldSuite)) {
            if (!HostKey.SUITE.equals(newSuite)) {
                throw new IllegalStateException("A hybrid host pin cannot be downgraded. Explicitly Forget it to remove the pin.");
            }
            if (oldFingerprintV2 == null || oldFingerprintV2.isEmpty() ||
                !oldFingerprintV2.equals(newFingerprintV2)) {
                throw new IllegalStateException("The hybrid host pin changed. Explicitly Forget it before pairing again.");
            }
        } else if (!HostKey.SUITE.equals(newSuite) && !oldFingerprint.equals(newFingerprint)) {
            throw new IllegalStateException("The legacy host pin changed. Explicitly Forget it before pairing again.");
        }
    }

    private HostPinPolicy() {}
}
