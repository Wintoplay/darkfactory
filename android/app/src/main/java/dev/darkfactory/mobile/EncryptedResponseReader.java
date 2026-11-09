package dev.darkfactory.mobile;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;

/** Bounded framing and authenticated completion checks shared by proxy and notifications. */
public final class EncryptedResponseReader {
    public interface Opener {
        byte[] openData(byte[] frame, int index) throws Exception;
        /** Returns null only when this frame did not authenticate as the end marker. */
        byte[] openEnd(byte[] frame, int index) throws Exception;
    }

    public interface Listener {
        void onHead(byte[] plaintext) throws Exception;
        void onBody(byte[] plaintext) throws Exception;
    }

    public static void read(
        InputStream input,
        Opener opener,
        Listener listener,
        int maxFrameBytes,
        long maxBodyBytes,
        boolean requireEnd
    ) throws Exception {
        int firstLength = readFrameLength(input, maxFrameBytes);
        if (firstLength < 28) {
            throw new IOException("invalid encrypted response head frame length");
        }
        listener.onHead(opener.openData(readExactly(input, firstLength), 0));

        int frameIndex = 1;
        long bodyBytes = 0;
        while (true) {
            int length = readOptionalFrameLength(input, maxFrameBytes);
            if (length < 0) {
                if (requireEnd) throw new IOException("encrypted response ended without an authenticated terminator");
                return;
            }
            if (length < 28) {
                throw new IOException("invalid encrypted response frame length");
            }
            byte[] frame = readExactly(input, length);
            byte[] marker = opener.openEnd(frame, frameIndex);
            if (marker != null) {
                byte[] expected = V2Protocol.RESPONSE_END_MARKER.getBytes(StandardCharsets.US_ASCII);
                if (!Arrays.equals(expected, marker)) {
                    throw new IOException("invalid authenticated response terminator");
                }
                if (input.read() != -1) throw new IOException("bytes follow the authenticated response terminator");
                return;
            }

            byte[] plaintext = opener.openData(frame, frameIndex);
            bodyBytes += plaintext.length;
            if (maxBodyBytes >= 0 && bodyBytes > maxBodyBytes) {
                throw new IOException("encrypted response body exceeds its limit");
            }
            listener.onBody(plaintext);
            if (frameIndex == Integer.MAX_VALUE) throw new IOException("encrypted response has too many frames");
            frameIndex++;
        }
    }

    private static int readFrameLength(InputStream input, int maxFrameBytes) throws IOException {
        int length = readOptionalFrameLength(input, maxFrameBytes);
        if (length < 0) throw new IOException("encrypted response has no head frame");
        return length;
    }

    private static int readOptionalFrameLength(InputStream input, int maxFrameBytes) throws IOException {
        int first = input.read();
        if (first < 0) return -1;
        int second = input.read();
        int third = input.read();
        int fourth = input.read();
        if (second < 0 || third < 0 || fourth < 0) throw new IOException("encrypted frame length ended early");
        long length = ((long) first << 24) | ((long) second << 16) | ((long) third << 8) | fourth;
        if (length < 28 || length > maxFrameBytes) {
            throw new IOException("invalid encrypted response frame length");
        }
        return (int) length;
    }

    private static byte[] readExactly(InputStream input, int length) throws IOException {
        byte[] result = new byte[length];
        int offset = 0;
        while (offset < length) {
            int read = input.read(result, offset, length - offset);
            if (read < 0) throw new IOException("encrypted frame ended early");
            offset += read;
        }
        return result;
    }

    private EncryptedResponseReader() {}
}
