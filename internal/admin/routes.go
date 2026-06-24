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

	mux.Handle("GET /collections", http.HandlerFunc(h.collectionsList))
	mux.Handle("POST /collections", http.HandlerFunc(h.createCollection))
	mux.Handle("POST /collections/{name}/delete", http.HandlerFunc(h.deleteCollection))

	mux.Handle("GET /collections/{name}/tree", http.HandlerFunc(h.collectionTree))
	mux.Handle("POST /collections/{name}/upload", http.HandlerFunc(h.uploadFile))
	mux.Handle("POST /collections/{name}/upload/begin", http.HandlerFunc(h.beginUpload))
	mux.Handle("POST /collections/{name}/delete-file/{path...}", http.HandlerFunc(h.deleteFile))
	mux.Handle("POST /collections/{name}/delete-folder/{path...}", http.HandlerFunc(h.deleteFolder))

	return mux
}
