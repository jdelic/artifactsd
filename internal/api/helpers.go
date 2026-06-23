package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/meta"
)

const (
	defaultPageLimit = 100
	maxPageLimit     = 1000
)

// listResponse is the paginated list envelope (design/04 §1).
type listResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// parseLimit parses ?limit, clamped to [1, maxPageLimit] with a default.
func parseLimit(s string) int {
	if s == "" {
		return defaultPageLimit
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return defaultPageLimit
	}
	if n > maxPageLimit {
		return maxPageLimit
	}
	return n
}

// actor returns the audit actor for the authenticated API caller (the token
// name; never the secret).
func actor(r *http.Request) string {
	if p := auth.PrincipalFromContext(r.Context()); p != nil {
		if p.Name != "" {
			return p.Name
		}
		return p.Prefix
	}
	return ""
}

// internal logs an unexpected error and returns a 500 envelope.
func (h *Handler) internal(w http.ResponseWriter, what string, err error) {
	if h.log != nil {
		h.log.Error("api: "+what, "error", err)
	}
	writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
}

// prepareMetadata validates a fresh metadata document, hashes its secrets, and
// returns the storage JSON. Writes a 400 and returns false on invalid input.
func (h *Handler) prepareMetadata(w http.ResponseWriter, raw json.RawMessage) (json.RawMessage, bool) {
	doc, err := meta.FromJSON(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, false
	}
	if err = doc.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, false
	}
	if err = doc.HashSecrets(); err != nil {
		h.internal(w, "hash secrets", err)
		return nil, false
	}
	out, err := doc.JSON()
	if err != nil {
		h.internal(w, "encode metadata", err)
		return nil, false
	}
	return out, true
}

// mergeMetadata decodes a patch document from the body, validates+hashes it, and
// merges it onto the existing stored metadata, returning the storage JSON.
func (h *Handler) mergeMetadata(w http.ResponseWriter, r *http.Request, existing json.RawMessage) (json.RawMessage, bool) {
	var raw json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return nil, false
	}
	patch, err := meta.FromJSON(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, false
	}
	if err = patch.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, false
	}
	if err = patch.HashSecrets(); err != nil {
		h.internal(w, "hash secrets", err)
		return nil, false
	}

	base, err := meta.FromJSON(existing)
	if err != nil {
		h.internal(w, "parse existing metadata", err)
		return nil, false
	}
	base.Merge(patch)
	out, err := base.JSON()
	if err != nil {
		h.internal(w, "encode metadata", err)
		return nil, false
	}
	return out, true
}
