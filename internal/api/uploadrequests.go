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

// uploadRequestView is the API representation of an upload request.
type uploadRequestView struct {
	RequestID            string          `json:"request_id"`
	URL                  string          `json:"url"`
	Collection           string          `json:"collection"`
	TargetFolder         string          `json:"target_folder"`
	Label                *string         `json:"label,omitempty"`
	Description          *string         `json:"description,omitempty"`
	FileMetadata         json.RawMessage `json:"file_metadata,omitempty"`
	AllowUploaderAccess  bool            `json:"allow_uploader_access"`
	SingleUse            bool            `json:"single_use"`
	UseSubmissionFolders bool            `json:"use_submission_folders"`
	MaxFiles             *int64          `json:"max_files,omitempty"`
	MaxFileSize          *int64          `json:"max_file_size,omitempty"`
	FilesUploaded        int64           `json:"files_uploaded"`
	Status               string          `json:"status"`
	CreatedAt            string          `json:"created_at"`
	ExpiresAt            *string         `json:"expires_at,omitempty"`
	RevokedAt            *string         `json:"revoked_at,omitempty"`
}

// uploadRequestSubmissionView is the API representation of one submission.
type uploadRequestSubmissionView struct {
	SubmissionID string  `json:"submission_id"`
	FileCount    int64   `json:"file_count"`
	TotalBytes   int64   `json:"total_bytes"`
	ClientIP     *string `json:"client_ip,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

// uploadRequestURL builds the public /r/{request_id} URL.
func (h *Handler) uploadRequestURL(requestID string) string {
	return strings.TrimRight(h.baseURL, "/") + "/r/" + requestID
}

func (h *Handler) toUploadRequestView(collection string, ur *store.UploadRequest) uploadRequestView {
	v := uploadRequestView{
		RequestID:            ur.RequestID,
		URL:                  h.uploadRequestURL(ur.RequestID),
		Collection:           collection,
		TargetFolder:         ur.TargetFolder,
		Label:                ur.Label,
		Description:          ur.Description,
		FileMetadata:         ur.FileMetadata,
		AllowUploaderAccess:  ur.AllowUploaderAccess,
		SingleUse:            ur.SingleUse,
		UseSubmissionFolders: ur.UseSubmissionFolders,
		MaxFiles:             ur.MaxFiles,
		MaxFileSize:          ur.MaxFileSize,
		FilesUploaded:        ur.FilesUploaded,
		Status:               ur.Status,
		CreatedAt:            ur.CreatedAt.UTC().Format(time.RFC3339),
	}
	if ur.ExpiresAt != nil {
		s := ur.ExpiresAt.UTC().Format(time.RFC3339)
		v.ExpiresAt = &s
	}
	if ur.RevokedAt != nil {
		s := ur.RevokedAt.UTC().Format(time.RFC3339)
		v.RevokedAt = &s
	}
	return v
}

// createUploadRequest request body (design/15 §5).
type createUploadRequestBody struct {
	Collection           string          `json:"collection"`
	TargetFolder         string          `json:"target_folder"`
	Label                *string         `json:"label,omitempty"`
	Description          *string         `json:"description,omitempty"`
	FileMetadata         json.RawMessage `json:"file_metadata,omitempty"`
	AllowUploaderAccess  bool            `json:"allow_uploader_access"`
	SingleUse            bool            `json:"single_use"`
	UseSubmissionFolders bool            `json:"use_submission_folders"`
	MaxFiles             *int64          `json:"max_files,omitempty"`
	MaxFileSize          *int64          `json:"max_file_size,omitempty"`
	ExpiresAt            *string         `json:"expires_at,omitempty"`
}

// patchUploadRequestBody holds mutable fields for PATCH.
type patchUploadRequestBody struct {
	Label       *string `json:"label,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

// createUploadRequest mints a new upload request link (design/15 §5).
func (h *Handler) createUploadRequest(w http.ResponseWriter, r *http.Request) {
	var req createUploadRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}

	c, err := h.store.GetCollectionByName(r.Context(), req.Collection)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "collection does not exist")
		return
	}
	if err != nil {
		h.internalServerError(w, "lookup collection", err)
		return
	}

	// Validate target_folder path if non-empty.
	targetFolder := ""
	if strings.TrimSpace(req.TargetFolder) != "" {
		var ok bool
		targetFolder, ok = h.cleanPathStr(w, req.TargetFolder)
		if !ok {
			return
		}
	}

	// Validate and prepare file_metadata.
	fileMetaJSON, ok := h.prepareMetadata(w, req.FileMetadata)
	if !ok {
		return
	}

	// Validate max_file_size ≤ server limit.
	if req.MaxFileSize != nil && *req.MaxFileSize > h.cfg.Limits.MaxUploadSize {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "max_file_size exceeds server limit")
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "expires_at must be RFC3339")
			return
		}
		if !t.After(time.Now()) {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "expires_at must be in the future")
			return
		}
		expiresAt = &t
	}

	requestID, err := auth.GenerateUploadRequestID()
	if err != nil {
		h.internalServerError(w, "generate request id", err)
		return
	}

	ur := &store.UploadRequest{
		RequestID:            requestID,
		CollectionID:         c.ID,
		TargetFolder:         targetFolder,
		Label:                req.Label,
		Description:          req.Description,
		FileMetadata:         fileMetaJSON,
		AllowUploaderAccess:  req.AllowUploaderAccess,
		SingleUse:            req.SingleUse,
		UseSubmissionFolders: req.UseSubmissionFolders,
		MaxFiles:             req.MaxFiles,
		MaxFileSize:          req.MaxFileSize,
		ExpiresAt:            expiresAt,
	}
	if err := h.store.CreateUploadRequest(r.Context(), ur); err != nil {
		h.internalServerError(w, "create upload request", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionUploadRequestCreate,
		Actor:  actor(r),
		Target: c.Name + " " + requestID,
	})
	writeJSON(w, http.StatusCreated, h.toUploadRequestView(c.Name, ur))
}

// listUploadRequests lists all upload requests (design/15 §5).
func (h *Handler) listUploadRequests(w http.ResponseWriter, r *http.Request) {
	reqs, err := h.store.ListUploadRequests(r.Context(), nil)
	if err != nil {
		h.internalServerError(w, "list upload requests", err)
		return
	}

	// Batch-load collection names.
	colNames := make(map[int64]string)
	items := make([]uploadRequestView, 0, len(reqs))
	for _, ur := range reqs {
		name, ok := colNames[ur.CollectionID]
		if !ok {
			c, err := h.store.GetCollection(r.Context(), ur.CollectionID)
			if err == nil {
				name = c.Name
			}
			colNames[ur.CollectionID] = name
		}
		items = append(items, h.toUploadRequestView(name, ur))
	}
	writeJSON(w, http.StatusOK, listResponse[uploadRequestView]{Items: items})
}

// getUploadRequest returns one upload request by request_id (design/15 §5).
func (h *Handler) getUploadRequest(w http.ResponseWriter, r *http.Request) {
	ur, c, ok := h.lookupUploadRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.toUploadRequestView(c.Name, ur))
}

// patchUploadRequest updates mutable fields of an upload request (design/15 §5).
func (h *Handler) patchUploadRequest(w http.ResponseWriter, r *http.Request) {
	ur, c, ok := h.lookupUploadRequest(w, r)
	if !ok {
		return
	}
	var req patchUploadRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Label != nil {
		ur.Label = req.Label
	}
	if req.Description != nil {
		ur.Description = req.Description
	}
	if req.Status != nil {
		switch *req.Status {
		case store.UploadRequestClosed:
			ur.Status = store.UploadRequestClosed
		default:
			writeError(w, http.StatusBadRequest, CodeBadRequest, "status may only be set to 'closed' via PATCH; use DELETE to revoke")
			return
		}
	}
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "expires_at must be RFC3339")
			return
		}
		ur.ExpiresAt = &t
	}

	if err := h.store.UpdateUploadRequest(r.Context(), ur); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "upload request does not exist")
			return
		}
		h.internalServerError(w, "update upload request", err)
		return
	}
	reloaded, err := h.store.GetUploadRequest(r.Context(), ur.RequestID)
	if err != nil {
		h.internalServerError(w, "reload upload request", err)
		return
	}
	writeJSON(w, http.StatusOK, h.toUploadRequestView(c.Name, reloaded))
}

// deleteUploadRequest revokes an upload request (design/15 §5).
func (h *Handler) deleteUploadRequest(w http.ResponseWriter, r *http.Request) {
	ur, c, ok := h.lookupUploadRequest(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if err := h.store.CloseUploadRequest(r.Context(), ur.RequestID, store.UploadRequestRevoked, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Already revoked — idempotent.
		} else {
			h.internalServerError(w, "revoke upload request", err)
			return
		}
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionUploadRequestRevoke,
		Actor:  actor(r),
		Target: c.Name + " " + ur.RequestID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// listUploadRequestSubmissions returns submissions for one request (design/15 §5).
func (h *Handler) listUploadRequestSubmissions(w http.ResponseWriter, r *http.Request) {
	ur, _, ok := h.lookupUploadRequest(w, r)
	if !ok {
		return
	}
	subs, err := h.store.ListUploadRequestSubmissions(r.Context(), ur.ID)
	if err != nil {
		h.internalServerError(w, "list submissions", err)
		return
	}
	items := make([]uploadRequestSubmissionView, 0, len(subs))
	for _, sub := range subs {
		items = append(items, uploadRequestSubmissionView{
			SubmissionID: sub.SubmissionID,
			FileCount:    sub.FileCount,
			TotalBytes:   sub.TotalBytes,
			ClientIP:     sub.ClientIP,
			CreatedAt:    sub.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, listResponse[uploadRequestSubmissionView]{Items: items})
}

// lookupUploadRequest resolves {request_id} and its collection.
func (h *Handler) lookupUploadRequest(w http.ResponseWriter, r *http.Request) (*store.UploadRequest, *store.Collection, bool) {
	ur, err := h.store.GetUploadRequest(r.Context(), r.PathValue("request_id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "upload request does not exist")
		return nil, nil, false
	}
	if err != nil {
		h.internalServerError(w, "lookup upload request", err)
		return nil, nil, false
	}
	c, err := h.store.GetCollection(r.Context(), ur.CollectionID)
	if err != nil {
		h.internalServerError(w, "lookup upload request collection", err)
		return nil, nil, false
	}
	return ur, c, true
}
