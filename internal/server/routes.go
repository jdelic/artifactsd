package server

import (
	"net/http"
)

// routes builds the route mux with the health endpoints and stubs for the route
// groups whose handlers land in later epics (design/01 §3). Real handlers for
// /c, /s, /api/v1, /admin, robots/sitemap register over these as they are built.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// OIDC admin login flow (design/14 §7).
	mux.HandleFunc("GET /auth/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("GET /auth/oidc/callback", s.handleOIDCCallback)

	// Public serving (design/05).
	if s.serve != nil {
		mux.HandleFunc("GET /c/{collection}/{path...}", s.serve.ServeCollection)
		mux.HandleFunc("HEAD /c/{collection}/{path...}", s.serve.ServeCollection)
		mux.HandleFunc("GET /s/{share_id}/{path...}", s.serve.ServeShare)
		mux.HandleFunc("HEAD /s/{share_id}/{path...}", s.serve.ServeShare)
	} else {
		mux.Handle("/c/", notImplemented("public serving"))
		mux.Handle("/s/", notImplemented("public serving"))
	}
	if s.api != nil {
		mux.Handle("/api/v1/", s.api.Routes())
	} else {
		mux.Handle("/api/v1/", notImplemented("management API"))
	}
	// Admin UI lands in its epic; the OIDC-gated entry point is wired now.
	mux.Handle(s.cfg.Admin.Path+"/", s.adminGate(notImplemented("admin UI")))

	mux.HandleFunc("/", s.handleRoot)
	return mux
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
