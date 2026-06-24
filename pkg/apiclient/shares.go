package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Share mirrors the API's share view (design/04 §6).
type Share struct {
	ShareID    string          `json:"share_id"`
	Collection string          `json:"collection"`
	TargetPath string          `json:"target_path"`
	TargetKind string          `json:"target_kind"`
	Metadata   json.RawMessage `json:"metadata"`
	URL        string          `json:"url"`
	CreatedAt  string          `json:"created_at"`
	ExpiresAt  *string         `json:"expires_at,omitempty"`
	RevokedAt  *string         `json:"revoked_at,omitempty"`
}

// CreateShareRequest is the body for CreateShare. TargetPath "" shares the
// whole collection.
type CreateShareRequest struct {
	TargetPath string          `json:"target_path"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// CreateShare creates a share for a collection, folder, or file (design/04 §6).
func (c *Client) CreateShare(ctx context.Context, collection string, req CreateShareRequest) (*Share, error) {
	var out Share
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/collections/"+url.PathEscape(collection)+"/shares", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListShares lists a collection's shares (design/04 §6).
func (c *Client) ListShares(ctx context.Context, collection string) (*Page[Share], error) {
	var out Page[Share]
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/collections/"+url.PathEscape(collection)+"/shares", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetShare fetches one share by id (design/04 §6).
func (c *Client) GetShare(ctx context.Context, shareID string) (*Share, error) {
	var out Share
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/shares/"+url.PathEscape(shareID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchShareMeta merges metadata onto a share's policy/expiry (design/04 §6).
func (c *Client) PatchShareMeta(ctx context.Context, shareID string, metadata json.RawMessage) (*Share, error) {
	var out Share
	if err := c.sendJSON(ctx, http.MethodPatch, "/api/v1/shares/"+url.PathEscape(shareID), metadata, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeShare revokes (deletes) a share (design/04 §6).
func (c *Client) RevokeShare(ctx context.Context, shareID string) error {
	return c.sendJSON(ctx, http.MethodDelete, "/api/v1/shares/"+url.PathEscape(shareID), nil, nil)
}
