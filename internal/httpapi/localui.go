package httpapi

import (
	"net/http"
	"strings"
)

// localUIOnly keeps the daemon-served browser UI on the machine that runs the
// daemon. The API remains reachable remotely for a native client, but a remote
// browser must not receive JavaScript from the daemon: a transport that can
// change that JavaScript is also able to read every key the page derives.
//
// The check uses both ends of the connection. A remote client can set a
// loopback Host header, and a local reverse proxy can connect from loopback
// with a public Host header, so either an explicit proxy header or a
// non-loopback endpoint refuses the UI.
func localUIOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalUIRequest(r) {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w,
				"DarkFactory serves its browser UI only on loopback. Use the native client for remote access.",
				http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalUIRequest(r *http.Request) bool {
	if !isLoopbackRemote(r.RemoteAddr) {
		return false
	}
	if !isLoopbackHost(hostOnly(r.Host)) {
		return false
	}
	for _, header := range []string{
		"Forwarded",
		"X-Forwarded-For",
		"X-Forwarded-Host",
		"X-Forwarded-Proto",
		"X-Real-IP",
	} {
		if strings.TrimSpace(r.Header.Get(header)) != "" {
			return false
		}
	}
	return true
}

func isLoopbackRemote(remoteAddr string) bool {
	if strings.TrimSpace(remoteAddr) == "" {
		// In-process handlers (tests and embedders) have no network peer. They
		// still need a loopback Host; a real net/http server always fills this.
		return true
	}
	return isLoopbackHost(hostOnly(remoteAddr))
}
