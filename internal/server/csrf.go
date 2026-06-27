package server

import (
	"net/http"
	"net/url"

	"github.com/jdelic/artifactsd/internal/session"
)

// csrfGate protects state-changing admin requests with a same-origin check
// plus a synchronizer token bound to the caller's identity (design/09 §5,
// design/14 §7). It runs inside the admin mux, downstream of adminGate, which
// has already stashed that identity in the request context — covering the
// basic-auth fallback too, which carries no session cookie of its own
// (adminGate synthesizes a stable identity for it; see its comment). A
// missing identity here still fails closed rather than relying solely on
// that ordering.
func (s *Server) csrfGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutating(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.sameOrigin(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		// Fetch Metadata defense-in-depth (design/14 §7): a browser labels a
		// request triggered by a cross-site (e.g. sandboxed-artifact) context as
		// Sec-Fetch-Site: cross-site. Reject those outright. Same-origin,
		// same-site, and user-initiated ("none") are allowed; an absent header
		// (older clients, curl) falls through to the token check below.
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-site request rejected", http.StatusForbidden)
			return
		}
		id := session.FromContext(r.Context())
		if id == nil {
			http.Error(w, "admin authentication required", http.StatusUnauthorized)
			return
		}
		token := r.Header.Get("X-CSRF-Token")
		if token == "" {
			token = r.FormValue("csrf_token")
		}
		if token == "" || !s.session.VerifyCSRFToken(id, token) {
			http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isMutating reports whether method is a state-changing HTTP method.
func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// sameOrigin reports whether the request's Origin header (when present)
// matches the server's own base URL host. Requests without an Origin header
// (e.g. same-site form posts in older browsers) are not rejected here — the
// CSRF token check below is the primary defense.
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	base, err := url.Parse(s.cfg.Server.BaseURL)
	if err != nil {
		return true // base_url is validated at startup; a parse failure here can't happen
	}
	return o.Host == base.Host
}
