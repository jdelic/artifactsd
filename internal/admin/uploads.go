package admin

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// uploadFile handles the small-file upload form: a direct multipart POST,
// stored synchronously (design/09 §4). The body is already capped at
// ARTIFACTSD_MAX_UPLOAD_SIZE by the server's global body-limit middleware.
func (h *Handler) uploadFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	dir := r.FormValue("dir")

	file, header, err := r.FormFile("file")
	if err != nil {
		h.renderTreePanel(w, r, c, dir, "no file selected")
		return
	}
	defer func() { _ = file.Close() }()

	target := path.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if dir != "" {
		target = dir + "/" + target
	}
	p, err := paths.Clean(target)
	if err != nil {
		h.renderTreePanel(w, r, c, dir, "invalid filename: "+err.Error())
		return
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		if guess := mime.TypeByExtension(path.Ext(p)); guess != "" {
			contentType = guess
		}
	}

	ctx := r.Context()
	ref, size, sum, err := h.blob.Put(ctx, file)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			h.renderTreePanel(w, r, c, dir, "upload exceeds the size limit")
			return
		}
		h.renderTreePanel(w, r, c, dir, "storing file: "+err.Error())
		return
	}
	parentID, err := h.ensureParents(ctx, c.ID, p)
	if err != nil {
		h.renderTreePanel(w, r, c, dir, "materializing folders: "+err.Error())
		return
	}
	refStr := string(ref)
	node := &store.Node{
		CollectionID: c.ID, ParentID: parentID, Path: p, Name: lastSegment(p), Kind: store.KindFile,
		BlobID: &refStr, Size: &size, ContentType: strPtrOrNil(contentType), Checksum: &sum,
		Metadata: json.RawMessage(`{}`),
	}
	if err := h.store.UpsertNode(ctx, node); err != nil {
		h.renderTreePanel(w, r, c, dir, "saving file: "+err.Error())
		return
	}
	if err := h.store.RecomputeNodeDenormalized(ctx, node.ID); err != nil {
		h.renderTreePanel(w, r, c, dir, "saving file: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionFilePut, Actor: actorOf(id), Target: c.Name + "/" + p})
	}
	h.renderTreePanel(w, r, c, dir, "")
}

// beginUploadRequest is the JSON body of POST .../upload/begin.
type beginUploadRequest struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type,omitempty"`
}

// beginUploadResponse mirrors the management API's upload-session response
// (design/04 §5) so the same client-side PUT-with-progress flow applies.
type beginUploadResponse struct {
	UploadURL string `json:"upload_url"`
	UploadID  string `json:"upload_id"`
	Method    string `json:"method"`
	ExpiresAt string `json:"expires_at"`
}

// beginUpload starts a large-upload session for the browser to stream bytes
// to directly (design/09 §4): the returned upload_url is the capability
// endpoint at /api/v1/uploads/{id}, which needs no further admin auth.
func (h *Handler) beginUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	var req beginUploadRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	p, err := paths.Clean(req.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid path: "+err.Error())
		return
	}

	uploadID, err := auth.GenerateUploadID()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "generating upload id: "+err.Error())
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	sess := &store.UploadSession{
		UploadID: uploadID, CollectionID: c.ID, TargetPath: p, Metadata: json.RawMessage(`{}`),
		ContentType: strPtrOrNil(req.ContentType), Status: store.UploadPending,
		ExpiresAt: now.Add(h.cfg.Limits.UploadTTL),
	}
	if err := h.store.CreateUploadSession(ctx, sess); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "creating upload session: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionUploadBegin, Actor: actorOf(id), Target: c.Name + "/" + p})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(beginUploadResponse{
		UploadURL: strings.TrimRight(h.cfg.Server.BaseURL, "/") + "/api/v1/uploads/" + uploadID,
		UploadID:  uploadID,
		Method:    http.MethodPut,
		ExpiresAt: sess.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// ensureParents materializes folder nodes for each ancestor of p, returning
// the immediate parent's id (nil at collection root) — mirrors design/07 §4
// ("always create parent folder rows on upload"); duplicated from
// internal/api since that package's helper is unexported and the admin UI
// goes through the store directly rather than looping back through the API.
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

func lastSegment(p string) string { return path.Base(p) }

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
