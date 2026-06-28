package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// UploadRequest mirrors the API's upload request view (design/15 §5).
type UploadRequest struct {
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

// UploadRequestSubmission mirrors one submission record (design/15 §5).
type UploadRequestSubmission struct {
	SubmissionID string  `json:"submission_id"`
	FileCount    int64   `json:"file_count"`
	TotalBytes   int64   `json:"total_bytes"`
	ClientIP     *string `json:"client_ip,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

// CreateUploadRequestRequest is the body for CreateUploadRequest.
type CreateUploadRequestRequest struct {
	Collection           string          `json:"collection"`
	TargetFolder         string          `json:"target_folder,omitempty"`
	Label                *string         `json:"label,omitempty"`
	Description          *string         `json:"description,omitempty"`
	FileMetadata         json.RawMessage `json:"file_metadata,omitempty"`
	AllowUploaderAccess  bool            `json:"allow_uploader_access,omitempty"`
	SingleUse            bool            `json:"single_use,omitempty"`
	UseSubmissionFolders bool            `json:"use_submission_folders,omitempty"`
	MaxFiles             *int64          `json:"max_files,omitempty"`
	MaxFileSize          *int64          `json:"max_file_size,omitempty"`
	ExpiresAt            *string         `json:"expires_at,omitempty"`
}

// PatchUploadRequestRequest is the body for PatchUploadRequest.
type PatchUploadRequestRequest struct {
	Label       *string `json:"label,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

// CreateUploadRequest creates a new upload request link (design/15 §5).
func (c *Client) CreateUploadRequest(ctx context.Context, req CreateUploadRequestRequest) (*UploadRequest, error) {
	var out UploadRequest
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/upload-requests", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListUploadRequests lists all upload request links (design/15 §5).
func (c *Client) ListUploadRequests(ctx context.Context) (*Page[UploadRequest], error) {
	var out Page[UploadRequest]
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/upload-requests", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUploadRequest returns one upload request by request_id (design/15 §5).
func (c *Client) GetUploadRequest(ctx context.Context, requestID string) (*UploadRequest, error) {
	var out UploadRequest
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/upload-requests/"+url.PathEscape(requestID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchUploadRequest updates mutable fields of an upload request (design/15 §5).
func (c *Client) PatchUploadRequest(ctx context.Context, requestID string, req PatchUploadRequestRequest) (*UploadRequest, error) {
	var out UploadRequest
	if err := c.sendJSON(ctx, http.MethodPatch, "/api/v1/upload-requests/"+url.PathEscape(requestID), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeUploadRequest revokes (deletes) an upload request link (design/15 §5).
func (c *Client) RevokeUploadRequest(ctx context.Context, requestID string) error {
	return c.sendJSON(ctx, http.MethodDelete, "/api/v1/upload-requests/"+url.PathEscape(requestID), nil, nil)
}

// ListUploadRequestSubmissions returns submissions for one request (design/15 §5).
func (c *Client) ListUploadRequestSubmissions(ctx context.Context, requestID string) (*Page[UploadRequestSubmission], error) {
	var out Page[UploadRequestSubmission]
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/upload-requests/"+url.PathEscape(requestID)+"/submissions", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
