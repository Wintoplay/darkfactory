package httpapi

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// networkSource is where a direct HTTP peer connects from, judged by the TCP
// peer address alone. It deliberately ignores X-Forwarded-For: a caller can
// write that header, and a proxy that reaches loopback is already inside the
// host boundary. The classes follow the same split as the native client and
// the Orca runtime so operators see one vocabulary.
type networkSource int

const (
	sourceUnknown networkSource = iota
	sourceLoopback
	sourcePrivate
	sourceTailnet
	sourcePublic
)

func (s networkSource) String() string {
	switch s {
	case sourceLoopback:
		return "loopback"
	case sourcePrivate:
		return "private"
	case sourceTailnet:
		return "tailnet"
	case sourcePublic:
		return "public"
	default:
		return "unknown"
	}
}

var (
	// Tailscale's ULA prefix sits inside fc00::/7, so check the tailnet first.
	tailnetPrefixes = []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
	}
	privatePrefixes = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
	}
)

// sourceAddress parses the peer half of a net/http RemoteAddr. A dual-stack
// listener reports IPv4 peers as ::ffff:a.b.c.d; a link-local peer may carry a
// zone. Unmapping first keeps every prefix comparison in one address family.
func sourceAddress(remoteAddr string) (netip.Addr, bool) {
	value := strings.TrimSpace(remoteAddr)
	if value == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if i := strings.LastIndexByte(value, '%'); i >= 0 {
		value = value[:i]
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func classifySourceAddr(addr netip.Addr) networkSource {
	if !addr.IsValid() {
		return sourceUnknown
	}
	if addr.IsLoopback() {
		return sourceLoopback
	}
	for _, prefix := range tailnetPrefixes {
		if prefix.Contains(addr) {
			return sourceTailnet
		}
	}
	for _, prefix := range privatePrefixes {
		if prefix.Contains(addr) {
			return sourcePrivate
		}
	}
	return sourcePublic
}

func classifySource(remoteAddr string) networkSource {
	addr, ok := sourceAddress(remoteAddr)
	if !ok {
		return sourceUnknown
	}
	return classifySourceAddr(addr)
}

// onLinkPrefixes caches the host's own interface networks. A home LAN that
// hands out global IPv6 addresses (SLAAC) looks public by address alone, but a
// peer inside one of our own on-link prefixes is a LAN peer, not an internet
// caller. Refreshing every 30 s keeps a new interface from being ignored
// without walking interfaces on every refused request.
var onLinkCache struct {
	sync.Mutex
	at       time.Time
	prefixes []netip.Prefix
}

func onLinkPrefixes() []netip.Prefix {
	onLinkCache.Lock()
	defer onLinkCache.Unlock()
	if !onLinkCache.at.IsZero() && time.Since(onLinkCache.at) < 30*time.Second {
		return onLinkCache.prefixes
	}
	var prefixes []netip.Prefix
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, raw := range addrs {
				ipnet, ok := raw.(*net.IPNet)
				if !ok || ipnet.IP == nil || ipnet.Mask == nil {
					continue
				}
				addr, ok := netip.AddrFromSlice(ipnet.IP)
				if !ok {
					continue
				}
				addr = addr.Unmap()
				ones, bits := ipnet.Mask.Size()
				if ones < 0 || bits != addr.BitLen() {
					continue
				}
				prefixes = append(prefixes, netip.PrefixFrom(addr, ones))
			}
		}
	}
	onLinkCache.at = time.Now()
	onLinkCache.prefixes = prefixes
	return prefixes
}

func onLinkAddress(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	for _, prefix := range onLinkPrefixes() {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// sourcePolicyActive reports whether this listener can receive a direct peer
// that is not loopback. A loopback bind cannot, so the guard stays out of the
// way and the local-only default is byte-for-byte what it was before.
func (s *Server) sourcePolicyActive() bool {
	addr := strings.TrimSpace(s.cfg.Listen)
	if addr == "" {
		return false
	}
	return !listenIsLoopback(addr)
}

// sourceRefusal is the non-loopback listener's public-address gate. It runs
// before routing so /healthz, pairing and the encrypted envelope cannot be
// reached by a direct internet peer. A request that arrives through a local
// proxy has a loopback peer and is not classified here; that proxy is the
// operator's trust boundary and must terminate TLS.
func (s *Server) sourceRefusal(r *http.Request) *refusal {
	if !s.sourcePolicyActive() || s.cfg.AllowPublicSources {
		return nil
	}
	addr, ok := sourceAddress(r.RemoteAddr)
	if !ok {
		// A socket that already closed has no peer to refuse. It cannot carry
		// a live request either.
		return nil
	}
	if classifySourceAddr(addr) != sourcePublic || onLinkAddress(addr) {
		return nil
	}
	s.notePublicRefusal(addr.String())
	ref := refusalFor(codeSourceDenied, "", "this listener accepts loopback, LAN and Tailscale peers; a public address was refused")
	ref.Rule = "public_source"
	return &ref
}

// notePublicRefusal logs the first public refusal each hour. The ledger is for
// authenticated decisions; an unauthenticated peer would otherwise be able to
// fill it.
func (s *Server) notePublicRefusal(addr string) {
	s.sourceMu.Lock()
	now := time.Now()
	due := s.sourceWarnAt.IsZero() || now.Sub(s.sourceWarnAt) >= time.Hour
	if due {
		s.sourceWarnAt = now
	}
	s.sourceMu.Unlock()
	if due {
		s.loggerForSource().Warn("refused a public request",
			"source", addr,
			"rule", "restart with allowPublicSources or --allow-public to accept public peers")
	}
}

func (s *Server) loggerForSource() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
