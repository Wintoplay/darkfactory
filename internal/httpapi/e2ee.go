package httpapi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wintoplay/darkfactory/internal/store"
)

const (
	e2eeVersion   = 1
	e2eeVersionV2 = 2
	e2eeSuiteV2   = "x25519-mlkem768-v1"
	e2eeKeyInfo   = "darkfactory-e2ee-v1"
	e2eeKeyInfoV2 = "darkfactory-e2ee-v2"
	// The transcript domain is separate from the HKDF label so the same bytes
	// cannot be reused as both a salt and an output context.
	e2eeTxDomainV2   = "darkfactory-e2ee-transcript-v2"
	e2eePairDomainV2 = "darkfactory-e2ee-pair-v2"

	e2eeClockSkew = 5 * time.Minute
	// e2eeMaxBody bounds the encrypted envelope, not the inner request. Inner
	// handlers cap their own bodies at 1 MiB, and base64 plus JSON overhead
	// multiplies that on the way in, so 4 MiB leaves headroom without buffering
	// an unauthenticated 16 MiB per connection.
	e2eeMaxBody        = 4 << 20
	e2eeResponseType   = "application/x-df-e2ee"
	e2eeResponseHeader = "X-DF-E2EE"

	e2eeX25519Size          = 32
	e2eeMLKEMPublicSize     = 1184
	e2eeMLKEMCiphertextSize = 1088
	e2eeIdentityVersion     = 2
)

// e2eeInnerContextKey marks a request that already entered through the envelope
// route. The path guard alone is not enough: a future spelling or route change
// must not be able to nest one envelope inside another.
type e2eeInnerContextKey struct{}

// e2eeState is the daemon's long-lived identity. The P-256 half is the pin and
// the bundle-signing key; the X25519 and ML-KEM halves make the v2 suite
// hybrid. The private halves live only in state.db (or in memory for a
// store-less test server). Clients pin the P-256 public key, so an unreadable
// identity must not silently become a new one.
type e2eeState struct {
	private     *ecdh.PrivateKey
	publicB64   string
	fingerprint string

	x25519        *ecdh.PrivateKey
	mlkem         *mlkem.DecapsulationKey768
	bundle        e2eeBundle
	bundleHash    [32]byte
	fingerprintV2 string
}

// e2eeIdentityFile is the persisted form of e2eeState. A store written before
// the v2 suite holds a bare PKCS#8 P-256 key; loadOrCreateE2EE upgrades that in
// place without changing the pinned public key.
type e2eeIdentityFile struct {
	V      int    `json:"v"`
	Epoch  uint32 `json:"epoch"`
	P256   string `json:"p256"`
	X25519 string `json:"x25519"`
	MLKEM  string `json:"mlkem"`
}

// e2eeBundle is the public, signed key material the host publishes. The
// signature is over e2eeBundle.canonical; the P-256 key is the trust anchor,
// so rotating the X25519 or ML-KEM half does not change the v2 pin.
type e2eeBundle struct {
	V      int    `json:"v"`
	Epoch  uint32 `json:"epoch"`
	Suite  string `json:"suite"`
	P256   string `json:"p256"`
	X25519 string `json:"x25519"`
	MLKEM  string `json:"mlkem"`
	Sig    string `json:"sig,omitempty"`
}

type storedE2EEIdentity struct {
	p256   *ecdh.PrivateKey
	x25519 *ecdh.PrivateKey
	mlkem  *mlkem.DecapsulationKey768
	epoch  uint32
}

func loadOrCreateE2EE(st *store.Store) (*e2eeState, error) {
	var raw []byte
	if st != nil {
		saved, err := st.GetE2EEHostKey()
		switch {
		case err == nil:
			if len(saved) == 0 {
				return nil, errors.New("persisted host e2ee identity is empty")
			}
			raw = saved
		case errors.Is(err, store.ErrNotFound):
			// Create below.
		default:
			return nil, fmt.Errorf("read host e2ee key: %w", err)
		}
	}

	var (
		p256Key   *ecdh.PrivateKey
		x25519Key *ecdh.PrivateKey
		kemKey    *mlkem.DecapsulationKey768
		epoch     uint32 = 1
		changed   bool
	)
	if len(raw) > 0 {
		identity, ok, err := parseE2EEIdentityFile(raw)
		if err != nil {
			return nil, err
		}
		if ok {
			p256Key, x25519Key, kemKey, epoch = identity.p256, identity.x25519, identity.mlkem, identity.epoch
		} else {
			p256Key, err = parseHostP256(raw)
			if err != nil {
				return nil, err
			}
			changed = true
		}
	} else {
		changed = true
	}

	var err error
	if p256Key == nil {
		p256Key, err = ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate host e2ee key: %w", err)
		}
		changed = true
	}
	if x25519Key == nil {
		x25519Key, err = ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate host x25519 key: %w", err)
		}
		changed = true
	}
	if kemKey == nil {
		kemKey, err = mlkem.GenerateKey768()
		if err != nil {
			return nil, fmt.Errorf("generate host mlkem key: %w", err)
		}
		changed = true
	}
	if st != nil && changed {
		if err := storeE2EEIdentity(st, p256Key, x25519Key, kemKey, epoch); err != nil {
			return nil, err
		}
	}
	return newE2EEState(p256Key, x25519Key, kemKey, epoch)
}

// parseE2EEIdentityFile distinguishes the JSON identity file from a legacy
// PKCS#8 blob. A malformed JSON file fails closed instead of being retried as
// PKCS#8, so a damaged state.db cannot silently mint a new identity.
func parseE2EEIdentityFile(raw []byte) (storedE2EEIdentity, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return storedE2EEIdentity{}, false, nil
	}
	var file e2eeIdentityFile
	if err := decodeStrictJSON(trimmed, maxBody, &file); err != nil {
		return storedE2EEIdentity{}, false, fmt.Errorf("parse host e2ee identity: %w", err)
	}
	if file.V != e2eeIdentityVersion {
		return storedE2EEIdentity{}, false, fmt.Errorf("unsupported host e2ee identity version %d", file.V)
	}
	if file.Epoch == 0 {
		return storedE2EEIdentity{}, false, errors.New("host e2ee identity epoch must be nonzero")
	}
	p256DER, err := base64.StdEncoding.DecodeString(file.P256)
	if err != nil {
		return storedE2EEIdentity{}, false, fmt.Errorf("decode host e2ee key: %w", err)
	}
	p256Key, err := parseHostP256(p256DER)
	if err != nil {
		return storedE2EEIdentity{}, false, err
	}
	xRaw, err := base64.StdEncoding.DecodeString(file.X25519)
	if err != nil || len(xRaw) != e2eeX25519Size {
		return storedE2EEIdentity{}, false, errors.New("decode host x25519 key")
	}
	x25519Key, err := ecdh.X25519().NewPrivateKey(xRaw)
	if err != nil {
		return storedE2EEIdentity{}, false, fmt.Errorf("parse host x25519 key: %w", err)
	}
	seed, err := base64.StdEncoding.DecodeString(file.MLKEM)
	if err != nil {
		return storedE2EEIdentity{}, false, fmt.Errorf("decode host mlkem key: %w", err)
	}
	kemKey, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		return storedE2EEIdentity{}, false, fmt.Errorf("parse host mlkem key: %w", err)
	}
	return storedE2EEIdentity{p256: p256Key, x25519: x25519Key, mlkem: kemKey, epoch: file.Epoch}, true, nil
}

func parseHostP256(raw []byte) (*ecdh.PrivateKey, error) {
	parsed, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse host e2ee key: %w", err)
	}
	switch key := parsed.(type) {
	case *ecdh.PrivateKey:
		if key.Curve() != ecdh.P256() {
			return nil, errors.New("host e2ee key is not P-256 ECDH")
		}
		return key, nil
	case *ecdsa.PrivateKey:
		// x509 reports a PKCS#8 EC key as ECDSA even when it was written from
		// an ECDH key. The scalar is the same; convert it once.
		if key.Curve != elliptic.P256() {
			return nil, errors.New("host e2ee key is not P-256")
		}
		private, err := ecdh.P256().NewPrivateKey(key.D.FillBytes(make([]byte, 32)))
		if err != nil {
			return nil, fmt.Errorf("convert host e2ee key: %w", err)
		}
		return private, nil
	default:
		return nil, errors.New("host e2ee key is not P-256 ECDH")
	}
}

func storeE2EEIdentity(st *store.Store, p256Key *ecdh.PrivateKey, x25519Key *ecdh.PrivateKey, kemKey *mlkem.DecapsulationKey768, epoch uint32) error {
	der, err := x509.MarshalPKCS8PrivateKey(p256Key)
	if err != nil {
		return fmt.Errorf("encode host e2ee key: %w", err)
	}
	raw, err := json.Marshal(e2eeIdentityFile{
		V:      e2eeIdentityVersion,
		Epoch:  epoch,
		P256:   base64.StdEncoding.EncodeToString(der),
		X25519: base64.StdEncoding.EncodeToString(x25519Key.Bytes()),
		MLKEM:  base64.StdEncoding.EncodeToString(kemKey.Bytes()),
	})
	if err != nil {
		return fmt.Errorf("encode host e2ee identity: %w", err)
	}
	if err := st.PutE2EEHostKey(raw); err != nil {
		return fmt.Errorf("store host e2ee key: %w", err)
	}
	return nil
}

func newE2EEState(p256Key *ecdh.PrivateKey, x25519Key *ecdh.PrivateKey, kemKey *mlkem.DecapsulationKey768, epoch uint32) (*e2eeState, error) {
	publicDER, err := x509.MarshalPKIXPublicKey(p256Key.PublicKey())
	if err != nil {
		return nil, fmt.Errorf("encode host e2ee public key: %w", err)
	}
	sum := sha256.Sum256(publicDER)
	state := &e2eeState{
		private:     p256Key,
		publicB64:   base64.StdEncoding.EncodeToString(publicDER),
		fingerprint: fingerprint(sum[:]),
		x25519:      x25519Key,
		mlkem:       kemKey,
	}
	state.bundle = e2eeBundle{
		V:      e2eeVersionV2,
		Epoch:  epoch,
		Suite:  e2eeSuiteV2,
		P256:   state.publicB64,
		X25519: base64.StdEncoding.EncodeToString(x25519Key.PublicKey().Bytes()),
		MLKEM:  base64.StdEncoding.EncodeToString(kemKey.EncapsulationKey().Bytes()),
	}
	payload, err := state.bundle.canonical()
	if err != nil {
		return nil, err
	}
	signature, err := signE2EEBundle(p256Key, payload)
	if err != nil {
		return nil, fmt.Errorf("sign host e2ee bundle: %w", err)
	}
	state.bundle.Sig = base64.StdEncoding.EncodeToString(signature)
	state.bundleHash = sha256.Sum256(payload)
	// The v2 pin covers the identity and the suite, not the rotating key
	// halves: a rotation under the same P-256 key must not look like a new
	// host, and a pin must not silently accept a lower suite. A future rotation
	// also has to retain the previous bundle hash for the grace window, because
	// that hash is part of the per-request transcript.
	fpV2 := sha256.Sum256([]byte("df-e2ee-v2\x00" + state.publicB64 + "\x00" + e2eeSuiteV2))
	state.fingerprintV2 = fingerprint(fpV2[:])
	return state, nil
}

// ecdsaKeyFromECDH reuses the P-256 scalar for signing. The public point is
// the same curve point, so the PKCS#8 identity stays a single long-term key.
func ecdsaKeyFromECDH(private *ecdh.PrivateKey) (*ecdsa.PrivateKey, error) {
	x, y := elliptic.Unmarshal(elliptic.P256(), private.PublicKey().Bytes())
	if x == nil {
		return nil, errors.New("host e2ee key is not a P-256 point")
	}
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y},
		D:         new(big.Int).SetBytes(private.Bytes()),
	}, nil
}

func signE2EEBundle(private *ecdh.PrivateKey, payload []byte) ([]byte, error) {
	key, err := ecdsaKeyFromECDH(private)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	return ecdsa.SignASN1(rand.Reader, key, digest[:])
}

func verifyE2EEBundle(bundle e2eeBundle) error {
	payload, err := bundle.canonical()
	if err != nil {
		return err
	}
	der, err := base64.StdEncoding.DecodeString(bundle.P256)
	if err != nil {
		return errors.New("host bundle key is not base64")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return fmt.Errorf("parse host bundle key: %w", err)
	}
	var public *ecdsa.PublicKey
	switch key := parsed.(type) {
	case *ecdsa.PublicKey:
		public = key
	case *ecdh.PublicKey:
		x, y := elliptic.Unmarshal(elliptic.P256(), key.Bytes())
		if x == nil {
			return errors.New("host bundle key is not a P-256 point")
		}
		public = &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	default:
		return errors.New("host bundle key is not P-256")
	}
	signature, err := base64.StdEncoding.DecodeString(bundle.Sig)
	if err != nil {
		return errors.New("host bundle signature is not base64")
	}
	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(public, digest[:], signature) {
		return errors.New("host bundle signature did not verify")
	}
	return nil
}

// canonical is the signed byte string. Every variable-length field is
// length-prefixed so one field cannot be shifted into another.
func (b e2eeBundle) canonical() ([]byte, error) {
	der, err := base64.StdEncoding.DecodeString(b.P256)
	if err != nil || len(der) == 0 {
		return nil, errors.New("host bundle p256 is not base64")
	}
	x25519, err := base64.StdEncoding.DecodeString(b.X25519)
	if err != nil || len(x25519) != e2eeX25519Size {
		return nil, errors.New("host bundle x25519 is not 32 bytes")
	}
	kem, err := base64.StdEncoding.DecodeString(b.MLKEM)
	if err != nil || len(kem) != e2eeMLKEMPublicSize {
		return nil, errors.New("host bundle mlkem is not an ML-KEM-768 key")
	}
	if b.V != e2eeVersionV2 || b.Suite != e2eeSuiteV2 || b.Epoch == 0 {
		return nil, errors.New("host bundle version, suite, or epoch is unsupported")
	}
	var buf bytes.Buffer
	buf.WriteString("darkfactory-e2ee-bundle-v2\x00")
	var epoch [4]byte
	binary.BigEndian.PutUint32(epoch[:], b.Epoch)
	buf.Write(epoch[:])
	writeLengthPrefixed16(&buf, []byte(b.Suite))
	writeLengthPrefixed16(&buf, der)
	writeLengthPrefixed16(&buf, x25519)
	writeLengthPrefixed16(&buf, kem)
	return buf.Bytes(), nil
}

func writeLengthPrefixed16(buf *bytes.Buffer, field []byte) {
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(field)))
	buf.Write(length[:])
	buf.Write(field)
}

func fingerprint(sum []byte) string {
	raw := strings.ToUpper(hex.EncodeToString(sum))
	parts := make([]string, 0, len(raw)/4)
	for i := 0; i < len(raw); i += 4 {
		end := i + 4
		if end > len(raw) {
			end = len(raw)
		}
		parts = append(parts, raw[i:end])
	}
	return strings.Join(parts, " ")
}

func parseClientE2EEKey(encoded string) (*ecdh.PublicKey, string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, "", fmt.Errorf("clientPublicKey must be base64 SPKI")
	}
	parsed, err := x509.ParsePKIXPublicKey(raw)
	if err != nil {
		return nil, "", fmt.Errorf("clientPublicKey must be a P-256 SPKI key")
	}
	var public *ecdh.PublicKey
	switch key := parsed.(type) {
	case *ecdh.PublicKey:
		if key.Curve() != ecdh.P256() {
			return nil, "", errors.New("clientPublicKey must be a P-256 ECDH key")
		}
		public = key
	case *ecdsa.PublicKey:
		if key.Curve != elliptic.P256() {
			return nil, "", errors.New("clientPublicKey must be a P-256 ECDH key")
		}
		public, err = ecdh.P256().NewPublicKey(elliptic.Marshal(elliptic.P256(), key.X, key.Y))
		if err != nil {
			return nil, "", fmt.Errorf("clientPublicKey must be a P-256 ECDH key")
		}
	default:
		return nil, "", errors.New("clientPublicKey must be a P-256 ECDH key")
	}
	return public, base64.StdEncoding.EncodeToString(raw), nil
}

func deriveE2EEKeys(host *ecdh.PrivateKey, client *ecdh.PublicKey, hostB64, clientB64 string) ([]byte, []byte, error) {
	shared, err := host.ECDH(client)
	if err != nil {
		return nil, nil, fmt.Errorf("derive e2ee secret: %w", err)
	}
	salt := sha256.Sum256([]byte(hostB64 + "|" + clientB64))
	derived, err := hkdf.Key(sha256.New, shared, salt[:], e2eeKeyInfo, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("derive e2ee keys: %w", err)
	}
	return derived[:32], derived[32:], nil
}

// minSuite is host policy. A zero value (a Server built directly in a test)
// behaves like suite 1.
func (s *Server) minSuite() int {
	if s.cfg.E2EEMinSuite >= e2eeVersionV2 {
		return e2eeVersionV2
	}
	return e2eeVersion
}

func parseE2EEV2PairMaterial(epkB64, kemB64 string) ([]byte, []byte, error) {
	epk, err := base64.StdEncoding.DecodeString(strings.TrimSpace(epkB64))
	if err != nil || len(epk) != e2eeX25519Size {
		return nil, nil, errors.New("epk must be a base64 32-byte X25519 public key")
	}
	kem, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kemB64))
	if err != nil || len(kem) != e2eeMLKEMCiphertextSize {
		return nil, nil, errors.New("kem must be a base64 ML-KEM-768 ciphertext")
	}
	return epk, kem, nil
}

func decodeStrictJSON(raw []byte, limit int64, out any) error {
	if int64(len(raw)) > limit {
		return errors.New("JSON body exceeds size limit")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func parseE2EEEnvelope(raw []byte) (e2eeEnvelope, error) {
	var envelope e2eeEnvelope
	if err := decodeStrictJSON(raw, e2eeMaxBody, &envelope); err != nil {
		return e2eeEnvelope{}, err
	}
	if envelope.V != e2eeVersion && envelope.V != e2eeVersionV2 {
		return e2eeEnvelope{}, errors.New("unsupported e2ee envelope version")
	}
	if envelope.DeviceID == "" || envelope.ID == "" || envelope.TS == 0 {
		return e2eeEnvelope{}, errors.New("incomplete e2ee envelope")
	}
	return envelope, nil
}

// e2eeV2Secret combines the classical and post-quantum halves. Both are
// required: no code path uses only one, so a broken or stripped algorithm
// cannot degrade the result.
func e2eeV2Secret(state *e2eeState, epkRaw, kemCT []byte) ([]byte, error) {
	if state == nil || state.x25519 == nil || state.mlkem == nil {
		return nil, errors.New("host e2ee v2 suite is unavailable")
	}
	epk, err := ecdh.X25519().NewPublicKey(epkRaw)
	if err != nil {
		return nil, errors.New("epk is not a valid X25519 public key")
	}
	ssDH, err := state.x25519.ECDH(epk)
	if err != nil {
		return nil, fmt.Errorf("derive e2ee x25519: %w", err)
	}
	ssKEM, err := state.mlkem.Decapsulate(kemCT)
	if err != nil {
		return nil, fmt.Errorf("derive e2ee mlkem: %w", err)
	}
	ikm := make([]byte, 0, len(ssDH)+len(ssKEM))
	ikm = append(ikm, ssDH...)
	ikm = append(ikm, ssKEM...)
	return ikm, nil
}

// e2eeV2Transcript hashes a domain-separated, length-prefixed transcript. The
// result is the HKDF salt, so every derived key is bound to the suite, the
// bundle, the device and the request identity.
func e2eeV2Transcript(domain string, bundleHash []byte, fields ...[]byte) []byte {
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

func deriveE2EEKeysV2(state *e2eeState, deviceID, id string, ts int64, epkB64, kemB64 string) ([]byte, []byte, error) {
	epk, err := base64.StdEncoding.DecodeString(strings.TrimSpace(epkB64))
	if err != nil || len(epk) != e2eeX25519Size {
		return nil, nil, errors.New("epk must be a base64 32-byte X25519 public key")
	}
	kem, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kemB64))
	if err != nil || len(kem) != e2eeMLKEMCiphertextSize {
		return nil, nil, errors.New("kem must be a base64 ML-KEM-768 ciphertext")
	}
	ikm, err := e2eeV2Secret(state, epk, kem)
	if err != nil {
		return nil, nil, err
	}
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ts))
	transcript := e2eeV2Transcript(e2eeTxDomainV2, state.bundleHash[:], []byte(deviceID), []byte(id), stamp[:], epk, kem)
	derived, err := hkdf.Key(sha256.New, ikm, transcript, e2eeKeyInfoV2, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("derive e2ee v2 keys: %w", err)
	}
	return derived[:32], derived[32:], nil
}

// deriveE2EEPairKeysV2 derives the pair-token keys and returns the transcript,
// which is also the AAD that binds the sealed payload to this key exchange.
func deriveE2EEPairKeysV2(state *e2eeState, hostID, deviceID string, epk, kemCT []byte) ([]byte, []byte, []byte, error) {
	ikm, err := e2eeV2Secret(state, epk, kemCT)
	if err != nil {
		return nil, nil, nil, err
	}
	material := sha256.Sum256(append(append([]byte{}, epk...), kemCT...))
	transcript := e2eeV2Transcript(e2eePairDomainV2, state.bundleHash[:], []byte(hostID), []byte(deviceID), material[:])
	derived, err := hkdf.Key(sha256.New, ikm, transcript, e2eeKeyInfoV2, 64)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("derive e2ee pair keys: %w", err)
	}
	return derived[:32], derived[32:], transcript, nil
}

func deriveE2EEPairInitKeyV2(state *e2eeState, hostID string, epk, kemCT []byte) ([]byte, []byte, error) {
	ikm, err := e2eeV2Secret(state, epk, kemCT)
	if err != nil {
		return nil, nil, err
	}
	material := sha256.Sum256(append(append([]byte{}, epk...), kemCT...))
	transcript := e2eeV2Transcript("darkfactory-e2ee-pair-init-v2", state.bundleHash[:], []byte(hostID), material[:])
	key, err := hkdf.Key(sha256.New, ikm, transcript, e2eeKeyInfoV2, 32)
	if err != nil {
		return nil, nil, fmt.Errorf("derive e2ee pair-init key: %w", err)
	}
	return key, transcript, nil
}

func openE2EEPairCodeV2(state *e2eeState, hostID string, epk, kemCT []byte, sealedCode string) (string, error) {
	key, transcript, err := deriveE2EEPairInitKeyV2(state, hostID, epk, kemCT)
	if err != nil {
		return "", err
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sealedCode))
	if err != nil {
		return "", errors.New("sealedCode must be base64 nonce and ciphertext")
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead()+1 {
		return "", errors.New("sealedCode is too short")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], transcript)
	if err != nil || len(plain) == 0 || len(plain) > 64 {
		return "", errors.New("sealedCode did not authenticate")
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealE2EE(key, plaintext, aad []byte) (nonceB64, cipherB64 string, err error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", "", err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	return base64.StdEncoding.EncodeToString(nonce), base64.StdEncoding.EncodeToString(sealed), nil
}

func sealE2EECombined(key, plaintext, aad []byte) (string, error) {
	nonce, sealed, err := sealE2EE(key, plaintext, aad)
	if err != nil {
		return "", err
	}
	nonceRaw, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return "", err
	}
	sealedRaw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(nonceRaw, sealedRaw...)), nil
}

func openE2EE(key []byte, nonceB64, cipherB64 string, aad []byte) ([]byte, error) {
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return nil, errors.New("invalid nonce")
	}
	sealed, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return nil, errors.New("invalid ciphertext")
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid nonce length")
	}
	plain, err := gcm.Open(nil, nonce, sealed, aad)
	if err != nil {
		return nil, errors.New("e2ee ciphertext did not authenticate")
	}
	return plain, nil
}

func e2eeAAD(parts ...string) []byte {
	return []byte(strings.Join(parts, "|"))
}

type e2eeEnvelope struct {
	V          int    `json:"v"`
	DeviceID   string `json:"deviceId"`
	ID         string `json:"id"`
	TS         int64  `json:"ts"`
	Suite      string `json:"suite,omitempty"`
	Epk        string `json:"epk,omitempty"`
	KEM        string `json:"kem,omitempty"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// e2eeSealedPayload is the v2 pair result. The token, the device id and the
// accepted suite all travel inside the AEAD, so a rewritten response cannot
// downgrade the negotiation without failing the open.
type e2eeSealedPayload struct {
	Token     string    `json:"token"`
	DeviceID  string    `json:"deviceId"`
	HostID    string    `json:"hostId"`
	Suite     string    `json:"suite"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type e2eeRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
	Token   string            `json:"token"`
}

type e2eeResponseHead struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (s *Server) handleHostKey(w http.ResponseWriter, r *http.Request) {
	if s.e2ee == nil {
		s.refuse(w, r, codeE2EEUnavailable, "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"v":             e2eeVersion,
		"maxVersion":    e2eeVersionV2,
		"suite":         e2eeSuiteV2,
		"hostId":        s.hostID(),
		"publicKey":     s.e2ee.publicB64,
		"fingerprint":   s.e2ee.fingerprint,
		"fingerprintV2": s.e2ee.fingerprintV2,
		"bundle":        s.e2ee.bundle,
	})
}

// browserByID finds the hashed row key for a public device id. The caller has
// already authenticated the envelope; this only selects the E2EE key.
func (s *Server) browserByID(id string) (store.Browser, string, bool) {
	s.openMu.Lock()
	defer s.openMu.Unlock()
	for key, browser := range s.browsers {
		if browser.ID == id {
			return browser, key, true
		}
	}
	return store.Browser{}, "", false
}

func (s *Server) consumeE2EEReplay(deviceID, id string, now, expires time.Time) (bool, error) {
	if s.store != nil {
		return s.store.ConsumeE2EERequest(deviceID, id, now, expires)
	}
	s.e2eeMu.Lock()
	defer s.e2eeMu.Unlock()
	for key, at := range s.e2eeReplay {
		if now.After(at) {
			delete(s.e2eeReplay, key)
		}
	}
	key := deviceID + "\x00" + id
	if _, seen := s.e2eeReplay[key]; seen {
		return false, nil
	}
	s.e2eeReplay[key] = expires
	return true, nil
}

func (s *Server) handleE2EE(w http.ResponseWriter, r *http.Request) {
	if !s.allowE2EE(r) {
		s.refuse(w, r, codeE2EERateLimited, "")
		return
	}
	if s.e2ee == nil {
		s.refuse(w, r, codeE2EEUnavailable, "")
		return
	}
	// The envelope can carry a terminal or event stream, whose response
	// outlives the listener's write deadline. The request body is read in full
	// below, so clearing the read deadline is safe too.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	if r.Context().Value(e2eeInnerContextKey{}) != nil {
		s.refuse(w, r, codeInvalidRequest, "e2ee requests cannot be nested")
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		s.refuse(w, r, codeInvalidRequest, "send an application/json envelope")
		return
	}
	if r.ContentLength > e2eeMaxBody {
		s.refuse(w, r, codeRequestBodyTooLarge, "")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, e2eeMaxBody+1))
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, "read envelope failed")
		return
	}
	if len(raw) > e2eeMaxBody {
		s.refuse(w, r, codeRequestBodyTooLarge, "")
		return
	}
	envelope, err := parseE2EEEnvelope(raw)
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, "envelope must be JSON")
		return
	}
	now := time.Now()
	at := time.UnixMilli(envelope.TS)
	if now.Sub(at) > e2eeClockSkew || at.Sub(now) > e2eeClockSkew {
		s.refuse(w, r, codeInvalidRequest, "e2ee envelope is outside the clock window")
		return
	}

	browser, deviceKey, ok := s.browserByID(envelope.DeviceID)
	if !ok {
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	var (
		c2s, s2c     []byte
		reqAAD       []byte
		respAAD      []byte
		version      int
		clientPublic *ecdh.PublicKey
		clientB64    string
	)
	switch browser.E2EESuite {
	case e2eeSuiteV2:
		if envelope.V != e2eeVersionV2 || envelope.Suite != e2eeSuiteV2 {
			s.refuse(w, r, codeE2EESuiteDowngrade, "")
			return
		}
		c2s, s2c, err = deriveE2EEKeysV2(s.e2ee, browser.ID, envelope.ID, envelope.TS, envelope.Epk, envelope.KEM)
		if err != nil {
			// Bad ephemeral material is client-caused; a missing host key is
			// already handled above, so this is a refusal, not a 500.
			s.refuse(w, r, codeInvalidRequest, "e2ee envelope did not authenticate")
			return
		}
		version = e2eeVersionV2
		reqAAD = e2eeAAD("req", "v2", browser.ID, envelope.ID, strconv.FormatInt(envelope.TS, 10))
		respAAD = e2eeAAD("resp", "v2", browser.ID, envelope.ID)
	case "":
		if envelope.V != e2eeVersion || envelope.Suite != "" {
			s.refuse(w, r, codeE2EESuiteDowngrade, "")
			return
		}
		if browser.E2EEPublicKey == "" {
			s.refuse(w, r, codeUnauthorized, "")
			return
		}
		// The policy also covers devices paired before it was raised: a
		// legacy row cannot keep using the weaker suite against a v2 host.
		if s.minSuite() >= e2eeVersionV2 {
			s.refuse(w, r, codeE2EESuiteDowngrade, "")
			return
		}
		clientPublic, clientB64, err = parseClientE2EEKey(browser.E2EEPublicKey)
		if err != nil {
			s.refuse(w, r, codeInvalidRequest, "device has no usable e2ee key")
			return
		}
		c2s, s2c, err = deriveE2EEKeys(s.e2ee.private, clientPublic, s.e2ee.publicB64, clientB64)
		version = e2eeVersion
		reqAAD = e2eeAAD("req", browser.ID, envelope.ID, strconv.FormatInt(envelope.TS, 10))
		respAAD = e2eeAAD("resp", browser.ID, envelope.ID)
	default:
		s.refuse(w, r, codeE2EESuiteDowngrade, "")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	plain, err := openE2EE(c2s, envelope.Nonce, envelope.Ciphertext, reqAAD)
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, "e2ee envelope did not authenticate")
		return
	}
	var request e2eeRequest
	if err := json.Unmarshal(plain, &request); err != nil {
		s.refuse(w, r, codeInvalidRequest, "e2ee request must be JSON")
		return
	}
	if request.Token == "" || !secureEqual(s.hashSecret(request.Token), deviceKey) {
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	accepted, err := s.consumeE2EEReplay(envelope.DeviceID, envelope.ID, now, at.Add(e2eeClockSkew))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !accepted {
		s.refuse(w, r, codeInvalidRequest, "e2ee envelope was already used")
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead &&
		request.Method != http.MethodPost && request.Method != http.MethodDelete {
		s.refuse(w, r, codeMethodNotAllowed, "")
		return
	}
	// Validate the decoded path, not the raw string: url.ParseRequestURI and
	// ServeMux both work on URL.Path, so "/api/v1/%652ee" would otherwise reach
	// this handler again and nest envelopes. Only origin-form paths are valid.
	target, err := url.ParseRequestURI(request.Path)
	if err != nil || target.Scheme != "" || target.Host != "" ||
		!strings.HasPrefix(target.Path, "/api/v1/") ||
		target.Path == "/api/v1/e2ee" || strings.HasPrefix(target.Path, "/api/v1/e2ee/") {
		s.refuse(w, r, codeInvalidRequest, "e2ee path is not an API route")
		return
	}

	ctx := context.WithValue(r.Context(), e2eeInnerContextKey{}, true)
	inner, err := http.NewRequestWithContext(ctx, request.Method, target.RequestURI(), bytes.NewReader(request.Body))
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, "e2ee request is not valid")
		return
	}
	inner.Host = r.Host
	inner.RemoteAddr = r.RemoteAddr
	inner.TLS = r.TLS
	inner.Header.Set("Authorization", "Bearer "+request.Token)
	for name, value := range request.Headers {
		switch strings.ToLower(name) {
		case "accept", "content-type", "last-event-id", "x-df-request", "origin", "user-agent":
			inner.Header.Set(name, value)
		}
	}

	writer := &e2eeResponseWriter{
		outer:   w,
		key:     s2c,
		aad:     respAAD,
		version: version,
	}
	s.Handler().ServeHTTP(writer, inner)
	if inner.Context().Err() == nil {
		writer.finish()
	}
}

// e2eeResponseWriter buffers the inner response head, then emits encrypted
// frames. Frame 0 is the head; frames 1..n are body bytes. The outer HTTP
// response is always a 200 chunked stream so status and headers cannot leak.
type e2eeResponseWriter struct {
	outer    http.ResponseWriter
	key      []byte
	aad      []byte
	version  int
	header   http.Header
	status   int
	frame    int
	headSent bool
	finished bool
	writeErr error
}

func (w *e2eeResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *e2eeResponseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *e2eeResponseWriter) Write(p []byte) (int, error) {
	if w.finished {
		return 0, errors.New("e2ee response already finished")
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if err := w.writeHead(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if err := w.writeFrame(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *e2eeResponseWriter) Flush() {
	if w.finished {
		return
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if err := w.writeHead(); err == nil {
		_ = http.NewResponseController(w.outer).Flush()
	} else {
		w.writeErr = err
	}
}

func (w *e2eeResponseWriter) finish() {
	if w.finished {
		return
	}
	w.finished = true
	if w.writeErr != nil {
		return
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if err := w.writeHead(); err != nil {
		w.writeErr = err
		return
	}
	if w.version == e2eeVersionV2 {
		endAAD := []byte(fmt.Sprintf("%s|%d|end", w.aad, w.frame))
		if err := w.writeFrameWithAAD([]byte("DFE2EE-END-v2"), endAAD); err != nil {
			w.writeErr = err
			return
		}
	}
	_ = http.NewResponseController(w.outer).Flush()
}

func (w *e2eeResponseWriter) writeHead() error {
	if w.headSent {
		return nil
	}
	version := w.version
	if version == 0 {
		version = e2eeVersion
	}
	w.outer.Header().Set("Content-Type", fmt.Sprintf("%s; v=%d", e2eeResponseType, version))
	w.outer.Header().Set("Cache-Control", "no-store")
	w.outer.Header().Set("X-Content-Type-Options", "nosniff")
	w.outer.Header().Set(e2eeResponseHeader, strconv.Itoa(version))
	w.outer.WriteHeader(http.StatusOK)

	headers := make(map[string]string)
	for name, values := range w.header {
		if len(values) == 0 {
			continue
		}
		switch strings.ToLower(name) {
		case "set-cookie", "content-length", "transfer-encoding", "connection":
			continue
		}
		headers[name] = strings.Join(values, ",")
	}
	body, err := json.Marshal(e2eeResponseHead{Status: w.status, Headers: headers})
	if err != nil {
		return err
	}
	if err := w.writeFrame(body); err != nil {
		w.writeErr = err
		return err
	}
	w.headSent = true
	return nil
}

func (w *e2eeResponseWriter) writeFrame(plain []byte) error {
	aad := []byte(fmt.Sprintf("%s|%d", string(w.aad), w.frame))
	return w.writeFrameWithAAD(plain, aad)
}

func (w *e2eeResponseWriter) writeFrameWithAAD(plain, aad []byte) error {
	gcm, err := newGCM(w.key)
	if err != nil {
		w.writeErr = err
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		w.writeErr = err
		return err
	}
	sealed := gcm.Seal(nil, nonce, plain, aad)
	frame := make([]byte, 4+len(nonce)+len(sealed))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(nonce)+len(sealed)))
	copy(frame[4:], nonce)
	copy(frame[4+len(nonce):], sealed)
	if _, err := w.outer.Write(frame); err != nil {
		w.writeErr = err
		return err
	}
	w.frame++
	return nil
}
