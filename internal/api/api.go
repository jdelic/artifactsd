// Package api implements the artifactsd management API under /api/v1 (design/04):
// JSON contracts, a consistent error envelope, scope-gated auth, and handlers
// for collections, files, and metadata.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/store"
)

// Error codes (design/04 §1).
const (
	CodeBadRequest   = "bad_request"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeTooLarge     = "payload_too_large"
	CodeUnsupported  = "unsupported_media_type"
	CodeRateLimited  = "rate_limited"
	CodeInternal     = "internal"
)

// Deps are the handler's dependencies.
type Deps struct {
	Store   *store.Store
	Blob    blob.Store
	Config  *config.Config
	Audit   *audit.Recorder
	Authn   *auth.Authenticator
	Logger  *slog.Logger
	BaseURL string
}

// Handler serves the management API.
type Handler struct {
	store   *store.Store
	blob    blob.Store
	cfg     *config.Config
	audit   *audit.Recorder
	authn   *auth.Authenticator
	log     *slog.Logger
	baseURL string
}

// New builds the API handler.
func New(d Deps) *Handler {
	return &Handler{
		store:   d.Store,
		blob:    d.Blob,
		cfg:     d.Config,
		audit:   d.Audit,
		authn:   d.Authn,
		log:     d.Logger,
		baseURL: d.BaseURL,
	}
}

// errorBody is the JSON error envelope.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError emits the standard error envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// writeJSON writes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// onAuthError adapts auth failures to the API error envelope (used by the scope
// middleware).
func onAuthError(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
	writeError(w, status, code, message)
}

// requireScope wraps a handler with API-token scope enforcement.
func (h *Handler) requireScope(scope auth.Scope, fn http.HandlerFunc) http.Handler {
	return h.authn.Middleware(scope, onAuthError)(fn)
}

// decodeJSON reads a JSON body into v with a sane size cap, rejecting unknown
// fields. Returns false (after writing an error) on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
