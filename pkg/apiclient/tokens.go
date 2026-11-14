package apiclient

import (
	"context"
	"net/http"
	"net/url"
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
	// Restricted tokens reach only the collections in Grants (design/16).
	Restricted bool    `json:"restricted"`
	Grants     []Grant `json:"grants,omitempty"`
}

// Grant gives a restricted token scopes on one collection (design/16 §2).
type Grant struct {
	Collection string   `json:"collection"`
	Scopes     []string `json:"scopes"`
}

// CreateTokenRequest is the body for CreateToken. A restricted token carries
// its scopes on Grants and leaves Scopes empty.
type CreateTokenRequest struct {
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes,omitempty"`
	ExpiresAt  string   `json:"expires_at,omitempty"`
	Restricted bool     `json:"restricted,omitempty"`
	Grants     []Grant  `json:"grants,omitempty"`
}

// CreateTokenResponse adds the one-time cleartext secret to a Token.
type CreateTokenResponse struct {
	Token
	Secret string `json:"token"`
}

// CreateToken creates an API token; the cleartext secret is returned only in
// this response (design/04 §8).
func (c *Client) CreateToken(ctx context.Context, req CreateTokenRequest) (*CreateTokenResponse, error) { //nolint:gocritic // public API takes the request by value
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

// SetTokenGrant creates or replaces a restricted token's grant on a
// collection and returns the updated token (design/16 §5).
func (c *Client) SetTokenGrant(ctx context.Context, id int64, collection string, scopes []string) (*Token, error) {
	var out Token
	body := map[string][]string{"scopes": scopes}
	if err := c.sendJSON(ctx, http.MethodPut, tokenGrantPath(id, collection), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTokenGrant removes a restricted token's grant on a collection.
func (c *Client) DeleteTokenGrant(ctx context.Context, id int64, collection string) error {
	return c.sendJSON(ctx, http.MethodDelete, tokenGrantPath(id, collection), nil, nil)
}

func tokenGrantPath(id int64, collection string) string {
	return "/api/v1/tokens/" + strconv.FormatInt(id, 10) + "/grants/" + url.PathEscape(collection)
}
