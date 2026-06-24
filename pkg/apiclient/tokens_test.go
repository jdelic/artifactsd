package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_CreateToken(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/tokens" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req CreateTokenRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Name != "ci" || len(req.Scopes) != 1 || req.Scopes[0] != "collections:write" {
			t.Errorf("req = %+v", req)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(CreateTokenResponse{
			Token:  Token{ID: 1, Name: "ci", Prefix: "tok_abc", Scopes: req.Scopes},
			Secret: "tok_abc123",
		})
	})
	resp, err := c.CreateToken(context.Background(), CreateTokenRequest{Name: "ci", Scopes: []string{"collections:write"}})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if resp.Secret != "tok_abc123" || resp.Name != "ci" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestClient_ListTokens(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/tokens" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Page[Token]{Items: []Token{{ID: 1, Name: "ci"}}})
	})
	page, err := c.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "ci" {
		t.Errorf("items = %+v", page.Items)
	}
}

func TestClient_RevokeToken(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/tokens/7" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.RevokeToken(context.Background(), 7); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
}
