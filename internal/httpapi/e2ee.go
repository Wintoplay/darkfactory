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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wintoplay/darkfactory/internal/store"
)

const (
	e2eeVersion   = 1
	e2eeKeyInfo   = "darkfactory-e2ee-v1"
	e2eeClockSkew = 5 * time.Minute
	// e2eeMaxBody bounds the encrypted envelope, not the inner request. Inner
	// handlers cap their own bodies at 1 MiB, and base64 plus JSON overhead
	// multiplies that on the way in, so 4 MiB leaves headroom without buffering
	// an unauthenticated 16 MiB per connection.
	e2eeMaxBody        = 4 << 20
	e2eeResponseType   = "application/x-df-e2ee"
	e2eeResponseHeader = "X-DF-E2EE"
)

// e2eeInnerContextKey marks a request that already entered through the envelope
// route. The path guard alone is not enough: a future spelling or route change
// must not be able to nest one envelope inside another.
type e2eeInnerContextKey struct{}

// e2eeState is the daemon's long-lived P-256 identity. The private half lives
// only in state.db (or in memory for a store-less test server). Clients pin the
// public half, so an unreadable key must not silently become a new identity.
type e2eeState struct {
	private     *ecdh.PrivateKey
	publicB64   string
	fingerprint string
}

func loadOrCreateE2EE(st *store.Store) (*e2eeState, error) {
	var raw []byte
	if st != nil {
		saved, err := st.GetE2EEHostKey()
		switch {
		case err == nil:
			raw = saved
		case errors.Is(err, store.ErrNotFound):
			// Create below.
		default:
			return nil, fmt.Errorf("read host e2ee key: %w", err)
		}
	}

	var private *ecdh.PrivateKey
	if len(raw) > 0 {
		parsed, err := x509.ParsePKCS8PrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("parse host e2ee key: %w", err)
		}
		switch key := parsed.(type) {
		case *ecdh.PrivateKey:
			if key.Curve() != ecdh.P256() {
				return nil, errors.New("host e2ee key is not P-256 ECDH")
			}
			private = key
		case *ecdsa.PrivateKey:
			// x509 reports a PKCS#8 EC key as ECDSA even when it was written
			// from an ECDH key. The scalar is the same; convert it once.
			if key.Curve != elliptic.P256() {
				return nil, errors.New("host e2ee key is not P-256")
			}
			private, err = ecdh.P256().NewPrivateKey(key.D.FillBytes(make([]byte, 32)))
			if err != nil {
				return nil, fmt.Errorf("convert host e2ee key: %w", err)
			}
		default:
			return nil, errors.New("host e2ee key is not P-256 ECDH")
		}
	} else {
		generated, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate host e2ee key: %w", err)
		}
		private = generated
		if st != nil {
			der, err := x509.MarshalPKCS8PrivateKey(private)
			if err != nil {
				return nil, fmt.Errorf("encode host e2ee key: %w", err)
			}
			if err := st.PutE2EEHostKey(der); err != nil {
				return nil, fmt.Errorf("store host e2ee key: %w", err)
			}
		}
	}

	publicDER, err := x509.MarshalPKIXPublicKey(private.PublicKey())
	if err != nil {
		return nil, fmt.Errorf("encode host e2ee public key: %w", err)
	}
	sum := sha256.Sum256(publicDER)
	return &e2eeState{
		private:     private,
		publicB64:   base64.StdEncoding.EncodeToString(publicDER),
		fingerprint: fingerprint(sum[:]),
	}, nil
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
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
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
		"v":           e2eeVersion,
		"hostId":      s.hostID(),
		"publicKey":   s.e2ee.publicB64,
		"fingerprint": s.e2ee.fingerprint,
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
	var envelope e2eeEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		s.refuse(w, r, codeInvalidRequest, "envelope must be JSON")
		return
	}
	if envelope.V != e2eeVersion || envelope.DeviceID == "" || envelope.ID == "" || envelope.TS == 0 {
		s.refuse(w, r, codeInvalidRequest, "unsupported e2ee envelope")
		return
	}
	now := time.Now()
	at := time.UnixMilli(envelope.TS)
	if now.Sub(at) > e2eeClockSkew || at.Sub(now) > e2eeClockSkew {
		s.refuse(w, r, codeInvalidRequest, "e2ee envelope is outside the clock window")
		return
	}

	browser, deviceKey, ok := s.browserByID(envelope.DeviceID)
	if !ok || browser.E2EEPublicKey == "" {
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	clientPublic, clientB64, err := parseClientE2EEKey(browser.E2EEPublicKey)
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, "device has no usable e2ee key")
		return
	}
	c2s, s2c, err := deriveE2EEKeys(s.e2ee.private, clientPublic, s.e2ee.publicB64, clientB64)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	plain, err := openE2EE(c2s, envelope.Nonce, envelope.Ciphertext, e2eeAAD("req", envelope.DeviceID, envelope.ID, strconv.FormatInt(envelope.TS, 10)))
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
		outer: w,
		key:   s2c,
		aad:   e2eeAAD("resp", envelope.DeviceID, envelope.ID),
	}
	defer writer.finish()
	s.Handler().ServeHTTP(writer, inner)
}

// e2eeResponseWriter buffers the inner response head, then emits encrypted
// frames. Frame 0 is the head; frames 1..n are body bytes. The outer HTTP
// response is always a 200 chunked stream so status and headers cannot leak.
type e2eeResponseWriter struct {
	outer    http.ResponseWriter
	key      []byte
	aad      []byte
	header   http.Header
	status   int
	frame    int
	headSent bool
	finished bool
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
	}
}

func (w *e2eeResponseWriter) finish() {
	if w.finished {
		return
	}
	w.finished = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if err := w.writeHead(); err == nil {
		_ = http.NewResponseController(w.outer).Flush()
	}
}

func (w *e2eeResponseWriter) writeHead() error {
	if w.headSent {
		return nil
	}
	w.headSent = true
	w.outer.Header().Set("Content-Type", e2eeResponseType+"; v=1")
	w.outer.Header().Set("Cache-Control", "no-store")
	w.outer.Header().Set("X-Content-Type-Options", "nosniff")
	w.outer.Header().Set(e2eeResponseHeader, "1")
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
	return w.writeFrame(body)
}

func (w *e2eeResponseWriter) writeFrame(plain []byte) error {
	gcm, err := newGCM(w.key)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	aad := []byte(fmt.Sprintf("%s|%d", string(w.aad), w.frame))
	sealed := gcm.Seal(nil, nonce, plain, aad)
	frame := make([]byte, 4+len(nonce)+len(sealed))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(nonce)+len(sealed)))
	copy(frame[4:], nonce)
	copy(frame[4+len(nonce):], sealed)
	if _, err := w.outer.Write(frame); err != nil {
		return err
	}
	w.frame++
	return nil
}
