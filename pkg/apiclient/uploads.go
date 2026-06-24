package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// BeginUploadRequest is the body for BeginUpload.
type BeginUploadRequest struct {
	Path     string          `json:"path"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// UploadSession is the response to BeginUpload: a direct, unauthenticated PUT
// target for the file's bytes (design/04 §5, design/07).
type UploadSession struct {
	UploadURL string `json:"upload_url"`
	UploadID  string `json:"upload_id"`
	Method    string `json:"method"`
	ExpiresAt string `json:"expires_at"`
}

// BeginUpload requests an upload session for a large or binary file. The
// caller PUTs the file's bytes directly to UploadURL, out of band (design/04
// §5) — BeginUpload itself never sees the file content.
func (c *Client) BeginUpload(ctx context.Context, collection string, req BeginUploadRequest) (*UploadSession, error) {
	var out UploadSession
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/collections/"+url.PathEscape(collection)+"/uploads", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
