package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestIsLocalRequest(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		host   string
		want   bool
	}{
		{"loopback ipv4", "127.0.0.1:1234", "127.0.0.1:8787", true},
		{"loopback ipv6", "[::1]:1234", "localhost:8787", true},
		{"tailscale proxy host", "127.0.0.1:1234", "host.tailnet.ts.net", false},
		{"remote direct", "100.64.0.1:1234", "127.0.0.1:8787", false},
		{"spoofed host", "100.64.0.1:1234", "localhost:8787", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/fs/pick", nil)
			req.RemoteAddr = tc.remote
			req.Host = tc.host
			if got := isLocalRequest(req); got != tc.want {
				t.Fatalf("isLocalRequest(%q, %q) = %v, want %v", tc.remote, tc.host, got, tc.want)
			}
		})
	}
}
