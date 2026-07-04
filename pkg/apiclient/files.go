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

// DeleteFile removes a file (design/04 §3). Folder paths are rejected with a
// 409 — use DeleteFolder for recursive subtree deletion.
func (c *Client) DeleteFile(ctx context.Context, collection, path string) error {
	return c.sendJSON(ctx, http.MethodDelete, filePath(collection, path), nil, nil)
}

// DeleteFolder removes a folder and its whole subtree (design/04 §4). File
// paths are rejected with a 409.
func (c *Client) DeleteFolder(ctx context.Context, collection, path string) error {
	return c.sendJSON(ctx, http.MethodDelete, folderPath(collection, path), nil, nil)
}

// FileMeta is the response shape for collection, folder, and file metadata
// reads and patches (design/04 §2-§4). Own and Effective are left as raw JSON
// since they have different shapes (a metadata document vs. a flat resolved
// view); Effective, Chain, and Sources are absent from PATCH responses.
type FileMeta struct {
	Path      string          `json:"path"`
	Own       json.RawMessage `json:"own"`
	Effective json.RawMessage `json:"effective,omitempty"`
	// Chain is the inheritance chain, lowest precedence first (server
	// defaults → collection → folders → file), each with its own metadata.
	Chain []ChainEntry `json:"chain,omitempty"`
	// Sources maps each effective property (e.g. "visibility",
	// "headers.Cache-Control") to the label of the chain layer that
	// determined it (e.g. "defaults", "collection", "folder docs").
	Sources map[string]string `json:"sources,omitempty"`
}

// ChainEntry is one layer of a metadata inheritance chain.
type ChainEntry struct {
	Scope string          `json:"scope"`          // defaults | collection | folder | file
	Path  string          `json:"path,omitempty"` // set for folder/file scopes
	Meta  json.RawMessage `json:"meta"`
}

// GetFileMeta reads a file's own and effective metadata (design/04 §3).
func (c *Client) GetFileMeta(ctx context.Context, collection, path string) (*FileMeta, error) {
	var out FileMeta
	if err := c.sendJSON(ctx, http.MethodGet, filePath(collection, path)+"/meta", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchFileMeta merges patch (a metadata document) onto a file's stored
// metadata (design/04 §3).
func (c *Client) PatchFileMeta(ctx context.Context, collection, path string, patch json.RawMessage) (*FileMeta, error) {
	var out FileMeta
	if err := c.sendJSON(ctx, http.MethodPatch, filePath(collection, path)+"/meta", patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetFolderMeta reads a folder's own and effective metadata (design/04 §4).
// Unlike GetFileMeta it also answers for folders that exist only implicitly
// (own is then an empty document).
func (c *Client) GetFolderMeta(ctx context.Context, collection, path string) (*FileMeta, error) {
	var out FileMeta
	if err := c.sendJSON(ctx, http.MethodGet, folderPath(collection, path)+"/meta", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchFolderMeta merges patch onto a folder's metadata, materializing the
// folder node if it does not exist yet (design/04 §4).
func (c *Client) PatchFolderMeta(ctx context.Context, collection, path string, patch json.RawMessage) (*FileMeta, error) {
	var out FileMeta
	if err := c.sendJSON(ctx, http.MethodPatch, folderPath(collection, path)+"/meta", patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// filePath builds the /files/ route, escaping each path segment while preserving
// the slash separators the {path...} wildcard expects.
func filePath(collection, path string) string {
	return "/api/v1/collections/" + url.PathEscape(collection) + "/files/" + escapeSegments(path)
}

// folderPath builds the /folders/ route the same way.
func folderPath(collection, path string) string {
	return "/api/v1/collections/" + url.PathEscape(collection) + "/folders/" + escapeSegments(path)
}

// escapeSegments percent-escapes each "/"-separated segment.
func escapeSegments(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
