package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/store"
)

// metadataHeader carries a base64url(JSON) metadata document on binary uploads.
const metadataHeader = "Artifactsd-Metadata"

type fileView struct {
	Collection  string  `json:"collection"`
	Path        string  `json:"path"`
	Size        int64   `json:"size"`
	ContentType string  `json:"content_type,omitempty"`
	Checksum    string  `json:"checksum,omitempty"`
	URL         string  `json:"url"`
	UpdatedAt   string  `json:"updated_at"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

func (h *Handler) putFile(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	p, ok := h.cleanPathStr(w, r.PathValue("path"))
	if !ok {
		return
	}

	doc, ok := h.uploadMetadata(w, r)
	if !ok {
		return
	}
	contentType := resolveContentType(docContentType(doc), r)
	metaJSON, err := doc.JSON()
	if err != nil {
		h.internalServerError(w, "encode metadata", err)
		return
	}

	ref, size, sum, err := h.blob.Put(r.Context(), r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, CodeTooLarge, "upload exceeds the size limit")
			return
		}
		h.internalServerError(w, "store blob", err)
		return
	}

	parentID, err := h.ensureParents(r.Context(), c.ID, p)
	if err != nil {
		h.internalServerError(w, "materialize folders", err)
		return
	}

	existing, _ := h.store.GetNode(r.Context(), c.ID, p)
	refStr := string(ref)
	node := &store.Node{
		CollectionID: c.ID, ParentID: parentID, Path: p, Name: lastSegment(p), Kind: store.KindFile,
		BlobID: &refStr, Size: &size, ContentType: strPtrOrNil(contentType), Checksum: &sum, Metadata: metaJSON,
	}
	status := http.StatusCreated
	if existing != nil {
		status = http.StatusOK
	}
	if err := h.store.UpsertNode(r.Context(), node); err != nil {
		h.internalServerError(w, "upsert node", err)
		return
	}
	if err := h.store.RecomputeNodeDenormalized(r.Context(), node.ID); err != nil {
		h.internalServerError(w, "recompute node", err)
		return
	}
	node, _ = h.store.GetNodeByID(r.Context(), node.ID)

	_ = h.audit.Record(r.Context(), &audit.Entry{Action: audit.ActionFilePut, Actor: actor(r), Target: c.Name + "/" + p})
	writeJSON(w, status, h.toFileView(c.Name, node))
}

// getFile dispatches GET to either a metadata read (".../meta" suffix) or a
// file download.
func (h *Handler) getFile(w http.ResponseWriter, r *http.Request) {
	if mp, isMeta := strings.CutSuffix(r.PathValue("path"), "/meta"); isMeta {
		h.serveFileMeta(w, r, mp)
		return
	}
	h.downloadFile(w, r)
}

func (h *Handler) downloadFile(w http.ResponseWriter, r *http.Request) {
	node, _, ok := h.resolveFile(w, r, r.PathValue("path"))
	if !ok {
		return
	}
	if node.BlobID == nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not a file")
		return
	}
	rc, err := h.blob.Open(r.Context(), blob.Ref(*node.BlobID))
	if err != nil {
		h.internalServerError(w, "open blob", err)
		return
	}
	defer func() { _ = rc.Close() }()

	if node.ContentType != nil && *node.ContentType != "" {
		w.Header().Set("Content-Type", *node.ContentType)
	}
	if node.Checksum != nil {
		w.Header().Set("ETag", `"`+*node.Checksum+`"`)
	}
	http.ServeContent(w, r, node.Name, node.UpdatedAt, rc)
}

func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	node, c, ok := h.resolveFile(w, r, r.PathValue("path"))
	if !ok {
		return
	}
	if err := h.store.DeleteNode(r.Context(), node.ID); err != nil {
		h.internalServerError(w, "delete node", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{Action: audit.ActionFileDelete, Actor: actor(r), Target: c.Name + "/" + node.Path})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) serveFileMeta(w http.ResponseWriter, r *http.Request, filePath string) {
	node, c, ok := h.resolveFile(w, r, filePath)
	if !ok {
		return
	}
	own := redactMetadata(node.Metadata)
	eff := h.effectiveMeta(r.Context(), c, node.Path)
	writeJSON(w, http.StatusOK, map[string]any{"path": node.Path, "own": own, "effective": eff})
}

// patchFile requires the ".../meta" suffix (only metadata is patchable).
func (h *Handler) patchFile(w http.ResponseWriter, r *http.Request) {
	mp, isMeta := strings.CutSuffix(r.PathValue("path"), "/meta")
	if !isMeta {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "PATCH applies to .../meta only")
		return
	}
	node, _, ok := h.resolveFile(w, r, mp)
	if !ok {
		return
	}
	merged, ok := h.mergeMetadata(w, r, node.Metadata)
	if !ok {
		return
	}
	node.Metadata = merged
	if err := h.store.UpsertNode(r.Context(), node); err != nil {
		h.internalServerError(w, "update node", err)
		return
	}
	if err := h.store.RecomputeNodeDenormalized(r.Context(), node.ID); err != nil {
		h.internalServerError(w, "recompute node", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": node.Path, "own": redactMetadata(merged)})
}

// --- helpers ---------------------------------------------------------------

func (h *Handler) cleanPathStr(w http.ResponseWriter, raw string) (string, bool) {
	p, err := paths.Clean(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid path: "+err.Error())
		return "", false
	}
	return p, true
}

// resolveFile resolves the collection and file node for rawPath, writing 404 if
// absent.
func (h *Handler) resolveFile(w http.ResponseWriter, r *http.Request, rawPath string) (*store.Node, *store.Collection, bool) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return nil, nil, false
	}
	p, ok := h.cleanPathStr(w, rawPath)
	if !ok {
		return nil, nil, false
	}
	node, err := h.store.GetNode(r.Context(), c.ID, p)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "file does not exist")
		return nil, nil, false
	}
	if err != nil {
		h.internalServerError(w, "lookup node", err)
		return nil, nil, false
	}
	return node, c, true
}

// uploadMetadata parses the optional Artifactsd-Metadata header, validating and
// hashing its secrets.
func (h *Handler) uploadMetadata(w http.ResponseWriter, r *http.Request) (*meta.Document, bool) {
	raw := r.Header.Get(metadataHeader)
	if raw == "" {
		return &meta.Document{}, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(raw, "="))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid "+metadataHeader+" encoding")
		return nil, false
	}
	doc, err := meta.FromJSON(decoded)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, false
	}
	if err := doc.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return nil, false
	}
	if err := doc.HashSecrets(); err != nil {
		h.internalServerError(w, "hash secrets", err)
		return nil, false
	}
	return doc, true
}

// ensureParents materializes folder nodes for each ancestor of p, returning the
// immediate parent's id (nil at collection root). Design/07 §4: always create
// parent folder rows on upload.
func (h *Handler) ensureParents(ctx context.Context, collectionID int64, p string) (*int64, error) {
	segs := strings.Split(p, "/")
	if len(segs) == 1 {
		return nil, nil
	}
	var parentID *int64
	prefix := ""
	for _, seg := range segs[:len(segs)-1] {
		if prefix == "" {
			prefix = seg
		} else {
			prefix += "/" + seg
		}
		existing, err := h.store.GetNode(ctx, collectionID, prefix)
		if err == nil {
			parentID = &existing.ID
			continue
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		folder := &store.Node{
			CollectionID: collectionID, ParentID: parentID, Path: prefix,
			Name: lastSegment(prefix), Kind: store.KindFolder, Metadata: json.RawMessage(`{}`),
		}
		if err := h.store.UpsertNode(ctx, folder); err != nil {
			return nil, err
		}
		id := folder.ID
		parentID = &id
	}
	return parentID, nil
}

func (h *Handler) toFileView(collection string, n *store.Node) fileView {
	v := fileView{
		Collection: collection, Path: n.Path, URL: h.fileURL(collection, n.Path),
		UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if n.Size != nil {
		v.Size = *n.Size
	}
	if n.ContentType != nil {
		v.ContentType = *n.ContentType
	}
	if n.Checksum != nil {
		v.Checksum = *n.Checksum
	}
	if n.ExpiresAt != nil {
		s := n.ExpiresAt.UTC().Format(time.RFC3339)
		v.ExpiresAt = &s
	}
	return v
}

func (h *Handler) fileURL(collection, p string) string {
	return strings.TrimRight(h.baseURL, "/") + "/c/" + collection + "/" + p
}

// docContentType extracts a metadata document's explicit content type, if any.
func docContentType(doc *meta.Document) *string {
	if doc.Content == nil {
		return nil
	}
	return doc.Content.ContentType
}

// resolveContentType prefers an explicit metadata content type over the
// request's Content-Type header (ignoring the Go HTTP client's generic
// default), shared by the direct-PUT and large-upload paths (design/07 §2/§3).
func resolveContentType(metaCT *string, r *http.Request) string {
	if metaCT != nil && *metaCT != "" {
		return *metaCT
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && ct != "application/octet-stream" {
		return ct
	}
	return ""
}

func lastSegment(p string) string { return path.Base(p) }

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// effectiveMeta resolves inheritance for a node path into a serializable view.
func (h *Handler) effectiveMeta(ctx context.Context, c *store.Collection, nodePath string) any {
	layers := []*meta.Document{docOrNil(c.Metadata)}
	prefix := ""
	for _, seg := range strings.Split(nodePath, "/") {
		if prefix == "" {
			prefix = seg
		} else {
			prefix += "/" + seg
		}
		if n, err := h.store.GetNode(ctx, c.ID, prefix); err == nil {
			layers = append(layers, docOrNil(n.Metadata))
		}
	}
	eff := meta.Resolve(meta.Defaults{
		Visibility:       string(h.cfg.Policy.Visibility),
		DirectoryListing: h.cfg.Policy.DirectoryListing,
		Crawlable:        h.cfg.Policy.Crawlable,
		Indexable:        h.cfg.Policy.Indexable,
	}, layers...)

	authView := make([]map[string]any, 0, len(eff.Auth))
	for i := range eff.Auth {
		authView = append(authView, map[string]any{"type": eff.Auth[i].Type, "configured": true})
	}
	return map[string]any{
		"visibility":        eff.Visibility,
		"directory_listing": eff.DirectoryListing,
		"content_type":      eff.ContentType,
		"headers":           eff.Headers,
		"crawlable":         eff.Crawlable,
		"indexable":         eff.Indexable,
		"auth":              authView,
	}
}

func docOrNil(raw json.RawMessage) *meta.Document {
	d, err := meta.FromJSON(raw)
	if err != nil {
		return nil
	}
	return d
}
