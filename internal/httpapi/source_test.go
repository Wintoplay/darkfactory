package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClassifySource(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want networkSource
	}{
		{"loopback ipv4", "127.0.0.1:1234", sourceLoopback},
		{"loopback ipv6", "[::1]:1234", sourceLoopback},
		{"tailnet ipv4", "100.64.0.5:1234", sourceTailnet},
		{"tailnet ipv6", "[fd7a:115c:a1e0::1]:1234", sourceTailnet},
		{"private ten", "10.1.2.3:1234", sourcePrivate},
		{"private one seventy two", "172.16.5.4:1234", sourcePrivate},
		{"private one ninety two", "192.168.1.10:1234", sourcePrivate},
		{"link local", "169.254.1.2:1234", sourcePrivate},
		{"ipv6 link local with zone", "[fe80::1%eth0]:1234", sourcePrivate},
		{"ipv4 mapped", "::ffff:192.168.1.2", sourcePrivate},
		{"ipv4 mapped dual stack listener", "[::ffff:100.64.0.5]:1234", sourceTailnet},
		{"public ipv4", "203.0.113.9:1234", sourcePublic},
		{"public ipv4 mapped", "[::ffff:203.0.113.9]:1234", sourcePublic},
		{"public ipv6", "[2001:db8::1]:1234", sourcePublic},
		{"empty peer", "", sourceUnknown},
		{"not an address", "example.com:1234", sourceUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySource(tc.addr); got != tc.want {
				t.Fatalf("classifySource(%q) = %s, want %s", tc.addr, got, tc.want)
			}
		})
	}
}

func sourceTestServer(t *testing.T, listen string, allowPublic bool) *Server {
	t.Helper()
	_, api := newRefusalServer(t, nil)
	api.cfg.Listen = listen
	api.cfg.AllowPublicSources = allowPublic
	return api
}

func getHealth(handler http.Handler, host, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/healthz", nil)
	req.Host = host
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// The guard exists only when the listener can actually accept a non-loopback
// peer. The default loopback bind must not change behaviour, and a public bind
// must still admit LAN and Tailscale peers.
func TestPublicSourceGuardFollowsListener(t *testing.T) {
	cases := []struct {
		name        string
		listen      string
		allowPublic bool
		remote      string
		want        int
	}{
		{"loopback listener ignores public peer", "127.0.0.1:8787", false, "203.0.113.9:1234", http.StatusOK},
		{"public listener refuses public peer", "0.0.0.0:8787", false, "203.0.113.9:1234", http.StatusForbidden},
		{"public listener admits tailnet peer", "0.0.0.0:8787", false, "100.64.0.5:1234", http.StatusOK},
		{"public listener admits private peer", "0.0.0.0:8787", false, "192.168.1.10:1234", http.StatusOK},
		{"public listener admits loopback peer", "0.0.0.0:8787", false, "127.0.0.1:1234", http.StatusOK},
		{"explicit opt-in admits public peer", "0.0.0.0:8787", true, "203.0.113.9:1234", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := getHealth(sourceTestServer(t, tc.listen, tc.allowPublic).Handler(), "127.0.0.1:8787", tc.remote)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusForbidden {
				var body refusalEnvelope
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body is not a refusal: %v (%s)", err, rec.Body.String())
				}
				if body.Code != codeSourceDenied {
					t.Fatalf("code = %q, want %q", body.Code, codeSourceDenied)
				}
			}
		})
	}
}

// A full-scope device may use the workspace from anywhere, but minting a
// pairing code or revoking a device is host authority. The local UI is
// loopback-only, so the normal path is unchanged; a stolen remote cookie is
// what this refuses.
func TestHostCredentialVerbsNeedHostAuthority(t *testing.T) {
	_, api := newRefusalServer(t, nil)
	mintRec := httptest.NewRecorder()
	mintReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/v1/open/code", nil)
	mintReq.Host = "127.0.0.1:8787"
	token, _, err := api.mintBrowserSession(mintRec, mintReq, "Remote browser", scopeFull)
	if err != nil {
		t.Fatal(err)
	}

	do := func(method, path, remote, body string, cookie, bearer string) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, reader)
		req.Host = "127.0.0.1:8787"
		req.RemoteAddr = remote
		req.Header.Set("X-DF-Request", "1")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, req)
		return rec
	}

	t.Run("remote cookie cannot mint a pairing code", func(t *testing.T) {
		rec := do(http.MethodPost, "/api/v1/pair/code", "100.64.0.9:1234", "{}", token, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %q)", rec.Code, rec.Body.String())
		}
		var body refusalEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Code != codeHostAdmin || body.Rule != "host_only" {
			t.Fatalf("refusal = %#v, want host_admin rule=host_only", body)
		}
	})

	t.Run("remote cookie cannot revoke devices", func(t *testing.T) {
		for _, path := range []string{"/api/v1/devices/dev_other", "/api/v1/devices/revoke-all"} {
			method := http.MethodDelete
			if strings.HasSuffix(path, "revoke-all") {
				method = http.MethodPost
			}
			rec := do(method, path, "192.168.1.20:1234", "{}", token, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s status = %d, want 403 (body %q)", method, path, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("loopback cookie still mints a pairing code", func(t *testing.T) {
		rec := do(http.MethodPost, "/api/v1/pair/code", "127.0.0.1:1234", "{}", token, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
	})

	t.Run("master token from a remote peer still mints a pairing code", func(t *testing.T) {
		rec := do(http.MethodPost, "/api/v1/pair/code", "100.64.0.9:1234", "{}", "", "test-token")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
	})

	t.Run("remote cookie keeps workspace verbs", func(t *testing.T) {
		rec := do(http.MethodPost, "/api/v1/debug/client", "100.64.0.9:1234", "x", token, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %q)", rec.Code, rec.Body.String())
		}
	})
}

func TestDeviceKeyRegistrationIsNotHostAdmin(t *testing.T) {
	// A native client on another machine enters the master token and registers
	// its own key; that is a first-login step, not credential administration.
	if got := verbPermission(verbDeviceKeyCreate); got != permissionWorkspace {
		t.Fatalf("device.key.create permission = %s, want workspace", got)
	}
	if got := verbPermission(verbPairCode); got != permissionHostCredential {
		t.Fatalf("pair.code permission = %s, want host-credential", got)
	}
}
