package apiclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// FileInfo mirrors the API's file view (design/04 §3).
type FileInfo struct {
	Collection  string  `json:"collection"`
	Path        string  `json:"path"`
	Size        int64   `json:"size"`
	ContentType string  `json:"content_type,omitempty"`
	Checksum    string  `json:"checksum,omitempty"`
	URL         string  `json:"url"`
	UpdatedAt   string  `json:"updated_at"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

// PutFileOptions are optional inputs to PutFile.
type PutFileOptions struct {
	ContentType string          // sets the Content-Type header
	Metadata    json.RawMessage // optional metadata document (Artifactsd-Metadata)
}

// metadataHeader carries a base64url(JSON) metadata document on a binary upload
// (design/04 §3).
const metadataHeader = "Artifactsd-Metadata"

// PutFile uploads (or replaces) a file. The body is streamed as raw bytes.
func (c *Client) PutFile(ctx context.Context, collection, path string, body io.Reader, opts PutFileOptions) (*FileInfo, error) {
	headers := map[string]string{}
	if opts.ContentType != "" {
		headers["Content-Type"] = opts.ContentType
	}
	if len(opts.Metadata) > 0 {
		headers[metadataHeader] = base64.RawURLEncoding.EncodeToString(opts.Metadata)
	}
	var out FileInfo
	if err := c.sendRaw(ctx, http.MethodPut, filePath(collection, path), body, headers, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetFile opens a file for download. The caller must Close the returned stream.
func (c *Client) GetFile(ctx context.Context, collection, path string) (io.ReadCloser, error) {
	return c.openStream(ctx, filePath(collection, path))
}

// DeleteFile removes a file (design/04 §3).
func (c *Client) DeleteFile(ctx context.Context, collection, path string) error {
	return c.sendJSON(ctx, http.MethodDelete, filePath(collection, path), nil, nil)
}

// filePath builds the /files/ route, escaping each path segment while preserving
// the slash separators the {path...} wildcard expects.
func filePath(collection, path string) string {
	return "/api/v1/collections/" + url.PathEscape(collection) + "/files/" + escapeSegments(path)
}

// escapeSegments percent-escapes each "/"-separated segment.
func escapeSegments(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
