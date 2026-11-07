package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

func localUIHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := session.NewManager(1024)
	t.Cleanup(func() { mgr.CloseAll() })
	return New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
}

func TestBrowserUIRemainsLoopbackOnly(t *testing.T) {
	handler := localUIHandler(t)

	for _, tc := range []struct {
		name   string
		path   string
		host   string
		remote string
		header string
		value  string
		want   int
	}{
		{name: "local index", path: "/", host: "127.0.0.1:8787", remote: "127.0.0.1:54321", want: http.StatusOK},
		{name: "local app.js", path: "/app.js", host: "127.0.0.1:8787", remote: "127.0.0.1:54321", want: http.StatusOK},
		{name: "remote peer", path: "/", host: "127.0.0.1:8787", remote: "100.64.0.1:54321", want: http.StatusForbidden},
		{name: "remote host", path: "/", host: "desktop.tailnet.ts.net", remote: "127.0.0.1:54321", want: http.StatusForbidden},
		{name: "forwarded", path: "/app.js", host: "127.0.0.1:8787", remote: "127.0.0.1:54321", header: "X-Forwarded-For", value: "100.64.0.1", want: http.StatusForbidden},
		{name: "remote health", path: "/healthz", host: "desktop.tailnet.ts.net", remote: "100.64.0.1:54321", want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+tc.path, nil)
			req.Host = tc.host
			req.RemoteAddr = tc.remote
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
