package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// Collection mirrors the API's collection view (design/04 §2). Metadata is left
// as raw JSON so the client stays agnostic to the metadata document shape.
type Collection struct {
	Name       string          `json:"name"`
	Metadata   json.RawMessage `json:"metadata"`
	Visibility string          `json:"visibility"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
	ExpiresAt  *string         `json:"expires_at,omitempty"`
}

// Page is the paginated list envelope (design/04 §1).
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// CreateCollectionRequest is the body for CreateCollection.
type CreateCollectionRequest struct {
	Name     string          `json:"name"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// CreateCollection creates a collection (design/04 §2).
func (c *Client) CreateCollection(ctx context.Context, req CreateCollectionRequest) (*Collection, error) {
	var out Collection
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/collections", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListCollections lists collections. cursor may be empty; limit <= 0 uses the
// server default.
func (c *Client) ListCollections(ctx context.Context, cursor string, limit int) (*Page[Collection], error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out Page[Collection]
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/collections"+encodeQuery(q), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCollection fetches one collection by name (design/04 §2).
func (c *Client) GetCollection(ctx context.Context, name string) (*Collection, error) {
	var out Collection
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/collections/"+url.PathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchCollectionMeta merges metadata onto a collection (design/04 §2).
func (c *Client) PatchCollectionMeta(ctx context.Context, name string, metadata json.RawMessage) (*Collection, error) {
	var out Collection
	if err := c.sendJSON(ctx, http.MethodPatch, "/api/v1/collections/"+url.PathEscape(name)+"/meta", metadata, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCollection deletes a collection; force cascades non-empty collections
// (design/04 §2).
func (c *Client) DeleteCollection(ctx context.Context, name string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	return c.sendJSON(ctx, http.MethodDelete, "/api/v1/collections/"+url.PathEscape(name)+encodeQuery(q), nil, nil)
}

// TreeNode is one entry in a collection's structured listing (design/04 §4).
type TreeNode struct {
	Path        string  `json:"path"`
	Kind        string  `json:"kind"` // "file"|"folder"
	Size        *int64  `json:"size,omitempty"`
	ContentType *string `json:"content_type,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

// Tree lists a collection's files and folders (design/04 §4). path scopes the
// listing to a subtree ("" for the root); depth limits how many levels below
// path are returned (0 for unlimited).
func (c *Client) Tree(ctx context.Context, collection, path string, depth int) (*Page[TreeNode], error) {
	q := url.Values{}
	if path != "" {
		q.Set("path", path)
	}
	if depth > 0 {
		q.Set("depth", strconv.Itoa(depth))
	}
	var out Page[TreeNode]
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/collections/"+url.PathEscape(collection)+"/tree"+encodeQuery(q), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
