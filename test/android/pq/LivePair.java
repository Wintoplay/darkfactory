package dev.darkfactory.mobile.pq;

import dev.darkfactory.mobile.Envelope;
import dev.darkfactory.mobile.EncryptedResponseReader;
import dev.darkfactory.mobile.HostKey;
import dev.darkfactory.mobile.V2Protocol;
import dev.darkfactory.mobile.crypto.MlKem768;
import dev.darkfactory.mobile.crypto.X25519;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.util.Base64;

/**
 * Pairs and reads a live daemon using the app's own Java protocol classes.
 *
 * <p>{@code check-pq.sh} proves the derivation matches Go offline. This program
 * proves the same classes talk to a running {@code --e2ee-min-suite=2} daemon:
 * it verifies the signed bundle, pairs with a real one-time code, opens the
 * sealed token, seals a {@code GET /api/v1/state}, opens the framed reply, and
 * then checks that a v1 envelope and a replayed envelope are both refused.
 */
public final class LivePair {
    public static void main(String[] args) throws Exception {
        if (args.length != 2) {
            throw new IllegalArgumentException("usage: LivePair BASE_URL PAIR_CODE");
        }
        String base = args[0].replaceAll("/+$", "");
        String code = args[1];

        String hostKeyBody = get(base + "/api/v1/e2ee/host-key");
        String hostId = field(hostKeyBody, "hostId");
        String publicKey = field(hostKeyBody, "publicKey");
        require(hostId.length() > 0 && publicKey.length() > 0, "host-key response identity");
        int maxVersion = Integer.parseInt(field(hostKeyBody, "maxVersion"));
        require(maxVersion >= 2, "host advertises v2");

        String bundleBody = object(hostKeyBody, "bundle");
        require(bundleBody != null, "host publishes a bundle");
        HostKey host = HostKey.verify(
            field(bundleBody, "p256"),
            Integer.parseInt(field(bundleBody, "epoch")),
            field(bundleBody, "suite"),
            field(bundleBody, "x25519"),
            field(bundleBody, "mlkem"),
            field(bundleBody, "sig")
        );
        require(host.publicKeyB64.equals(publicKey), "bundle identity matches host-key identity");
        String advertised = field(hostKeyBody, "fingerprintV2");
        require(advertised.isEmpty() || advertised.equals(host.fingerprintV2),
            "advertised fingerprintV2 matches the bundle");
        System.out.println("live: host " + hostId + " pin " + host.fingerprintV2);

        X25519.KeyPair ephemeral = X25519.generateKeyPair(new SecureRandom());
        MlKem768.Encapsulation encapsulation = MlKem768.encapsulate(host.mlkem);
        byte[] sharedDh = X25519.scalarMult(ephemeral.privateKey, host.x25519);
        byte[] ikm = V2Protocol.combine(sharedDh, encapsulation.sharedKey);
        byte[] pairInitTranscript = V2Protocol.pairInitTranscript(
            host.bundleHash, hostId, ephemeral.publicKey, encapsulation.ciphertext);
        byte[] pairInitKey = V2Protocol.derivePairInitKey(ikm, pairInitTranscript);
        String sealedCode = b64(Envelope.seal(
            pairInitKey, code.getBytes(StandardCharsets.UTF_8), pairInitTranscript));
        String pairRequest = "{"
            + "\"v\":2,"
            + "\"suite\":" + quote(HostKey.SUITE) + ","
            + "\"epk\":" + quote(b64(ephemeral.publicKey)) + ","
            + "\"kem\":" + quote(b64(encapsulation.ciphertext)) + ","
            + "\"sealedCode\":" + quote(sealedCode)
            + "}";
        byte[] substitutedEpk = ephemeral.publicKey.clone();
        substitutedEpk[0] ^= 1;
        String substitutedRequest = pairRequest.replace(
            quote(b64(ephemeral.publicKey)), quote(b64(substitutedEpk)));
        Refusal substitution = postRefusal(base + "/api/v1/pair", substitutedRequest);
        require(substitution.status >= 400,
            "pair-init rejects an EPK substitution before code consumption (HTTP " + substitution.status + ")");
        String pairBody = post(base + "/api/v1/pair", pairRequest, null);
        String deviceId = field(pairBody, "deviceId");
        String responseHostId = field(pairBody, "hostId");
        String sealed = field(pairBody, "sealedPayload");
        require(deviceId.length() > 0 && sealed.length() > 0, "sealed pairing payload");
        require(responseHostId.equals(hostId), "pair response names the same host");

        byte[] pairTranscript = V2Protocol.pairTranscript(
            host.bundleHash, hostId, deviceId, ephemeral.publicKey, encapsulation.ciphertext);
        byte[][] pairKeys = V2Protocol.deriveKeys(ikm, pairTranscript);
        String payload = new String(
            Envelope.open(pairKeys[1], Base64.getDecoder().decode(sealed), pairTranscript),
            StandardCharsets.UTF_8);
        String token = field(payload, "token");
        require(token.length() > 0, "sealed token");
        require(field(payload, "deviceId").equals(deviceId), "sealed device id");
        require(field(payload, "hostId").equals(hostId), "sealed host id");
        require(field(payload, "suite").equals(HostKey.SUITE), "sealed suite");
        System.out.println("live: paired device " + deviceId);

        String requestId = "6c6976652d706171" + randomHex(8);
        long timestamp = System.currentTimeMillis();
        String request = "{"
            + "\"method\":\"GET\","
            + "\"path\":\"/api/v1/state\","
            + "\"headers\":{\"accept\":\"application/json\"},"
            + "\"body\":\"\","
            + "\"token\":" + quote(token)
            + "}";
        byte[] requestTranscript = V2Protocol.requestTranscript(
            host.bundleHash, deviceId, requestId, timestamp,
            ephemeral.publicKey, encapsulation.ciphertext);
        byte[][] keys = V2Protocol.deriveKeys(ikm, requestTranscript);
        Envelope.Parts parts = Envelope.sealParts(
            keys[0],
            request.getBytes(StandardCharsets.UTF_8),
            V2Protocol.requestAad(deviceId, requestId, timestamp).getBytes(StandardCharsets.UTF_8));
        String envelope = "{"
            + "\"v\":2,"
            + "\"deviceId\":" + quote(deviceId) + ","
            + "\"id\":" + quote(requestId) + ","
            + "\"ts\":" + timestamp + ","
            + "\"suite\":" + quote(HostKey.SUITE) + ","
            + "\"epk\":" + quote(b64(ephemeral.publicKey)) + ","
            + "\"kem\":" + quote(b64(encapsulation.ciphertext)) + ","
            + "\"nonce\":" + quote(b64(parts.nonce)) + ","
            + "\"ciphertext\":" + quote(b64(parts.ciphertext))
            + "}";

        byte[] reply = postBytes(base + "/api/v1/e2ee", envelope);
        String[] frames = openFrames(reply, keys[1], deviceId, requestId);
        require(frames.length >= 2, "state reply has a head and a body");
        String head = frames[0];
        require(Integer.parseInt(field(head, "status")) == 200, "state status is 200");
        StringBuilder stateBuilder = new StringBuilder();
        for (int i = 1; i < frames.length; i++) {
            stateBuilder.append(frames[i]);
        }
        String state = stateBuilder.toString();
        require(state.contains("\"hostId\""), "state body names the host");
        System.out.println("live: encrypted /state opened (" + state.length() + " bytes)");

        String downgrade = "{"
            + "\"v\":1,"
            + "\"deviceId\":" + quote(deviceId) + ","
            + "\"id\":" + quote(randomHex(16)) + ","
            + "\"ts\":" + System.currentTimeMillis() + ","
            + "\"nonce\":" + quote(b64(new byte[12])) + ","
            + "\"ciphertext\":" + quote(b64(new byte[32]))
            + "}";
        Refusal refusal = postRefusal(base + "/api/v1/e2ee", downgrade);
        require(refusal.status == 403, "v1 envelope refused with 403 (got " + refusal.status + ")");
        require(refusal.body.contains("e2ee_suite_downgrade"),
            "v1 refusal names e2ee_suite_downgrade: " + refusal.body);

        Refusal replay = postRefusal(base + "/api/v1/e2ee", envelope);
        require(replay.status != 200, "replayed envelope is refused (got " + replay.status + ")");
        System.out.println("live: downgrade and replay refused");
        System.out.println("live: v2 pairing and envelope round trip agree with the daemon");
    }

    private static String[] openFrames(byte[] reply, byte[] key, String deviceId, String requestId) throws Exception {
        java.util.List<String> frames = new java.util.ArrayList<>();
        String aad = V2Protocol.responseAad(deviceId, requestId);
        EncryptedResponseReader.read(new java.io.ByteArrayInputStream(reply),
            new EncryptedResponseReader.Opener() {
                @Override public byte[] openData(byte[] frame, int index) {
                    return Envelope.open(key, frame, (aad + "|" + index).getBytes(StandardCharsets.UTF_8));
                }
                @Override public byte[] openEnd(byte[] frame, int index) {
                    try {
                        return Envelope.open(key, frame, V2Protocol.responseEndAad(deviceId, requestId, index)
                            .getBytes(StandardCharsets.UTF_8));
                    } catch (IllegalStateException notEnd) {
                        return null;
                    }
                }
            },
            new EncryptedResponseReader.Listener() {
                @Override public void onHead(byte[] plain) {
                    frames.add(new String(plain, StandardCharsets.UTF_8));
                }
                @Override public void onBody(byte[] plain) {
                    frames.add(new String(plain, StandardCharsets.UTF_8));
                }
            },
            4 * 1024 * 1024, 4L * 1024 * 1024, true);
        return frames.toArray(new String[0]);
    }

    private static final class Refusal {
        final int status;
        final String body;

        Refusal(int status, String body) {
            this.status = status;
            this.body = body;
        }
    }

    private static String get(String url) throws IOException {
        HttpURLConnection http = open(url, "GET");
        try {
            return readAll(http, 200);
        } finally {
            http.disconnect();
        }
    }

    private static String post(String url, String body, String token) throws IOException {
        HttpURLConnection http = open(url, "POST");
        http.setRequestProperty("X-DF-Request", "1");
        if (token != null) {
            http.setRequestProperty("Authorization", "Bearer " + token);
        }
        writeBody(http, body.getBytes(StandardCharsets.UTF_8));
        try {
            return readAll(http, 200);
        } finally {
            http.disconnect();
        }
    }

    private static byte[] postBytes(String url, String body) throws IOException {
        HttpURLConnection http = open(url, "POST");
        http.setRequestProperty("Accept", "application/x-df-e2ee");
        http.setRequestProperty("X-DF-E2EE", "1");
        http.setRequestProperty("X-DF-Request", "1");
        writeBody(http, body.getBytes(StandardCharsets.UTF_8));
        try {
            return readAllBytes(http, 200);
        } finally {
            http.disconnect();
        }
    }

    private static Refusal postRefusal(String url, String body) throws IOException {
        HttpURLConnection http = open(url, "POST");
        http.setRequestProperty("X-DF-Request", "1");
        writeBody(http, body.getBytes(StandardCharsets.UTF_8));
        try {
            int status = http.getResponseCode();
            InputStream stream = status >= 400 ? http.getErrorStream() : http.getInputStream();
            return new Refusal(status, stream == null ? "" : new String(drain(stream), StandardCharsets.UTF_8));
        } finally {
            http.disconnect();
        }
    }

    private static HttpURLConnection open(String url, String method) throws IOException {
        HttpURLConnection http = (HttpURLConnection) java.net.URI.create(url).toURL().openConnection();
        http.setRequestMethod(method);
        http.setConnectTimeout(10_000);
        http.setReadTimeout(15_000);
        http.setInstanceFollowRedirects(false);
        http.setUseCaches(false);
        http.setRequestProperty("Accept", "application/json");
        http.setRequestProperty("User-Agent", "DarkFactory-PQ-Live/0.1");
        return http;
    }

    private static void writeBody(HttpURLConnection http, byte[] body) throws IOException {
        http.setDoOutput(true);
        http.setRequestProperty("Content-Type", "application/json");
        http.setFixedLengthStreamingMode(body.length);
        try (OutputStream output = http.getOutputStream()) {
            output.write(body);
        }
    }

    private static String readAll(HttpURLConnection http, int expect) throws IOException {
        return new String(readAllBytes(http, expect), StandardCharsets.UTF_8);
    }

    private static byte[] readAllBytes(HttpURLConnection http, int expect) throws IOException {
        int status = http.getResponseCode();
        InputStream stream = status >= 400 ? http.getErrorStream() : http.getInputStream();
        byte[] body = stream == null ? new byte[0] : drain(stream);
        if (status != expect) {
            throw new IOException("HTTP " + status + ": " + new String(body, StandardCharsets.UTF_8));
        }
        return body;
    }

    private static byte[] drain(InputStream stream) throws IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        byte[] buffer = new byte[8192];
        int read;
        while ((read = stream.read(buffer)) != -1) {
            out.write(buffer, 0, read);
            if (out.size() > 1 << 22) {
                throw new IOException("response larger than 4 MiB");
            }
        }
        stream.close();
        return out.toByteArray();
    }

    /** The specific field shapes this harness reads; a missing field is fatal. */
    private static String field(String json, String name) {
        int key = json.indexOf("\"" + name + "\"");
        if (key < 0) {
            return "";
        }
        int at = json.indexOf(':', key + name.length() + 2);
        if (at < 0) {
            return "";
        }
        at++;
        while (at < json.length() && Character.isWhitespace(json.charAt(at))) {
            at++;
        }
        if (at >= json.length()) {
            return "";
        }
        if (json.charAt(at) == '"') {
            StringBuilder value = new StringBuilder();
            at++;
            while (at < json.length()) {
                char c = json.charAt(at++);
                if (c == '"') {
                    return value.toString();
                }
                if (c == '\\' && at < json.length()) {
                    value.append(json.charAt(at++));
                } else {
                    value.append(c);
                }
            }
            return "";
        }
        int start = at;
        while (at < json.length()) {
            char c = json.charAt(at);
            if (c == ',' || c == '}' || Character.isWhitespace(c)) {
                break;
            }
            at++;
        }
        return json.substring(start, at);
    }

    private static String object(String json, String name) {
        int start = json.indexOf("\"" + name + "\"");
        if (start < 0) {
            return null;
        }
        start = json.indexOf('{', start);
        if (start < 0) {
            return null;
        }
        int depth = 0;
        boolean inString = false;
        for (int i = start; i < json.length(); i++) {
            char c = json.charAt(i);
            if (inString) {
                if (c == '\\') {
                    i++;
                } else if (c == '"') {
                    inString = false;
                }
                continue;
            }
            if (c == '"') {
                inString = true;
            } else if (c == '{') {
                depth++;
            } else if (c == '}') {
                depth--;
                if (depth == 0) {
                    return json.substring(start, i + 1);
                }
            }
        }
        return null;
    }

    private static String quote(String value) {
        return "\"" + value.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }

    private static String b64(byte[] value) {
        return Base64.getEncoder().encodeToString(value);
    }

    private static String randomHex(int bytes) {
        byte[] raw = new byte[bytes];
        new SecureRandom().nextBytes(raw);
        StringBuilder builder = new StringBuilder();
        for (byte b : raw) {
            builder.append(String.format(java.util.Locale.ROOT, "%02x", b & 0xff));
        }
        return builder.toString();
    }

    private static void require(boolean condition, String what) {
        if (!condition) {
            throw new AssertionError(what);
        }
    }

    private LivePair() {}
}
