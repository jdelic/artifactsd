package server

import (
	"net/http"
	"strings"

	"github.com/jdelic/artifactsd/internal/config"
)

// routes builds the route mux with the health endpoints and stubs for the route
// groups whose handlers land in later epics (design/01 §3). Real handlers for
// /c, /s, /api/v1, /admin, robots/sitemap register over these as they are built.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// OIDC admin login flow (design/14 §7). The callback path is the single
	// source of truth behind the default redirect URL (config.OIDCCallbackPath).
	mux.HandleFunc("GET /auth/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("GET "+config.OIDCCallbackPath, s.handleOIDCCallback)

	// Public serving (design/05).
	if s.serve != nil {
		mux.HandleFunc("GET /c/{collection}/{path...}", s.serve.ServeCollection)
		mux.HandleFunc("HEAD /c/{collection}/{path...}", s.serve.ServeCollection)
		mux.HandleFunc("GET /s/{share_id}/{path...}", s.serve.ServeShare)
		mux.HandleFunc("HEAD /s/{share_id}/{path...}", s.serve.ServeShare)
		// Root robots policy (design/05 §3, SPEC §10.3) — deliberately
		// root-only, no /c/{collection}/robots.txt: RFC 9309 only recognizes
		// the file at the domain root, so per-collection policy is folded into
		// this response's Allow/Disallow lines instead.
		mux.HandleFunc("GET /robots.txt", s.serve.RobotsRoot)
		mux.HandleFunc("HEAD /robots.txt", s.serve.RobotsRoot)
		// Root sitemap (design/05 §4, SPEC §10.4). The per-collection sitemap
		// (/c/{collection}/sitemap.xml) is a generated endpoint too, but it is
		// dispatched from inside ServeCollection rather than registered here: a
		// GET on a specific path implicitly also matches HEAD, which collides
		// with the HEAD /c/{collection}/{path...} wildcard under net/http's
		// pattern-precedence rules. Folding it into ServeCollection both avoids
		// that conflict and lets the generated sitemap shadow any file literally
		// named sitemap.xml at a collection root.
		mux.HandleFunc("GET /sitemap.xml", s.serve.SitemapRoot)
		mux.HandleFunc("HEAD /sitemap.xml", s.serve.SitemapRoot)
	} else {
		mux.Handle("/robots.txt", notImplemented("public serving"))
		mux.Handle("/sitemap.xml", notImplemented("public serving"))
		mux.Handle("/c/", notImplemented("public serving"))
		mux.Handle("/s/", notImplemented("public serving"))
	}
	if s.api != nil {
		mux.Handle("/api/v1/", s.api.Routes())
	} else {
		mux.Handle("/api/v1/", notImplemented("management API"))
	}
	// Admin UI (design/09): server-rendered html/template + htmx over the same
	// service layer the API uses. adminGate enforces OIDC/basic auth; csrfGate
	// (design/09 §5) protects its mutating routes once a session exists.
	if s.adminUI != nil {
		stripped := http.StripPrefix(strings.TrimSuffix(s.cfg.Admin.Path, "/"), s.adminUI.Routes())
		mux.Handle(s.cfg.Admin.Path+"/", s.adminSecurityHeaders(s.adminGate(s.csrfGate(stripped))))
	} else {
		mux.Handle(s.cfg.Admin.Path+"/", s.adminSecurityHeaders(s.adminGate(notImplemented("admin UI"))))
	}

	mux.HandleFunc("/", s.handleRoot)

	// Separate-admin-origin isolation (design/14 §10): a no-op unless
	// ARTIFACTSD_ADMIN_HOST is set, in which case it dispatches the admin and
	// public planes by host.
	return s.hostIsolationGate(mux)
}

// handleRoot returns 404 for unmatched paths (the catch-all).
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.Error(w, "artifactsd", http.StatusOK)
		return
	}
	http.NotFound(w, r)
}

// notImplemented is a placeholder handler for not-yet-built route groups.
func notImplemented(what string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, what+" not implemented", http.StatusNotImplemented)
	})
}
