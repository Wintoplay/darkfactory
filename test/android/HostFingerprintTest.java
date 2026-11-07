package dev.darkfactory.mobile;

public final class HostFingerprintTest {
    public static void main(String[] args) {
        String actual = HostFingerprint.of("YWJj");
        String expected = "BA78 16BF 8F01 CFEA 4141 40DE 5DAE 2223 B003 61A3 9617 7A9C B410 FF61 F200 15AD";
        if (!expected.equals(actual)) throw new AssertionError(actual);
        if (!expected.equals(HostFingerprint.verified("YWJj", expected))) throw new AssertionError("valid key rejected");
        try {
            HostFingerprint.verified("YWJk", expected);
            throw new AssertionError("substituted key accepted");
        } catch (IllegalStateException expectedFailure) {
            // The displayed fingerprint must describe the received key.
        }
    }
}
