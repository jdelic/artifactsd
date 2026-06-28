package server

import (
	"net/http"
	"strings"

	"github.com/jdelic/artifactsd/internal/config"
)

// hostIsolationGate enforces the separate-admin-origin topology (design/14 §10,
// artifactsd-1it). When ARTIFACTSD_ADMIN_HOST is set, the admin control plane
// (the admin UI plus the OIDC login/callback that set the session cookie) is
// served ONLY on that host, and the public artifact plane (/c, /s, robots.txt,
// sitemap.xml) ONLY on the public base-URL host. Because the admin session
// cookie is host-only (no Domain attribute), it is then scoped to a host that
// artifact JavaScript on the public origin cannot reach — preventing
// artifact→admin credential theft structurally, independent of the per-artifact
// sandbox. Cross-plane requests are 404'd (rather than redirected) so neither
// host reveals what lives on the other.
//
// Shared, cookie-less routes (health checks, the bearer-token management API,
// and the catch-all root) are not host-restricted: the management API carries
// no ambient cookie, so it is not same-origin-exploitable and may be reached on
// either host. With ARTIFACTSD_ADMIN_HOST unset (the default), this gate is a
// no-op and admin + artifacts share one origin.
func (s *Server) hostIsolationGate(next http.Handler) http.Handler {
	adminHost := config.HostOnly(s.cfg.Server.AdminHost)
	if adminHost == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		onAdminHost := strings.EqualFold(config.HostOnly(s.requestHost(r)), adminHost)
		switch {
		case s.isAdminPlane(r.URL.Path):
			if !onAdminHost {
				http.NotFound(w, r)
				return
			}
		case isPublicPlane(r.URL.Path):
			if onAdminHost {
				http.NotFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestHost returns the host the client addressed. It honors X-Forwarded-Host
// only when proxies are trusted (mirroring clientIP), since the admin/public
// dispatch depends on it; otherwise it uses the request's own Host header.
func (s *Server) requestHost(r *http.Request) string {
	if s.cfg.Server.TrustProxy {
		if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
			// X-Forwarded-Host may carry a comma-separated chain; the first is the
			// original client-facing host.
			if i := strings.IndexByte(xfh, ','); i >= 0 {
				xfh = xfh[:i]
			}
			return strings.TrimSpace(xfh)
		}
	}
	return r.Host
}

// isAdminPlane reports whether path belongs to the cookie-authenticated admin
// control plane: the admin UI (under Admin.Path) or the OIDC login/callback that
// establishes the admin session.
func (s *Server) isAdminPlane(path string) bool {
	adminPath := strings.TrimSuffix(s.cfg.Admin.Path, "/")
	if path == adminPath || strings.HasPrefix(path, adminPath+"/") {
		return true
	}
	return strings.HasPrefix(path, "/auth/oidc/")
}

// isPublicPlane reports whether path serves public artifacts (the only content
// a hostile artifact's same-origin requests could target): collection/share
// serving and the root robots/sitemap policies.
func isPublicPlane(path string) bool {
	switch path {
	case "/robots.txt", "/sitemap.xml":
		return true
	}
	return strings.HasPrefix(path, "/c/") || strings.HasPrefix(path, "/s/")
}
