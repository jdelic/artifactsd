package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/internal/testserver"
)

// mintToken creates an API token with the given scopes through the API.
func mintToken(t *testing.T, ts *testserver.Server, scopes string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.BaseURL+"/api/v1/tokens",
		strings.NewReader(`{"name":"agent","scopes":`+scopes+`}`))
	req.Header.Set("Authorization", "Bearer "+ts.AdminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.Token == "" {
		t.Fatalf("mint token: %v", err)
	}
	return v.Token
}

// TestAdapter_RequiresBearer: no credentials → 401 with a plain Bearer
// challenge, and no OAuth discovery is served (artifactsd-m9e.18).
func TestAdapter_RequiresBearer(t *testing.T) {
	srv := httptest.NewServer(New(Config{BaseURL: "http://127.0.0.1:1"}).Handler())
	t.Cleanup(srv.Close)

	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"0"}}}`
	for _, authz := range []string{"", "Basic dTpw", "Bearer ", "Bearer"} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader(init))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != `Bearer realm="artifactsd-mcp"` {
			t.Errorf("Authorization %q: %d %q, want 401 with the plain Bearer challenge",
				authz, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", p, resp.StatusCode)
		}
	}
}

// TestAdapter_PerClientTokens: each client acts with its own token's scopes.
func TestAdapter_PerClientTokens(t *testing.T) {
	ts := testserver.New(t)
	srv := httptest.NewServer(New(Config{BaseURL: ts.BaseURL}).Handler())
	t.Cleanup(srv.Close)

	admin := connectMCP(t, srv.URL, ts.AdminToken)
	if _, err := admin.CallTool(context.Background(), &gosdk.CallToolParams{
		Name: "artifact_create_collection", Arguments: map[string]any{"name": "demo"},
	}); err != nil {
		t.Fatal(err)
	}

	writer := connectMCP(t, srv.URL, mintToken(t, ts, `["collections:write"]`))
	res, err := writer.CallTool(context.Background(), &gosdk.CallToolParams{
		Name: "artifact_put_text", Arguments: map[string]any{"collection": "demo", "path": "a.txt", "text": "hi"},
	})
	if err != nil || res.IsError {
		t.Fatalf("writer put_text: err=%v res=%+v", err, res)
	}

	reader := connectMCP(t, srv.URL, mintToken(t, ts, `["collections:read"]`))
	res, err = reader.CallTool(context.Background(), &gosdk.CallToolParams{
		Name: "artifact_put_text", Arguments: map[string]any{"collection": "demo", "path": "b.txt", "text": "no"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("read-only token was able to write through the adapter")
	}
	if text := res.Content[0].(*gosdk.TextContent).Text; !strings.Contains(text, "403") && !strings.Contains(text, "insufficient scope") {
		t.Errorf("error text = %q, want the API's forbidden error", text)
	}

	bad := connectMCP(t, srv.URL, "tok_doesnotexist")
	res, err = bad.CallTool(context.Background(), &gosdk.CallToolParams{
		Name: "artifact_list_tree", Arguments: map[string]any{"collection": "demo"},
	})
	if err != nil || !res.IsError {
		t.Errorf("invalid token: err=%v res=%+v, want a tool error from the API's 401", err, res)
	}
}

// TestAdapter_SessionBoundToToken: a session initialized with token A cannot
// be driven with token B.
func TestAdapter_SessionBoundToToken(t *testing.T) {
	ts := testserver.New(t)
	srv := httptest.NewServer(New(Config{BaseURL: ts.BaseURL}).Handler())
	t.Cleanup(srv.Close)

	post := func(token, sid, body string) *http.Response {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
			req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post(ts.AdminToken, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"0"}}}`)
	resp.Body.Close()
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != http.StatusOK || sid == "" {
		t.Fatalf("initialize = %d, session %q", resp.StatusCode, sid)
	}
	other := mintToken(t, ts, `["collections:read"]`)
	resp = post(other, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("session hijack with another token = %d, want 401", resp.StatusCode)
	}
	resp = post(ts.AdminToken, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Errorf("same token on its own session = %d", resp.StatusCode)
	}
}
