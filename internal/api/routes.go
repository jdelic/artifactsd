package api

import (
	"net/http"

	"github.com/jdelic/artifactsd/internal/auth"
)

// Routes returns the /api/v1 handler with scope-gated routes (design/04).
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	// Collections (design/04 §2).
	mux.Handle("POST /api/v1/collections", h.requireScope(auth.ScopeCollectionsWrite, h.createCollection))
	mux.Handle("GET /api/v1/collections", h.requireScope(auth.ScopeCollectionsRead, h.listCollections))
	mux.Handle("GET /api/v1/collections/{c}", h.requireScope(auth.ScopeCollectionsRead, h.getCollection))
	mux.Handle("PATCH /api/v1/collections/{c}/meta", h.requireScope(auth.ScopeCollectionsWrite, h.patchCollectionMeta))
	mux.Handle("DELETE /api/v1/collections/{c}", h.requireScope(auth.ScopeCollectionsWrite, h.deleteCollection))

	// Files (design/04 §3). Go's ServeMux requires {path...} to be the final
	// segment, so the ".../meta" sub-resource is dispatched in code by suffix.
	mux.Handle("PUT /api/v1/collections/{c}/files/{path...}", h.requireScope(auth.ScopeCollectionsWrite, h.putFile))
	mux.Handle("GET /api/v1/collections/{c}/files/{path...}", h.requireScope(auth.ScopeCollectionsRead, h.getFile))
	mux.Handle("PATCH /api/v1/collections/{c}/files/{path...}", h.requireScope(auth.ScopeCollectionsWrite, h.patchFile))
	mux.Handle("DELETE /api/v1/collections/{c}/files/{path...}", h.requireScope(auth.ScopeCollectionsWrite, h.deleteFile))

	return mux
}
