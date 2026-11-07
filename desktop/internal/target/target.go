// Package target validates the address loaded by the optional desktop client.
package target

import (
	"errors"
	"net"
	"net/url"
)

// Parse accepts literal loopback HTTP origins. Hostnames are excluded so DNS
// cannot turn a local desktop window into a client of a remote service.
func Parse(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid client URL")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return nil, errors.New("client URL must be literal loopback HTTP without credentials, query or fragment")
	}
	return u, nil
}

// Debug returns a copy of a validated loopback URL with ?debug=1 for the
// client diagnostics panel. It carries no credentials and is the only query
// the shell adds; Parse still refuses a query in the operator's base URL.
func Debug(u *url.URL) *url.URL {
	v := *u
	q := v.Query()
	q.Set("debug", "1")
	v.RawQuery = q.Encode()
	return &v
}

// Native returns a copy of a validated loopback URL with ?native=1. It tells
// the page that a desktop shell is hosting it, so the page can ask the shell
// for an embedded terminal instead of the web terminal. It is not a credential
// and a browser never has the Wails runtime the page also requires.
func Native(u *url.URL) *url.URL {
	v := *u
	q := v.Query()
	q.Set("native", "1")
	v.RawQuery = q.Encode()
	return &v
}
