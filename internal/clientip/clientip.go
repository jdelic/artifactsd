// Package clientip derives the client address, host and scheme of a request,
// honoring X-Forwarded-* headers only when the socket peer is a configured
// trusted proxy (design/06 §6).
//
// X-Forwarded-For is read from right to left: each trusted proxy appends the
// address it received the request from, so the rightmost entry that is not a
// trusted proxy is the client. Entries to the left of it were supplied by the
// client and are never used.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DeprecatedTrustProxyDefaults is the trusted set implied by the deprecated
// boolean --trust-proxy=true: loopback plus the private address ranges.
var DeprecatedTrustProxyDefaults = []string{
	"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
}

// Resolver resolves request-origin information. A nil or empty Resolver trusts
// no proxy and ignores every forwarded header.
type Resolver struct {
	trusted []netip.Prefix
}

// New parses entries (CIDRs or bare IPs; a bare IP is a single-address prefix).
func New(entries []string) (*Resolver, error) {
	r := &Resolver{}
	for _, e := range entries {
		p, err := ParsePrefix(e)
		if err != nil {
			return nil, err
		}
		r.trusted = append(r.trusted, p)
	}
	return r, nil
}

// ParsePrefix parses one trusted-proxy entry: a CIDR or a bare IP address.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("trusted proxy %q: %w", s, err)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("trusted proxy %q is not an IP address or CIDR", s)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// Enabled reports whether any proxy is trusted.
func (r *Resolver) Enabled() bool { return r != nil && len(r.trusted) > 0 }

// String lists the trusted prefixes, comma-separated.
func (r *Resolver) String() string {
	if r == nil {
		return ""
	}
	parts := make([]string, len(r.trusted))
	for i, p := range r.trusted {
		parts[i] = p.String()
	}
	return strings.Join(parts, ",")
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	if r == nil || !a.IsValid() {
		return false
	}
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// peer returns the socket peer address, or an invalid Addr if RemoteAddr does
// not parse.
func peer(req *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap().WithZone("")
}

// parseForwarded parses one X-Forwarded-For element (an IP, optionally with a
// port or IPv6 brackets).
func parseForwarded(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().WithZone(""), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	if a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")); err == nil {
		return a.Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}

// ClientIP returns the client address. If the socket peer is not a trusted
// proxy, it is the client. Otherwise X-Forwarded-For (all header lines) is
// walked right to left, skipping trusted proxies; the first untrusted address
// is the client; if every element is trusted, the socket peer is returned. An
// unparseable element stops the walk and the last good address is returned,
// so attacker-supplied text is never returned. The result
// is invalid only if RemoteAddr itself does not parse.
func (r *Resolver) ClientIP(req *http.Request) netip.Addr {
	p := peer(req)
	if !r.isTrusted(p) {
		return p
	}
	addr := p
	values := req.Header.Values("X-Forwarded-For")
	var elems []string
	for _, v := range values {
		elems = append(elems, strings.Split(v, ",")...)
	}
	for i := len(elems) - 1; i >= 0; i-- {
		if strings.TrimSpace(elems[i]) == "" {
			continue
		}
		a, ok := parseForwarded(elems[i])
		if !ok {
			return addr
		}
		addr = a
		if !r.isTrusted(a) {
			return addr
		}
	}
	// Every forwarded hop is a trusted proxy: the request originated inside the
	// trusted network, so the socket peer is the best answer.
	return p
}

// ClientIPString is ClientIP as a string ("" when RemoteAddr does not parse).
func (r *Resolver) ClientIPString(req *http.Request) string {
	a := r.ClientIP(req)
	if !a.IsValid() {
		return ""
	}
	return a.String()
}

// ForwardedHost returns the first X-Forwarded-Host element when the socket
// peer is a trusted proxy, else "".
func (r *Resolver) ForwardedHost(req *http.Request) string {
	return r.firstTrusted(req, "X-Forwarded-Host")
}

// ForwardedProto returns the first X-Forwarded-Proto element when the socket
// peer is a trusted proxy, else "".
func (r *Resolver) ForwardedProto(req *http.Request) string {
	return r.firstTrusted(req, "X-Forwarded-Proto")
}

func (r *Resolver) firstTrusted(req *http.Request, header string) string {
	if !r.isTrusted(peer(req)) {
		return ""
	}
	v := req.Header.Get(header)
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
