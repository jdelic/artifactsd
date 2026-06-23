package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/store"
)

// getFolder handles GET on a folder's ".../meta" sub-resource.
func (h *Handler) getFolder(w http.ResponseWriter, r *http.Request) {
	mp, isMeta := strings.CutSuffix(r.PathValue("path"), "/meta")
	if !isMeta {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "GET applies to .../meta only")
		return
	}
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	p, ok := h.cleanPathStr(w, mp)
	if !ok {
		return
	}

	own := json.RawMessage(`{}`)
	if node, err := h.store.GetNode(r.Context(), c.ID, p); err == nil {
		own = redactMetadata(node.Metadata)
	} else if !errors.Is(err, store.ErrNotFound) {
		h.internalServerError(w, "lookup folder", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": p, "own": own, "effective": h.effectiveMeta(r.Context(), c, p),
	})
}

// patchFolder sets/merges folder metadata, materializing the folder node.
func (h *Handler) patchFolder(w http.ResponseWriter, r *http.Request) {
	mp, isMeta := strings.CutSuffix(r.PathValue("path"), "/meta")
	if !isMeta {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "PATCH applies to .../meta only")
		return
	}
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	p, ok := h.cleanPathStr(w, mp)
	if !ok {
		return
	}

	existingMeta := json.RawMessage(`{}`)
	node, err := h.store.GetNode(r.Context(), c.ID, p)
	switch {
	case err == nil:
		if node.Kind != store.KindFolder {
			writeError(w, http.StatusConflict, CodeConflict, "path is a file, not a folder")
			return
		}
		existingMeta = node.Metadata
	case !errors.Is(err, store.ErrNotFound):
		h.internalServerError(w, "lookup folder", err)
		return
	}

	merged, ok := h.mergeMetadata(w, r, existingMeta)
	if !ok {
		return
	}
	parentID, err := h.ensureParents(r.Context(), c.ID, p)
	if err != nil {
		h.internalServerError(w, "materialize folders", err)
		return
	}
	folder := &store.Node{
		CollectionID: c.ID, ParentID: parentID, Path: p, Name: lastSegment(p),
		Kind: store.KindFolder, Metadata: merged,
	}
	if err := h.store.UpsertNode(r.Context(), folder); err != nil {
		h.internalServerError(w, "upsert folder", err)
		return
	}
	if err := h.store.RecomputeNodeDenormalized(r.Context(), folder.ID); err != nil {
		h.internalServerError(w, "recompute folder", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "own": redactMetadata(merged)})
}

// deleteFolder removes a folder node and its subtree (cascade).
func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	p, ok := h.cleanPathStr(w, r.PathValue("path"))
	if !ok {
		return
	}
	node, err := h.store.GetNode(r.Context(), c.ID, p)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "folder does not exist")
		return
	}
	if err != nil {
		h.internalServerError(w, "lookup folder", err)
		return
	}
	if node.Kind != store.KindFolder {
		writeError(w, http.StatusConflict, CodeConflict, "path is a file, not a folder")
		return
	}
	if err := h.store.DeleteNode(r.Context(), node.ID); err != nil {
		h.internalServerError(w, "delete folder", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{Action: audit.ActionFileDelete, Actor: actor(r), Target: c.Name + "/" + p})
	w.WriteHeader(http.StatusNoContent)
}

type treeNode struct {
	Path        string  `json:"path"`
	Kind        string  `json:"kind"`
	Size        *int64  `json:"size,omitempty"`
	ContentType *string `json:"content_type,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

// tree returns a structured listing under ?path= up to ?depth= (management view,
// not policy-filtered — design/04 §4).
func (h *Handler) tree(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	base := strings.Trim(r.URL.Query().Get("path"), "/")
	depth := parseDepth(r.URL.Query().Get("depth"))

	nodes, err := h.store.ListCollectionNodes(r.Context(), c.ID)
	if err != nil {
		h.internalServerError(w, "list nodes", err)
		return
	}
	items := make([]treeNode, 0, len(nodes))
	for _, n := range nodes {
		if !underBase(n.Path, base) {
			continue
		}
		if depth > 0 && relativeDepth(n.Path, base) > depth {
			continue
		}
		items = append(items, toTreeNode(n))
	}
	writeJSON(w, http.StatusOK, listResponse[treeNode]{Items: items})
}

func toTreeNode(n *store.Node) treeNode {
	tn := treeNode{Path: n.Path, Kind: n.Kind, Size: n.Size, ContentType: n.ContentType}
	if n.ExpiresAt != nil {
		s := n.ExpiresAt.UTC().Format(time.RFC3339)
		tn.ExpiresAt = &s
	}
	return tn
}

// underBase reports whether path is at or below base ("" = collection root).
func underBase(path, base string) bool {
	if base == "" {
		return true
	}
	return path == base || strings.HasPrefix(path, base+"/")
}

// relativeDepth is the number of path segments below base.
func relativeDepth(path, base string) int {
	rest := path
	if base != "" {
		rest = strings.TrimPrefix(strings.TrimPrefix(path, base), "/")
	}
	if rest == "" {
		return 0
	}
	return strings.Count(rest, "/") + 1
}

func parseDepth(s string) int {
	if s == "" {
		return 0 // unlimited
	}
	n := parseLimit(s)
	if s == "0" {
		return 0
	}
	return n
}
