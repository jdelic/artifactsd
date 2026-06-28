package admin

import "net/http"

// Routes returns the admin UI's handler, with relative patterns (mounted
// under the server's configurable Admin.Path via http.StripPrefix, the same
// way internal/api.Handler.Routes() is mounted at /api/v1).
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", staticHandler())

	mux.Handle("GET /{$}", http.HandlerFunc(h.dashboard))
	mux.Handle("POST /logout", http.HandlerFunc(h.logout))
	mux.Handle("POST /maintenance/cleanup", http.HandlerFunc(h.maintenanceCleanup))

	// Cross-collection overviews reachable from the top nav (artifactsd-rmn/0ab).
	mux.Handle("GET /files", http.HandlerFunc(h.allFiles))
	mux.Handle("GET /shares", http.HandlerFunc(h.allShares))
	mux.Handle("POST /shares/{share_id}/revoke", http.HandlerFunc(h.revokeShareGlobal))

	mux.Handle("GET /collections", http.HandlerFunc(h.collectionsList))
	mux.Handle("POST /collections", http.HandlerFunc(h.createCollection))
	mux.Handle("POST /collections/{name}/delete", http.HandlerFunc(h.deleteCollection))

	mux.Handle("GET /collections/{name}/tree", http.HandlerFunc(h.collectionTree))
	mux.Handle("POST /collections/{name}/upload", http.HandlerFunc(h.uploadFile))
	mux.Handle("POST /collections/{name}/upload/begin", http.HandlerFunc(h.beginUpload))
	mux.Handle("POST /collections/{name}/delete-file/{path...}", http.HandlerFunc(h.deleteFile))
	mux.Handle("POST /collections/{name}/delete-folder/{path...}", http.HandlerFunc(h.deleteFolder))

	mux.Handle("GET /collections/{name}/meta", http.HandlerFunc(h.editMetadata))
	mux.Handle("POST /collections/{name}/meta/fields", http.HandlerFunc(h.saveMetaFields))
	mux.Handle("POST /collections/{name}/meta/raw", http.HandlerFunc(h.saveMetaRaw))

	mux.Handle("GET /collections/{name}/shares", http.HandlerFunc(h.listShares))
	mux.Handle("POST /collections/{name}/shares", http.HandlerFunc(h.createShare))
	mux.Handle("POST /collections/{name}/share", http.HandlerFunc(h.quickShare))
	mux.Handle("POST /collections/{name}/unshare", http.HandlerFunc(h.unshare))
	mux.Handle("POST /collections/{name}/shares/{share_id}/revoke", http.HandlerFunc(h.revokeShare))

	// Upload request links (design/15).
	mux.Handle("GET /upload-requests", http.HandlerFunc(h.allUploadRequests))
	mux.Handle("POST /upload-requests", http.HandlerFunc(h.createUploadRequestAdmin))
	mux.Handle("POST /upload-requests/{request_id}/revoke", http.HandlerFunc(h.revokeUploadRequestAdmin))

	mux.Handle("GET /tokens", http.HandlerFunc(h.tokensList))
	mux.Handle("POST /tokens", http.HandlerFunc(h.createToken))
	mux.Handle("POST /tokens/{id}/revoke", http.HandlerFunc(h.revokeToken))

	mux.Handle("GET /audit", http.HandlerFunc(h.auditLog))

	return mux
}
