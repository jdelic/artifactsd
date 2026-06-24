package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// PutUploadURL streams body to an absolute upload_url returned by BeginUpload.
// The URL is its own capability (decision D2, design/07 §3) — possession of
// the unguessable upload_id is the only authorization, so this bypasses the
// normal base-URL-relative, bearer-token-authenticated request path entirely.
func (c *Client) PutUploadURL(ctx context.Context, uploadURL string, body io.Reader, contentType string) (*FileInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, body)
	if err != nil {
		return nil, fmt.Errorf("apiclient: building upload request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: PUT %s: %w", uploadURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return nil, decodeError(resp)
	}
	var out FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("apiclient: decoding response: %w", err)
	}
	return &out, nil
}
