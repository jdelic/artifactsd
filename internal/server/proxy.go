package server

import (
	"net/http"
	"strings"
)

// Secure reports whether the request reached artifactsd over a secure transport.
// TLS terminated in-process sets r.TLS; behind a TLS-terminating reverse proxy
// the X-Forwarded-Proto header is honored only when trust_proxy is set (design/
// 06 §6). Used for Secure-flag cookies and absolute-URL scheme selection.
func (s *Server) Secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.cfg.Server.TrustProxy && strings.EqualFold(forwardedProto(r), "https") {
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

// forwardedProto returns the first value of X-Forwarded-Proto.
func forwardedProto(r *http.Request) string {
	v := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
