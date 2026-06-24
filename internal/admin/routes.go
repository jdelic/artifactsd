package admin

import "net/http"

// Routes returns the admin UI's handler, with relative patterns (mounted
// under the server's configurable Admin.Path via http.StripPrefix, the same
// way internal/api.Handler.Routes() is mounted at /api/v1).
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", staticHandler())

	mux.Handle("GET /{$}", http.HandlerFunc(h.dashboard))
	mux.Handle("POST /maintenance/cleanup", http.HandlerFunc(h.maintenanceCleanup))

	return mux
}
