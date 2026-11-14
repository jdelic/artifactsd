package server

import (
	"net/http"
	"strings"
)

// Secure reports whether the request reached artifactsd over a secure transport.
// TLS terminated in-process sets r.TLS; behind a TLS-terminating reverse proxy
// the X-Forwarded-Proto header is honored only from a trusted proxy (design/06
// §6). Used for Secure-flag cookies and absolute-URL scheme selection.
func (s *Server) Secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(s.proxies.ForwardedProto(r), "https") {
		return true
	}
	return false
}

// Scheme returns "https" or "http" for the request's effective scheme.
func (s *Server) Scheme(r *http.Request) string {
	if s.Secure(r) {
		return "https"
	}
	return "http"
}

// logProxyMode states at startup which peers may set X-Forwarded-* headers.
func (s *Server) logProxyMode() {
	if s.cfg.Server.TrustProxy && len(s.cfg.Server.TrustedProxies) == 0 {
		s.log.Warn("--trust-proxy / ARTIFACTSD_TRUST_PROXY is deprecated; set --trusted-proxies / ARTIFACTSD_TRUSTED_PROXIES to your proxy addresses",
			"implied_trusted_proxies", s.proxies.String())
	} else if s.cfg.Server.TrustProxy {
		s.log.Warn("--trust-proxy / ARTIFACTSD_TRUST_PROXY is deprecated and ignored because --trusted-proxies is set")
	}
	if s.proxies.Enabled() {
		s.log.Info("trusted proxies", "cidrs", s.proxies.String())
	} else {
		s.log.Info("trusted proxies: none; forwarded headers ignored")
	}
}
