package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/store"
)

// shareView is the API representation of a share (metadata redacted, design/04 §6).
type shareView struct {
	ShareID    string          `json:"share_id"`
	Collection string          `json:"collection"`
	TargetPath string          `json:"target_path"`
	TargetKind string          `json:"target_kind"`
	Metadata   json.RawMessage `json:"metadata"`
	URL        string          `json:"url"`
	CreatedAt  string          `json:"created_at"`
	ExpiresAt  *string         `json:"expires_at,omitempty"`
	RevokedAt  *string         `json:"revoked_at,omitempty"`
}

func (h *Handler) toShareView(collection string, sh *store.Share) shareView {
	v := shareView{
		ShareID:    sh.ShareID,
		Collection: collection,
		TargetPath: sh.TargetPath,
		TargetKind: sh.TargetKind,
		Metadata:   redactMetadata(sh.Metadata),
		URL:        h.shareURL(sh.ShareID),
		CreatedAt:  sh.CreatedAt.UTC().Format(time.RFC3339),
	}
	if sh.ExpiresAt != nil {
		s := sh.ExpiresAt.UTC().Format(time.RFC3339)
		v.ExpiresAt = &s
	}
	if sh.RevokedAt != nil {
		s := sh.RevokedAt.UTC().Format(time.RFC3339)
		v.RevokedAt = &s
	}
	return v
}

// shareURL builds the public /s/{share_id}/ URL for a share.
func (h *Handler) shareURL(shareID string) string {
	return strings.TrimRight(h.baseURL, "/") + "/s/" + shareID + "/"
}

type createShareRequest struct {
	TargetPath string          `json:"target_path"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// createShare mints a share for a collection, folder, or file (design/04 §6).
func (h *Handler) createShare(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	var req createShareRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	targetPath, targetKind, ok := h.resolveShareTarget(w, r, c, req.TargetPath)
	if !ok {
		return
	}
	metaJSON, ok := h.prepareMetadata(w, req.Metadata)
	if !ok {
		return
	}

	shareID, err := auth.GenerateShareID()
	if err != nil {
		h.internalServerError(w, "generate share id", err)
		return
	}
	sh := &store.Share{
		ShareID: shareID, CollectionID: c.ID, TargetPath: targetPath,
		TargetKind: targetKind, Metadata: metaJSON,
	}
	if err := h.store.CreateShare(r.Context(), sh); err != nil {
		h.internalServerError(w, "create share", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionShareCreate, Actor: actor(r), Target: c.Name + " " + shareID,
	})
	writeJSON(w, http.StatusCreated, h.toShareView(c.Name, sh))
}

// listShares lists a collection's shares (design/04 §6).
func (h *Handler) listShares(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	shares, err := h.store.ListSharesByCollection(r.Context(), c.ID)
	if err != nil {
		h.internalServerError(w, "list shares", err)
		return
	}
	items := make([]shareView, 0, len(shares))
	for _, sh := range shares {
		items = append(items, h.toShareView(c.Name, sh))
	}
	writeJSON(w, http.StatusOK, listResponse[shareView]{Items: items})
}

// getShare returns one share by its public share_id (design/04 §6).
func (h *Handler) getShare(w http.ResponseWriter, r *http.Request) {
	sh, c, ok := h.lookupShare(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.toShareView(c.Name, sh))
}

// patchShare merges a share's policy/expiry metadata (design/04 §6).
func (h *Handler) patchShare(w http.ResponseWriter, r *http.Request) {
	sh, c, ok := h.lookupShare(w, r)
	if !ok {
		return
	}
	merged, ok := h.mergeMetadata(w, r, sh.Metadata)
	if !ok {
		return
	}
	sh.Metadata = merged
	if err := h.store.UpdateShare(r.Context(), sh); err != nil {
		h.internalServerError(w, "update share", err)
		return
	}
	sh, err := h.store.GetShare(r.Context(), sh.ShareID)
	if err != nil {
		h.internalServerError(w, "reload share", err)
		return
	}
	writeJSON(w, http.StatusOK, h.toShareView(c.Name, sh))
}

// deleteShare revokes and removes a share (design/04 §6).
func (h *Handler) deleteShare(w http.ResponseWriter, r *http.Request) {
	sh, c, ok := h.lookupShare(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteShare(r.Context(), sh.ShareID); err != nil {
		h.internalServerError(w, "delete share", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionShareRevoke, Actor: actor(r), Target: c.Name + " " + sh.ShareID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---------------------------------------------------------------

// resolveShareTarget cleans the requested target path and resolves its kind. An
// empty path shares the whole collection; otherwise the target node must exist.
func (h *Handler) resolveShareTarget(w http.ResponseWriter, r *http.Request, c *store.Collection, raw string) (path, kind string, ok bool) {
	if strings.Trim(raw, "/") == "" {
		return "", "collection", true
	}
	p, ok := h.cleanPathStr(w, raw)
	if !ok {
		return "", "", false
	}
	node, err := h.store.GetNode(r.Context(), c.ID, p)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "share target does not exist")
		return "", "", false
	}
	if err != nil {
		h.internalServerError(w, "lookup share target", err)
		return "", "", false
	}
	return p, node.Kind, true
}

// lookupShare resolves {share_id} and its collection; writes 404 and returns
// false if absent.
func (h *Handler) lookupShare(w http.ResponseWriter, r *http.Request) (*store.Share, *store.Collection, bool) {
	sh, err := h.store.GetShare(r.Context(), r.PathValue("share_id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "share does not exist")
		return nil, nil, false
	}
	if err != nil {
		h.internalServerError(w, "lookup share", err)
		return nil, nil, false
	}
	c, err := h.store.GetCollection(r.Context(), sh.CollectionID)
	if err != nil {
		h.internalServerError(w, "lookup share collection", err)
		return nil, nil, false
	}
	return sh, c, true
}
