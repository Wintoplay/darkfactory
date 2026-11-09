// Command pqcheck generates and verifies the cross-implementation vectors for
// the Android client's v2 envelope.
//
// The Android client uses Bouncy Castle's maintained lightweight APIs and can
// be compiled by a JVM against Go vectors. This tool owns the Go side of that
// comparison:
//
//	pqcheck gen -dir DIR      write a host bundle, keys and hash vectors
//	pqcheck check -dir DIR    verify the Java run's protocol output
//
// gen writes input.properties (public material the Java side consumes),
// vectors.properties (hash and X25519 vectors) and secret.properties (the host
// private halves, used only by check). The Java harness writes proto.properties;
// check recomputes every value in it from the same inputs and exits non-zero on
// any mismatch.
package main

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

const (
	suite          = "x25519-mlkem768-v1"
	bundleTag      = "darkfactory-e2ee-bundle-v2"
	pinTag         = "df-e2ee-v2"
	infoTag        = "darkfactory-e2ee-v2"
	txDomain       = "darkfactory-e2ee-transcript-v2"
	pairDomain     = "darkfactory-e2ee-pair-v2"
	pairInitDomain = "darkfactory-e2ee-pair-init-v2"
	hostID         = "00112233445566778899aabbccddeeff"
	deviceID       = "ffeeddccbbaa99887766554433221100"
	requestID      = "0123456789abcdef0123456789abcdef"
	requestTime    = int64(1763000000123)
)

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: pqcheck gen|check -dir DIR"))
	}
	mode := os.Args[1]
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	dir := fs.String("dir", "", "working directory")
	if err := fs.Parse(os.Args[2:]); err != nil {
		fail(err)
	}
	if *dir == "" {
		fail(errors.New("-dir is required"))
	}
	switch mode {
	case "gen":
		fail(generate(*dir))
	case "check":
		fail(verify(*dir))
	default:
		fail(fmt.Errorf("unknown mode %q", mode))
	}
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "pqcheck:", err)
		os.Exit(1)
	}
}

func generate(dir string) error {
	hostP256, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	hostX25519, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	seed := make([]byte, 64)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	decapsulation, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		return err
	}
	ek := decapsulation.EncapsulationKey().Bytes()

	spki, err := x509MarshalPublic(hostP256.PublicKey())
	if err != nil {
		return err
	}
	epoch := uint32(7)
	canonical := bundleCanonical(spki, epoch, suite, hostX25519.PublicKey().Bytes(), ek)
	signature, err := signBundle(hostP256, canonical)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(canonical)
	public := map[string]string{
		"hostPublicKey": base64.StdEncoding.EncodeToString(spki),
		"epoch":         fmt.Sprint(epoch),
		"suite":         suite,
		"hostX25519":    base64.StdEncoding.EncodeToString(hostX25519.PublicKey().Bytes()),
		"hostMlkem":     base64.StdEncoding.EncodeToString(ek),
		"signature":     base64.StdEncoding.EncodeToString(signature),
		"fingerprintV2": fingerprintV2(base64.StdEncoding.EncodeToString(spki), suite),
		"bundleHash":    hex.EncodeToString(hash[:]),
		"hostId":        hostID,
		"deviceId":      deviceID,
		"requestId":     requestID,
		"timestamp":     fmt.Sprint(requestTime),
	}
	if err := writeProps(filepath.Join(dir, "input.properties"), public); err != nil {
		return err
	}
	secret := map[string]string{
		"hostX25519Private": base64.StdEncoding.EncodeToString(hostX25519.Bytes()),
	}
	if err := writeProps(filepath.Join(dir, "secret.properties"), secret); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "mlkem.seed"), seed, 0o600); err != nil {
		return err
	}

	alicePrivate := mustHex("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
	bobPrivate := mustHex("5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb")
	alice, err := ecdh.X25519().NewPrivateKey(alicePrivate)
	if err != nil {
		return err
	}
	bob, err := ecdh.X25519().NewPrivateKey(bobPrivate)
	if err != nil {
		return err
	}
	shared, err := alice.ECDH(bob.PublicKey())
	if err != nil {
		return err
	}
	vectors := map[string]string{
		"alicePrivate": hex.EncodeToString(alicePrivate),
		"alicePublic":  hex.EncodeToString(alice.PublicKey().Bytes()),
		"bobPrivate":   hex.EncodeToString(bobPrivate),
		"bobPublic":    hex.EncodeToString(bob.PublicKey().Bytes()),
		"shared":       hex.EncodeToString(shared),
	}
	inputs := [][]byte{
		[]byte(""),
		[]byte("abc"),
		pattern(200),
	}
	for i, input := range inputs {
		vectors[fmt.Sprintf("sha3_256_%d", i)] = hex.EncodeToString(sum256(input))
		vectors[fmt.Sprintf("sha3_512_%d", i)] = hex.EncodeToString(sum512(input))
		vectors[fmt.Sprintf("shake128_%d", i)] = hex.EncodeToString(sha3.SumSHAKE128(input, 32))
		vectors[fmt.Sprintf("shake256_%d", i)] = hex.EncodeToString(sha3.SumSHAKE256(input, 64))
		vectors[fmt.Sprintf("input_%d", i)] = hex.EncodeToString(input)
	}
	return writeProps(filepath.Join(dir, "vectors.properties"), vectors)
}

func verify(dir string) error {
	public, err := readProps(filepath.Join(dir, "input.properties"))
	if err != nil {
		return err
	}
	secret, err := readProps(filepath.Join(dir, "secret.properties"))
	if err != nil {
		return err
	}
	proto, err := readProps(filepath.Join(dir, "proto.properties"))
	if err != nil {
		return err
	}
	seed, err := os.ReadFile(filepath.Join(dir, "mlkem.seed"))
	if err != nil {
		return err
	}
	decapsulation, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		return err
	}
	hostPrivate, err := ecdh.X25519().NewPrivateKey(mustB64(secret["hostX25519Private"]))
	if err != nil {
		return err
	}

	// The digest and X25519 checks happened on the Java side; this side only
	// re-checks the bundle hash the Java code derived, so a mistyped domain
	// string cannot pass.
	spki := mustB64(public["hostPublicKey"])
	epoch := atoi(public["epoch"])
	canonical := bundleCanonical(spki, uint32(epoch), public["suite"], mustB64(public["hostX25519"]), mustB64(public["hostMlkem"]))
	hash := sha256.Sum256(canonical)
	if got := hex.EncodeToString(hash[:]); got != proto["bundleHash"] {
		return fmt.Errorf("bundle hash: java %s, go %s", proto["bundleHash"], got)
	}
	if proto["bundleHash"] != public["bundleHash"] {
		return errors.New("java bundle hash does not match the generated bundle")
	}

	kemCiphertext := mustB64(proto["kemCt"])
	sharedKey, err := decapsulation.Decapsulate(kemCiphertext)
	if err != nil {
		return fmt.Errorf("decapsulate java ciphertext: %w", err)
	}
	if got := hex.EncodeToString(sharedKey); got != proto["ssKEM"] {
		return fmt.Errorf("mlkem shared secret: java %s, go %s", proto["ssKEM"], got)
	}
	for i := 2; i <= 8; i++ {
		key := fmt.Sprintf("kemCt%d", i)
		shared, err := decapsulation.Decapsulate(mustB64(proto[key]))
		if err != nil {
			return fmt.Errorf("decapsulate java ciphertext %d: %w", i, err)
		}
		if got := hex.EncodeToString(shared); got != proto[fmt.Sprintf("ssKEM%d", i)] {
			return fmt.Errorf("mlkem shared secret %d does not match Go", i)
		}
	}

	ephemeral, err := ecdh.X25519().NewPublicKey(mustB64(proto["epkPub"]))
	if err != nil {
		return fmt.Errorf("parse java ephemeral key: %w", err)
	}
	dhSecret, err := hostPrivate.ECDH(ephemeral)
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(dhSecret); got != proto["ssDH"] {
		return fmt.Errorf("x25519 shared secret: java %s, go %s", proto["ssDH"], got)
	}

	ikm := append(append([]byte{}, dhSecret...), sharedKey...)
	ts := atoi64(proto["ts"])
	if fmt.Sprint(ts) != public["timestamp"] {
		return fmt.Errorf("timestamp: java %d, generated %s", ts, public["timestamp"])
	}

	tx := transcript(txDomain, hash[:],
		[]byte(public["deviceId"]), []byte(public["requestId"]),
		u64(uint64(ts)), mustB64(proto["epkPub"]), kemCiphertext)
	keys := derive(ikm, tx)
	if got := hex.EncodeToString(tx); got != proto["requestTranscript"] {
		return fmt.Errorf("request transcript: java %s, go %s", proto["requestTranscript"], got)
	}
	if got := hex.EncodeToString(keys[0]); got != proto["c2s"] {
		return errors.New("client-to-server key mismatch")
	}
	if got := hex.EncodeToString(keys[1]); got != proto["s2c"] {
		return errors.New("server-to-client key mismatch")
	}

	material := sha256.Sum256(append(append([]byte{}, mustB64(proto["epkPub"])...), kemCiphertext...))
	ptx := transcript(pairDomain, hash[:], []byte(public["hostId"]), []byte(public["deviceId"]), material[:])
	derived := derive(ikm, ptx)
	if got := hex.EncodeToString(ptx); got != proto["pairTranscript"] {
		return fmt.Errorf("pair transcript: java %s, go %s", proto["pairTranscript"], got)
	}
	if got := hex.EncodeToString(derived[1]); got != proto["pairS2c"] {
		return errors.New("pair sealing key mismatch")
	}

	initMaterial := sha256.Sum256(append(append([]byte{}, mustB64(proto["epkPub"])...), kemCiphertext...))
	initTranscript := transcript(pairInitDomain, hash[:], []byte(public["hostId"]), initMaterial[:])
	initKey, err := hkdf.Key(sha256.New, ikm, initTranscript, infoTag, 32)
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(initTranscript); got != proto["pairInitTranscript"] {
		return fmt.Errorf("pair-init transcript: java %s, go %s", proto["pairInitTranscript"], got)
	}
	if got := hex.EncodeToString(initKey); got != proto["pairInitKey"] {
		return errors.New("pair-init sealing key mismatch")
	}

	wantReq := "req|v2|" + public["deviceId"] + "|" + public["requestId"] + "|" + fmt.Sprint(ts)
	if wantReq != proto["requestAad"] {
		return fmt.Errorf("request AAD: java %q, go %q", proto["requestAad"], wantReq)
	}
	wantResp := "resp|v2|" + public["deviceId"] + "|" + public["requestId"]
	if wantResp != proto["responseAad"] {
		return fmt.Errorf("response AAD: java %q, go %q", proto["responseAad"], wantResp)
	}
	if got := fingerprintV2(public["hostPublicKey"], suite); got != proto["fingerprintV2"] {
		return fmt.Errorf("fingerprint v2: java %s, go %s", proto["fingerprintV2"], got)
	}
	fmt.Println("pqcheck: v2 schedule agrees with Go")
	return nil
}

func derive(ikm, salt []byte) [][]byte {
	out, err := hkdf.Key(sha256.New, ikm, salt, infoTag, 64)
	if err != nil {
		panic(err)
	}
	return [][]byte{out[:32], out[32:]}
}

func transcript(domain string, bundleHash []byte, fields ...[]byte) []byte {
	digest := sha256.New()
	digest.Write([]byte(domain))
	digest.Write([]byte{0})
	digest.Write(bundleHash)
	for _, field := range fields {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		digest.Write(length[:])
		digest.Write(field)
	}
	return digest.Sum(nil)
}

func bundleCanonical(spki []byte, epoch uint32, suite string, x25519, mlkemKey []byte) []byte {
	buf := []byte(bundleTag + "\x00")
	var stamp [4]byte
	binary.BigEndian.PutUint32(stamp[:], epoch)
	buf = append(buf, stamp[:]...)
	for _, field := range [][]byte{[]byte(suite), spki, x25519, mlkemKey} {
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(field)))
		buf = append(buf, length[:]...)
		buf = append(buf, field...)
	}
	return buf
}

func fingerprintV2(publicKeyB64, suite string) string {
	digest := sha256.Sum256([]byte(pinTag + "\x00" + publicKeyB64 + "\x00" + suite))
	raw := strings.ToUpper(hex.EncodeToString(digest[:]))
	parts := make([]string, 0, 16)
	for i := 0; i < len(raw); i += 4 {
		parts = append(parts, raw[i:i+4])
	}
	return strings.Join(parts, " ")
}

func signBundle(key *ecdh.PrivateKey, payload []byte) ([]byte, error) {
	x, y := elliptic.Unmarshal(elliptic.P256(), key.PublicKey().Bytes())
	if x == nil {
		return nil, errors.New("host key is not a P-256 point")
	}
	signing := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y},
		D:         new(big.Int).SetBytes(key.Bytes()),
	}
	digest := sha256.Sum256(payload)
	return ecdsa.SignASN1(rand.Reader, signing, digest[:])
}

func x509MarshalPublic(key *ecdh.PublicKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(key)
}

func sum256(input []byte) []byte {
	value := sha3.Sum256(input)
	return value[:]
}

func sum512(input []byte) []byte {
	value := sha3.Sum512(input)
	return value[:]
}

func pattern(length int) []byte {
	out := make([]byte, length)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}

func u64(value uint64) []byte {
	var out [8]byte
	binary.BigEndian.PutUint64(out[:], value)
	return out[:]
}

func writeProps(path string, values map[string]string) error {
	var builder strings.Builder
	for key, value := range values {
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(value)
		builder.WriteString("\n")
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

func readProps(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s: malformed line %q", path, line)
		}
		values[key] = value
	}
	return values, nil
}

func mustHex(value string) []byte {
	raw, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func mustB64(value string) []byte {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func atoi(value string) int {
	var out int
	if _, err := fmt.Sscan(value, &out); err != nil {
		panic(err)
	}
	return out
}

func atoi64(value string) int64 {
	var out int64
	if _, err := fmt.Sscan(value, &out); err != nil {
		panic(err)
	}
	return out
}
