package api

import (
	"encoding/json"
	"fmt"
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

// parseDepthParam parses ?depth= for the tree and crawl endpoints (design/04
// §4, design/05 §5): omitted or 0 returns the full tree; a positive value
// limits how many levels below the base path are returned; anything else is
// rejected (the caller maps the error to a 400).
func parseDepthParam(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid depth %q: want a non-negative integer", s)
	}
	return n, nil
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
func (h *Handler) internalServerError(w http.ResponseWriter, what string, err error) {
	if h.log != nil {
		h.log.Error("api: "+what, "error", err)
	}
	writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
}

// prepareMetadata validates a fresh metadata document, hashes its secrets, and
// returns the storage JSON. Writes a 400 and returns false on invalid input.
func (h *Handler) prepareMetadata(w http.ResponseWriter, r *http.Request, raw json.RawMessage) (json.RawMessage, bool) {
	doc, err := meta.FromJSON(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, false
	}
	if err = doc.ValidateWith(h.metaPolicy); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, false
	}
	if !requireSandboxPrivilege(w, r, doc) {
		return nil, false
	}
	if err = doc.HashSecrets(); err != nil {
		h.internalServerError(w, "hash secrets", err)
		return nil, false
	}
	out, err := doc.JSON()
	if err != nil {
		h.internalServerError(w, "encode metadata", err)
		return nil, false
	}
	return out, true
}

// mergeMetadata decodes a patch document from the body, validates+hashes it, and
// merges it onto the existing stored metadata, returning the storage JSON and
// the patch itself.
func (h *Handler) mergeMetadata(w http.ResponseWriter, r *http.Request, existing json.RawMessage) (json.RawMessage, *meta.Document, bool) {
	var raw json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return nil, nil, false
	}
	patch, err := meta.FromJSON(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, nil, false
	}
	if err = patch.ValidateWith(h.metaPolicy); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, nil, false
	}
	if !requireSandboxPrivilege(w, r, patch) {
		return nil, nil, false
	}
	if err = patch.HashSecrets(); err != nil {
		h.internalServerError(w, "hash secrets", err)
		return nil, nil, false
	}

	base, err := meta.FromJSON(existing)
	if err != nil {
		h.internalServerError(w, "parse existing metadata", err)
		return nil, nil, false
	}
	base.Merge(patch)
	out, err := base.JSON()
	if err != nil {
		h.internalServerError(w, "encode metadata", err)
		return nil, nil, false
	}
	return out, patch, true
}

// requireSandboxPrivilege rejects (403) a metadata document that sets
// content.sandbox — to true or false — unless the caller holds an admin-scoped
// token (artifactsd-m9e.4, artifactsd-7tl.2). Unsandboxed HTML runs with full
// same-origin power, so only admins decide where the sandbox applies. A
// document that leaves sandbox unset is always allowed. Every metadata helper
// calls it, so no handler can forget.
func requireSandboxPrivilege(w http.ResponseWriter, r *http.Request, doc *meta.Document) bool {
	if sandboxSetting(doc) == nil || isAdmin(r) {
		return true
	}
	writeError(w, http.StatusForbidden, CodeForbidden, "content.sandbox can only be set with an admin-scoped token")
	return false
}

// sandboxSetting returns the document's content.sandbox, or nil when unset.
func sandboxSetting(doc *meta.Document) *bool {
	if doc == nil || doc.Content == nil {
		return nil
	}
	return doc.Content.Sandbox
}

// setsSandboxOff reports whether doc explicitly sets content.sandbox=false.
func setsSandboxOff(doc *meta.Document) bool {
	sb := sandboxSetting(doc)
	return sb != nil && !*sb
}

// vouchIfUnsandboxed records an admin's decision to unsandbox: when doc sets
// content.sandbox=false, the files currently at or below prefix are marked
// trusted, so they are served unsandboxed without being re-uploaded
// (artifactsd-7tl.2). Only admins can reach this (requireSandboxPrivilege).
func (h *Handler) vouchIfUnsandboxed(r *http.Request, doc *meta.Document, collectionID int64, prefix string) error {
	if !setsSandboxOff(doc) || !isAdmin(r) {
		return nil
	}
	return h.store.TrustContent(r.Context(), collectionID, prefix)
}

// keepAdminSandbox copies the existing document's content.sandbox into doc
// when doc leaves it unset: a non-admin replacing an object's metadata must
// not drop a sandbox decision only an admin may make (artifactsd-7tl.2).
func keepAdminSandbox(doc *meta.Document, existing json.RawMessage) {
	if sandboxSetting(doc) != nil {
		return
	}
	old, err := meta.FromJSON(existing)
	if err != nil || sandboxSetting(old) == nil {
		return
	}
	if doc.Content == nil {
		doc.Content = &meta.Content{}
	}
	v := *old.Content.Sandbox
	doc.Content.Sandbox = &v
}
