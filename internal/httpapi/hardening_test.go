package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

func newHardeningServer(t *testing.T) (*Server, *httptest.Server, *store.Store, config.Config) {
	t.Helper()
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	t.Cleanup(func() {
		mgr.CloseAll()
		st.Close()
	})
	api := New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return api, srv, st, cfg
}

func TestE2EEBudgetIsPerSourceAndBounded(t *testing.T) {
	api := &Server{e2eeSources: map[string]pairWindow{}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee", nil)
	req.RemoteAddr = "203.0.113.7:1234"

	for i := 0; i < e2eeSourceLimit; i++ {
		if !api.allowE2EE(req) {
			t.Fatalf("request %d refused under the per-source budget", i+1)
		}
	}
	if api.allowE2EE(req) {
		t.Fatal("per-source budget was not enforced")
	}

	// A denied source must not spend the shared budget, so another source
	// still works after one source is exhausted.
	other := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee", nil)
	other.RemoteAddr = "203.0.113.8:1234"
	if !api.allowE2EE(other) {
		t.Fatal("one exhausted source also exhausted the global budget")
	}

	api.e2eeGlobal = pairWindow{Start: time.Now(), Count: e2eeGlobalLimit}
	if api.allowE2EE(other) {
		t.Fatal("global budget was not enforced")
	}
}

func TestE2EERouteRefusesOverBudgetTyped(t *testing.T) {
	api, srv, _, _ := newHardeningServer(t)
	// The httptest client reaches the daemon from loopback, so seed that
	// source's window instead of making 300 real requests.
	api.e2eeSources["127.0.0.1"] = pairWindow{Start: time.Now(), Count: e2eeSourceLimit}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/e2ee", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 429: %s", resp.StatusCode, raw)
	}
	var body refusalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != codeE2EERateLimited {
		t.Fatalf("code = %q, want %q", body.Code, codeE2EERateLimited)
	}
}

func TestOriginMustMatchPortAndFetchSite(t *testing.T) {
	_, srv, _, cfg := newHardeningServer(t)
	host := strings.TrimPrefix(srv.URL, "http://")

	cases := []struct {
		name      string
		origin    string
		fetchSite string
		status    int
	}{
		{name: "same origin", origin: "http://" + host, fetchSite: "same-origin", status: http.StatusOK},
		{name: "another port on the same host", origin: "http://127.0.0.1:1", fetchSite: "same-origin", status: http.StatusForbidden},
		{name: "same site, another port", origin: "http://" + host, fetchSite: "same-site", status: http.StatusForbidden},
		{name: "cross site", origin: "http://" + host, fetchSite: "cross-site", status: http.StatusForbidden},
		{name: "native client sends no fetch metadata", origin: "", fetchSite: "", status: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+cfg.Token)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
		})
	}
}

func TestLogoutDropsNativePaneCapability(t *testing.T) {
	_, srv, st, cfg := newHardeningServer(t)
	sess := nativePaneSession(t, srv.URL, cfg.Token, st)

	resp := postJSON(t, srv.URL+"/api/v1/sessions/"+sess.ID+"/native-pane", cfg.Token, nil)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("native-pane status = %d: %s", resp.StatusCode, raw)
	}
	var minted struct {
		Pane string `json:"pane"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// The same principal that minted it logs out. The capability is PTY
	// authority, so it must be gone now, not when the window closes.
	logout, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	logout.Header.Set("Authorization", "Bearer "+cfg.Token)
	out, err := http.DefaultClient.Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	out.Body.Close()
	if out.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", out.StatusCode)
	}

	input, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/"+sess.ID+"/input", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	input.Header.Set("Authorization", "Bearer "+minted.Pane)
	after, err := http.DefaultClient.Do(input)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pane input after logout = %d, want 401", after.StatusCode)
	}
}

func TestSanitizeLogLine(t *testing.T) {
	got := sanitizeLogLine("hello\nlevel=ERROR msg=forged\r\ttab\x1b[31mred\x00")
	want := "hello level=ERROR msg=forged  tab[31mred"
	if got != want {
		t.Fatalf("sanitizeLogLine = %q, want %q", got, want)
	}
}

func TestLocalOnlyRoutesRefuseForwardedRequests(t *testing.T) {
	_, srv, _, cfg := newHardeningServer(t)
	cases := []struct {
		name string
		path string
		code string
	}{
		{name: "open", path: "/api/v1/open", code: codeOpenHostOnly},
		{name: "picker", path: "/api/v1/fs/pick", code: codePickerRemote},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, srv.URL+tc.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+cfg.Token)
			req.Header.Set("X-DF-Request", "1")
			req.Header.Set("X-Forwarded-For", "203.0.113.9")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want a refusal", resp.StatusCode)
			}
			var body refusalEnvelope
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.code {
				t.Fatalf("code = %q, want %q", body.Code, tc.code)
			}
		})
	}
}

// A listener with finite deadlines must not cut a terminal stream. The stream
// handler clears its own read and write deadlines; without that, the default
// WriteTimeout would end every session after a minute.
func TestTerminalStreamOutlivesListenerTimeouts(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	t.Cleanup(func() {
		mgr.CloseAll()
		st.Close()
	})
	api := New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// httptest.Server.Start ignores Config timeouts (it calls http.Serve with
	// the handler only), so run the real server to make the assertion mean
	// something.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		Handler:      api.Handler(),
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	baseURL := "http://" + listener.Addr().String()

	sess := nativePaneSession(t, baseURL, cfg.Token, st)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/sessions/"+sess.ID+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	if _, err := resp.Body.Read(make([]byte, 1)); err != nil {
		t.Fatalf("read first stream byte: %v", err)
	}

	// Drain whatever the attach replay already buffered, so the marker below
	// cannot be satisfied by old bytes.
	var mu sync.Mutex
	var received bytes.Buffer
	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buf)
			mu.Lock()
			received.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				readErr <- err
				return
			}
		}
	}()

	// Outlive both deadlines before proving the stream still carries bytes.
	time.Sleep(400 * time.Millisecond)

	marker := "alive-after-deadline"
	input, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/sessions/"+sess.ID+"/input", strings.NewReader("echo "+marker+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	input.Header.Set("Authorization", "Bearer "+cfg.Token)
	sent, err := http.DefaultClient.Do(input)
	if err != nil {
		t.Fatal(err)
	}
	sent.Body.Close()
	if sent.StatusCode != http.StatusNoContent {
		t.Fatalf("input status = %d", sent.StatusCode)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		seen := bytes.Contains(received.Bytes(), []byte(marker))
		mu.Unlock()
		if seen {
			return
		}
		select {
		case err := <-readErr:
			t.Fatalf("stream ended after the listener deadlines: %v", err)
		case <-time.After(25 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			mu.Lock()
			tail := received.String()
			mu.Unlock()
			t.Fatalf("marker never arrived after input; stream may have been cut (tail %q)", tail)
		}
	}
}
