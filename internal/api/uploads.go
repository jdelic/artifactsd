package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/store"
)

// beginUploadRequest is the body of POST .../uploads (design/04 §5).
type beginUploadRequest struct {
	Path     string          `json:"path"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// uploadSessionView is the response to POST .../uploads.
type uploadSessionView struct {
	UploadURL string `json:"upload_url"`
	UploadID  string `json:"upload_id"`
	Method    string `json:"method"`
	ExpiresAt string `json:"expires_at"`
}

// uploadStatusView is the response to GET /api/v1/uploads/{upload_id}.
type uploadStatusView struct {
	UploadID    string  `json:"upload_id"`
	Collection  string  `json:"collection"`
	Path        string  `json:"path"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   string  `json:"expires_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
}

// uploadURL builds the capability URL for an upload session (design/04 §5).
func (h *Handler) uploadURL(uploadID string) string {
	return strings.TrimRight(h.baseURL, "/") + "/api/v1/uploads/" + uploadID
}

// createUploadSession begins a large-upload session: validates the target
// path and metadata up front, then mints an unguessable upload_id whose
// possession is the sole capability needed to PUT the bytes (design/04 §5,
// design/07 §3, design/14 §5).
func (h *Handler) createUploadSession(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	var req beginUploadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, ok := h.cleanPathStr(w, req.Path)
	if !ok {
		return
	}
	metaJSON, ok := h.prepareMetadata(w, r, req.Metadata)
	if !ok {
		return
	}
	// An omitted metadata document keeps the target file's metadata when the
	// upload replaces a file (artifactsd-m9e.3).
	supplied := len(req.Metadata) > 0 && string(req.Metadata) != "null"

	doc, err := meta.FromJSON(metaJSON)
	if err != nil {
		h.internalServerError(w, "parse prepared metadata", err)
		return
	}
	if !h.checkCapacity(w, r, c.ID, p) {
		return
	}
	if err := h.store.CheckFilePath(r.Context(), c.ID, p); err != nil {
		h.storeWriteError(w, "check target path", err)
		return
	}

	uploadID, err := auth.GenerateUploadID()
	if err != nil {
		h.internalServerError(w, "generate upload id", err)
		return
	}
	now := time.Now().UTC()
	session := &store.UploadSession{
		UploadID: uploadID, CollectionID: c.ID, TargetPath: p, Metadata: metaJSON,
		MetadataSupplied: supplied,
		ContentTrusted:   isAdmin(r),
		ContentType:      docContentType(doc), Status: store.UploadPending,
		ExpiresAt: now.Add(h.cfg.Limits.UploadTTL),
	}
	if err := h.store.CreateUploadSession(r.Context(), session); err != nil {
		h.internalServerError(w, "create upload session", err)
		return
	}

	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionUploadBegin, Actor: actor(r), Target: c.Name + "/" + p,
	})
	writeJSON(w, http.StatusCreated, uploadSessionView{
		UploadURL: h.uploadURL(uploadID), UploadID: uploadID, Method: http.MethodPut,
		ExpiresAt: session.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// putUpload streams a large-upload PUT to the session's target. No bearer
// token is required: possession of the unguessable upload_id is the capability
// (decision D2). The session is single-use: claiming it (a conditional
// pending→completed transition) and writing the node happen in one
// transaction, so two concurrent PUTs to the same upload URL can never both
// mutate the target (artifactsd-ku3), and a failed node write leaves the
// session pending (artifactsd-m9e.13).
func (h *Handler) putUpload(w http.ResponseWriter, r *http.Request) {
	session, ok := h.lookupUploadSession(w, r)
	if !ok {
		return
	}
	contentType := resolveContentType(session.ContentType, r)

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
	refStr := string(ref)

	c, err := h.store.GetCollection(r.Context(), session.CollectionID)
	if err != nil {
		h.internalServerError(w, "lookup session collection", err)
		return
	}
	existing, err := h.store.GetNode(r.Context(), c.ID, session.TargetPath)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		h.internalServerError(w, "lookup target node", err)
		return
	}
	node := &store.Node{
		CollectionID: c.ID, Path: session.TargetPath, Name: lastSegment(session.TargetPath),
		Kind: store.KindFile, BlobID: &refStr, Size: &size, ContentType: strPtrOrNil(contentType),
		Checksum: &sum, Metadata: session.Metadata,
	}
	status := http.StatusCreated
	if existing != nil {
		status = http.StatusOK
	}
	// A non-admin session's metadata must not drop an admin's sandbox
	// decision on the file it replaces (artifactsd-7tl.2).
	if session.MetadataSupplied && !session.ContentTrusted && existing != nil && existing.Kind == store.KindFile {
		if doc, err := meta.FromJSON(node.Metadata); err == nil {
			keepAdminSandbox(doc, existing.Metadata)
			if out, err := doc.JSON(); err == nil {
				node.Metadata = out
			}
		}
	}

	// Claim the session and write the node in one transaction
	// (artifactsd-m9e.13). The blob above is content-addressed and not yet
	// referenced, so storing it has no visible effect. If the node write fails
	// (a kind conflict, the collection file cap, a DB error) the claim rolls
	// back and the session stays pending for a retry. Of two concurrent PUTs
	// exactly one commits; the loser gets 409 and never touches the node
	// (artifactsd-ku3).
	if err := h.store.CommitUpload(r.Context(), session.UploadID, node, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, CodeConflict, "upload session already consumed")
			return
		}
		if errors.Is(err, store.ErrUploadSessionExpired) {
			// Same answer as a session found expired up front.
			writeError(w, http.StatusNotFound, CodeNotFound, "upload session does not exist")
			return
		}
		h.storeWriteError(w, "commit upload", err)
		return
	}

	node, _ = h.store.GetNodeByID(r.Context(), node.ID)
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionUploadComplete, Actor: audit.ActorUpload, Target: c.Name + "/" + session.TargetPath,
	})
	writeJSON(w, status, h.toFileView(c.Name, node))
}

// getUploadStatus reports a session's lifecycle state. Like the PUT, this is a
// capability endpoint: no bearer token is checked.
func (h *Handler) getUploadStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := h.lookupUploadSession(w, r)
	if !ok {
		return
	}
	c, err := h.store.GetCollection(r.Context(), session.CollectionID)
	if err != nil {
		h.internalServerError(w, "lookup session collection", err)
		return
	}
	writeJSON(w, http.StatusOK, toUploadStatusView(c.Name, session))
}

// deleteUpload aborts a pending session. Capability endpoint: no bearer token
// is checked. Sessions that already completed or were aborted return 409.
func (h *Handler) deleteUpload(w http.ResponseWriter, r *http.Request) {
	session, ok := h.lookupUploadSession(w, r)
	if !ok {
		return
	}
	if session.Status != store.UploadPending {
		writeError(w, http.StatusConflict, CodeConflict, "upload session is not pending")
		return
	}
	if err := h.store.DeleteUploadSession(r.Context(), session.UploadID); err != nil {
		h.internalServerError(w, "abort upload session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// lookupUploadSession resolves {upload_id}, writing 404 if it is absent or has
// expired (SPEC §11: expired objects return 404, not 403/410), and 409 if a PUT
// targets a session that already left the pending state.
func (h *Handler) lookupUploadSession(w http.ResponseWriter, r *http.Request) (*store.UploadSession, bool) {
	session, err := h.store.GetUploadSession(r.Context(), r.PathValue("upload_id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "upload session does not exist")
		return nil, false
	}
	if err != nil {
		h.internalServerError(w, "lookup upload session", err)
		return nil, false
	}
	if session.Status == store.UploadPending && time.Now().UTC().After(session.ExpiresAt) {
		writeError(w, http.StatusNotFound, CodeNotFound, "upload session does not exist")
		return nil, false
	}
	if session.Status != store.UploadPending && r.Method == http.MethodPut {
		writeError(w, http.StatusConflict, CodeConflict, "upload session is not pending")
		return nil, false
	}
	return session, true
}

func toUploadStatusView(collection string, u *store.UploadSession) uploadStatusView {
	v := uploadStatusView{
		UploadID: u.UploadID, Collection: collection, Path: u.TargetPath, Status: u.Status,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: u.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if u.CompletedAt != nil {
		s := u.CompletedAt.UTC().Format(time.RFC3339)
		v.CompletedAt = &s
	}
	return v
}
