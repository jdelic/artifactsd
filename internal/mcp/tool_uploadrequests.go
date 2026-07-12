package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/internal/durations"
	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// CreateUploadRequestIn is the input for artifact_create_upload_request.
type CreateUploadRequestIn struct {
	Collection           string         `json:"collection" jsonschema:"Collection name"`
	TargetFolder         string         `json:"target_folder,omitempty" jsonschema:"Destination folder path inside the collection; collection root if omitted"`
	Label                string         `json:"label,omitempty" jsonschema:"Human-readable label shown on the upload form"`
	Description          string         `json:"description,omitempty" jsonschema:"Longer description shown below the label"`
	SingleUse            bool           `json:"single_use,omitempty" jsonschema:"Auto-close the link after the first successful submission"`
	UseSubmissionFolders bool           `json:"use_submission_folders,omitempty" jsonschema:"Store each submission in its own sub-folder named by submission_id"`
	AllowUploaderAccess  bool           `json:"allow_uploader_access,omitempty" jsonschema:"Return file URLs to the uploader for public and unlisted files"`
	MaxFiles             int64          `json:"max_files,omitempty" jsonschema:"Maximum total files that may be uploaded; 0 = unlimited"`
	MaxFileSizeBytes     int64          `json:"max_file_size_bytes,omitempty" jsonschema:"Per-file size limit in bytes; 0 = server default"`
	ExpiresIn            string         `json:"expires_in,omitempty" jsonschema:"Duration like 24h, 7d, or 2w; the link never expires if omitted"`
	FileMetadata         *meta.Document `json:"file_metadata,omitempty" jsonschema:"Metadata document applied to every uploaded file, e.g. {\"access\":{\"visibility\":\"private\"}} or {\"expiry\":{\"delete_after_creation\":\"7d\"}}"`
}

// CreateUploadRequestOut is the output of artifact_create_upload_request.
type CreateUploadRequestOut struct {
	RequestID string  `json:"request_id"`
	URL       string  `json:"url"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

func (s *Server) createUploadRequest(ctx context.Context, _ *gosdk.CallToolRequest, in *CreateUploadRequestIn) (*gosdk.CallToolResult, CreateUploadRequestOut, error) {
	if in.Collection == "" {
		return nil, CreateUploadRequestOut{}, fmt.Errorf("collection is required")
	}

	req := apiclient.CreateUploadRequestRequest{
		Collection:           in.Collection,
		TargetFolder:         in.TargetFolder,
		SingleUse:            in.SingleUse,
		UseSubmissionFolders: in.UseSubmissionFolders,
		AllowUploaderAccess:  in.AllowUploaderAccess,
	}
	if in.Label != "" {
		req.Label = &in.Label
	}
	if in.Description != "" {
		req.Description = &in.Description
	}
	if in.MaxFiles > 0 {
		req.MaxFiles = &in.MaxFiles
	}
	if in.MaxFileSizeBytes > 0 {
		req.MaxFileSize = &in.MaxFileSizeBytes
	}
	if in.ExpiresIn != "" {
		d, err := durations.Parse(in.ExpiresIn)
		if err != nil {
			return nil, CreateUploadRequestOut{}, fmt.Errorf("invalid expires_in %q: %w", in.ExpiresIn, err)
		}
		s := time.Now().UTC().Add(d).Format(time.RFC3339)
		req.ExpiresAt = &s
	}
	if in.FileMetadata != nil {
		raw, err := json.Marshal(in.FileMetadata)
		if err != nil {
			return nil, CreateUploadRequestOut{}, err
		}
		req.FileMetadata = raw
	}

	ur, err := s.Client.CreateUploadRequest(ctx, &req)
	if err != nil {
		return nil, CreateUploadRequestOut{}, MapError(err)
	}
	return nil, CreateUploadRequestOut{
		RequestID: ur.RequestID,
		URL:       ur.URL,
		ExpiresAt: ur.ExpiresAt,
	}, nil
}

// ListUploadRequestsIn is the input for artifact_list_upload_requests.
type ListUploadRequestsIn struct{}

// ListUploadRequestsOut is the output of artifact_list_upload_requests.
type ListUploadRequestsOut struct {
	Items []uploadRequestSummary `json:"items"`
}

type uploadRequestSummary struct {
	RequestID     string  `json:"request_id"`
	URL           string  `json:"url"`
	Collection    string  `json:"collection"`
	Status        string  `json:"status"`
	Label         *string `json:"label,omitempty"`
	FilesUploaded int64   `json:"files_uploaded"`
	ExpiresAt     *string `json:"expires_at,omitempty"`
}

func (s *Server) listUploadRequests(ctx context.Context, _ *gosdk.CallToolRequest, _ *ListUploadRequestsIn) (*gosdk.CallToolResult, ListUploadRequestsOut, error) {
	page, err := s.Client.ListUploadRequests(ctx)
	if err != nil {
		return nil, ListUploadRequestsOut{}, MapError(err)
	}
	items := make([]uploadRequestSummary, 0, len(page.Items))
	for i := range page.Items {
		ur := &page.Items[i]
		items = append(items, uploadRequestSummary{
			RequestID:     ur.RequestID,
			URL:           ur.URL,
			Collection:    ur.Collection,
			Status:        ur.Status,
			Label:         ur.Label,
			FilesUploaded: ur.FilesUploaded,
			ExpiresAt:     ur.ExpiresAt,
		})
	}
	return nil, ListUploadRequestsOut{Items: items}, nil
}

// GetUploadRequestIn is the input for artifact_get_upload_request.
type GetUploadRequestIn struct {
	RequestID string `json:"request_id" jsonschema:"Upload request ID (urq_ prefix)"`
}

// GetUploadRequestOut is the output of artifact_get_upload_request.
type GetUploadRequestOut struct {
	RequestID     string  `json:"request_id"`
	URL           string  `json:"url"`
	Collection    string  `json:"collection"`
	Status        string  `json:"status"`
	Label         *string `json:"label,omitempty"`
	SingleUse     bool    `json:"single_use"`
	FilesUploaded int64   `json:"files_uploaded"`
	ExpiresAt     *string `json:"expires_at,omitempty"`
}

func (s *Server) getUploadRequest(ctx context.Context, _ *gosdk.CallToolRequest, in *GetUploadRequestIn) (*gosdk.CallToolResult, GetUploadRequestOut, error) {
	if in.RequestID == "" {
		return nil, GetUploadRequestOut{}, fmt.Errorf("request_id is required")
	}
	ur, err := s.Client.GetUploadRequest(ctx, in.RequestID)
	if err != nil {
		return nil, GetUploadRequestOut{}, MapError(err)
	}
	return nil, GetUploadRequestOut{
		RequestID:     ur.RequestID,
		URL:           ur.URL,
		Collection:    ur.Collection,
		Status:        ur.Status,
		Label:         ur.Label,
		SingleUse:     ur.SingleUse,
		FilesUploaded: ur.FilesUploaded,
		ExpiresAt:     ur.ExpiresAt,
	}, nil
}

// RevokeUploadRequestIn is the input for artifact_revoke_upload_request.
type RevokeUploadRequestIn struct {
	RequestID string `json:"request_id" jsonschema:"Upload request ID (urq_ prefix) to revoke"`
}

// RevokeUploadRequestOut is the output of artifact_revoke_upload_request.
type RevokeUploadRequestOut struct {
	Revoked bool `json:"revoked"`
}

func (s *Server) revokeUploadRequest(ctx context.Context, _ *gosdk.CallToolRequest, in *RevokeUploadRequestIn) (*gosdk.CallToolResult, RevokeUploadRequestOut, error) {
	if in.RequestID == "" {
		return nil, RevokeUploadRequestOut{}, fmt.Errorf("request_id is required")
	}
	if err := s.Client.RevokeUploadRequest(ctx, in.RequestID); err != nil {
		return nil, RevokeUploadRequestOut{}, MapError(err)
	}
	return nil, RevokeUploadRequestOut{Revoked: true}, nil
}
