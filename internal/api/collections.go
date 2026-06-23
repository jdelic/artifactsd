package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/store"
)

// collectionView is the API representation of a collection (metadata redacted).
type collectionView struct {
	Name       string          `json:"name"`
	Metadata   json.RawMessage `json:"metadata"`
	Visibility string          `json:"visibility"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
	ExpiresAt  *string         `json:"expires_at,omitempty"`
}

func toCollectionView(c *store.Collection) collectionView {
	v := collectionView{
		Name:       c.Name,
		Metadata:   redactMetadata(c.Metadata),
		Visibility: c.Visibility,
		CreatedAt:  c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:  c.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if c.ExpiresAt != nil {
		s := c.ExpiresAt.UTC().Format(time.RFC3339)
		v.ExpiresAt = &s
	}
	return v
}

// redactMetadata parses, redacts secrets from, and re-serializes a metadata doc.
func redactMetadata(raw json.RawMessage) json.RawMessage {
	doc, err := meta.FromJSON(raw)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	out, err := doc.Redacted().JSON()
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

type createCollectionRequest struct {
	Name     string          `json:"name"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

func (h *Handler) createCollection(w http.ResponseWriter, r *http.Request) {
	var req createCollectionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := paths.ValidateCollectionName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return
	}
	metaJSON, ok := h.prepareMetadata(w, req.Metadata)
	if !ok {
		return
	}

	ctx := r.Context()
	if _, err := h.store.GetCollectionByName(ctx, req.Name); err == nil {
		writeError(w, http.StatusConflict, CodeConflict, "collection already exists")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		h.internalServerError(w, "lookup collection", err)
		return
	}

	c := &store.Collection{Name: req.Name, Metadata: metaJSON}
	if err := h.store.CreateCollection(ctx, c); err != nil {
		h.internalServerError(w, "create collection", err)
		return
	}
	if err := h.store.RecomputeCollectionDenormalized(ctx, c.ID, string(h.cfg.Policy.Visibility)); err != nil {
		h.internalServerError(w, "recompute collection", err)
		return
	}
	c, _ = h.store.GetCollection(ctx, c.ID)
	_ = h.audit.Record(ctx, &audit.Entry{
		Action: audit.ActionCollectionCreate, Actor: actor(r), Target: c.Name,
	})
	writeJSON(w, http.StatusCreated, toCollectionView(c))
}

func (h *Handler) listCollections(w http.ResponseWriter, r *http.Request) {
	cols, err := h.store.ListCollections(r.Context())
	if err != nil {
		h.internalServerError(w, "list collections", err)
		return
	}
	cursor := r.URL.Query().Get("cursor")
	limit := parseLimit(r.URL.Query().Get("limit"))

	items := make([]collectionView, 0, len(cols))
	var next string
	for _, c := range cols {
		if cursor != "" && c.Name <= cursor {
			continue
		}
		if len(items) == limit {
			next = items[len(items)-1].Name
			break
		}
		items = append(items, toCollectionView(c))
	}
	writeJSON(w, http.StatusOK, listResponse[collectionView]{Items: items, NextCursor: next})
}

func (h *Handler) getCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toCollectionView(c))
}

func (h *Handler) patchCollectionMeta(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	merged, ok := h.mergeMetadata(w, r, c.Metadata)
	if !ok {
		return
	}
	c.Metadata = merged
	if err := h.store.UpdateCollectionMetadata(r.Context(), c); err != nil {
		h.internalServerError(w, "update collection", err)
		return
	}
	if err := h.store.RecomputeCollectionDenormalized(r.Context(), c.ID, string(h.cfg.Policy.Visibility)); err != nil {
		h.internalServerError(w, "recompute collection", err)
		return
	}
	c, _ = h.store.GetCollection(r.Context(), c.ID)
	writeJSON(w, http.StatusOK, toCollectionView(c))
}

func (h *Handler) deleteCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteCollection(r.Context(), c.ID); err != nil {
		h.internalServerError(w, "delete collection", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionCollectionDelete, Actor: actor(r), Target: c.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

// lookupCollection resolves {c}; writes 404 and returns false if absent.
func (h *Handler) lookupCollection(w http.ResponseWriter, r *http.Request) (*store.Collection, bool) {
	name := r.PathValue("c")
	c, err := h.store.GetCollectionByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "collection does not exist")
		return nil, false
	}
	if err != nil {
		h.internalServerError(w, "lookup collection", err)
		return nil, false
	}
	return c, true
}
