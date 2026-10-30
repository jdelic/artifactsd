package admin

import (
	"encoding/json"
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

// beginUpload starts an upload session for the browser to stream bytes
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
	// Browsers send an empty or generic type for extensions they don't know;
	// fall back to the extension so such files are not stored untyped.
	if req.ContentType == "" || req.ContentType == "application/octet-stream" {
		if guess := mime.TypeByExtension(path.Ext(p)); guess != "" {
			req.ContentType = guess
		}
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

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
