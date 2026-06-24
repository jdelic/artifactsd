package apiclient

import (
	"context"
	"net/http"
	"strconv"
)

// Token mirrors the API's token view; the cleartext secret is never included
// (design/04 §8).
type Token struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt *string  `json:"last_used_at,omitempty"`
	ExpiresAt  *string  `json:"expires_at,omitempty"`
	RevokedAt  *string  `json:"revoked_at,omitempty"`
}

// CreateTokenRequest is the body for CreateToken.
type CreateTokenRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

// CreateTokenResponse adds the one-time cleartext secret to a Token.
type CreateTokenResponse struct {
	Token
	Secret string `json:"token"`
}

// CreateToken creates an API token; the cleartext secret is returned only in
// this response (design/04 §8).
func (c *Client) CreateToken(ctx context.Context, req CreateTokenRequest) (*CreateTokenResponse, error) {
	var out CreateTokenResponse
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/tokens", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTokens lists API tokens (metadata only, design/04 §8).
func (c *Client) ListTokens(ctx context.Context) (*Page[Token], error) {
	var out Page[Token]
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/tokens", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeToken revokes an API token by id (design/04 §8).
func (c *Client) RevokeToken(ctx context.Context, id int64) error {
	return c.sendJSON(ctx, http.MethodDelete, "/api/v1/tokens/"+strconv.FormatInt(id, 10), nil, nil)
}
