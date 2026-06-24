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

	// Folders & tree (design/04 §4).
	mux.Handle("GET /api/v1/collections/{c}/tree", h.requireScope(auth.ScopeCollectionsRead, h.tree))
	mux.Handle("GET /api/v1/collections/{c}/folders/{path...}", h.requireScope(auth.ScopeCollectionsRead, h.getFolder))
	mux.Handle("PATCH /api/v1/collections/{c}/folders/{path...}", h.requireScope(auth.ScopeCollectionsWrite, h.patchFolder))
	mux.Handle("DELETE /api/v1/collections/{c}/folders/{path...}", h.requireScope(auth.ScopeCollectionsWrite, h.deleteFolder))

	// Shares (design/04 §6).
	mux.Handle("POST /api/v1/collections/{c}/shares", h.requireScope(auth.ScopeSharesWrite, h.createShare))
	mux.Handle("GET /api/v1/collections/{c}/shares", h.requireScope(auth.ScopeSharesRead, h.listShares))
	mux.Handle("GET /api/v1/shares/{share_id}", h.requireScope(auth.ScopeSharesRead, h.getShare))
	mux.Handle("PATCH /api/v1/shares/{share_id}", h.requireScope(auth.ScopeSharesWrite, h.patchShare))
	mux.Handle("DELETE /api/v1/shares/{share_id}", h.requireScope(auth.ScopeSharesWrite, h.deleteShare))

	// Tokens, audit, stats & maintenance (design/04 §8, admin-scoped).
	mux.Handle("GET /api/v1/tokens", h.requireScope(auth.ScopeAdmin, h.listTokens))
	mux.Handle("POST /api/v1/tokens", h.requireScope(auth.ScopeAdmin, h.createToken))
	mux.Handle("DELETE /api/v1/tokens/{id}", h.requireScope(auth.ScopeAdmin, h.deleteToken))
	mux.Handle("GET /api/v1/audit", h.requireScope(auth.ScopeAdmin, h.getAudit))
	mux.Handle("GET /api/v1/stats", h.requireScope(auth.ScopeAdmin, h.getStats))
	mux.Handle("POST /api/v1/maintenance/cleanup", h.requireScope(auth.ScopeAdmin, h.postMaintenanceCleanup))

	return mux
}
