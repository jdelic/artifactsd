package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/internal/testserver"
	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// wantToolNames are the SPEC §16 tool names this adapter must expose.
var wantToolNames = []string{
	"artifact_create_collection",
	"artifact_put_text",
	"artifact_begin_upload",
	"artifact_get_metadata",
	"artifact_set_metadata",
	"artifact_delete",
	"artifact_list_tree",
	"artifact_get_public_url",
	"artifact_create_share",
	"artifact_get_crawl_tree",
	// upload request links (design/15 §7)
	"artifact_create_upload_request",
	"artifact_list_upload_requests",
	"artifact_get_upload_request",
	"artifact_revoke_upload_request",
}

// mcpHarness wires a real artifactsd test server, an apiclient pointed at it,
// and an MCP client connected over Streamable HTTP — the same path a real
// agent uses, and the only way to actually exercise the SDK's schema
// inference/validation rather than just trusting it by inspection.
type mcpHarness struct {
	t  *testing.T
	ts *testserver.Server
	cs *gosdk.ClientSession
}

func newMCPHarness(t *testing.T) *mcpHarness {
	t.Helper()
	ts := testserver.New(t)
	client := apiclient.New(apiclient.Config{BaseURL: ts.BaseURL, Token: ts.AdminToken})
	srv := New(Config{Client: client, BaseURL: ts.BaseURL})

	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	mc := gosdk.NewClient(&gosdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := mc.Connect(context.Background(), &gosdk.StreamableClientTransport{Endpoint: httpSrv.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &mcpHarness{t: t, ts: ts, cs: cs}
}

// call invokes a tool and fails the test if the call errored at the protocol
// or tool level; it returns the decoded structured output.
func (h *mcpHarness) call(name string, args map[string]any) map[string]any {
	h.t.Helper()
	res, err := h.cs.CallTool(context.Background(), &gosdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("%s: protocol error: %v", name, err)
	}
	if res.IsError {
		h.t.Fatalf("%s: tool error: %+v", name, res.Content)
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		h.t.Fatalf("%s: structured content = %T, want map[string]any (%+v)", name, res.StructuredContent, res.StructuredContent)
	}
	return out
}

func TestRegisterTools_ListsAll(t *testing.T) {
	h := newMCPHarness(t)
	tools, err := h.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := make(map[string]bool, len(tools.Tools))
	for _, tl := range tools.Tools {
		got[tl.Name] = true
		if tl.Description == "" {
			t.Errorf("tool %q has no description", tl.Name)
		}
	}
	for _, name := range wantToolNames {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
	if len(tools.Tools) != len(wantToolNames) {
		t.Errorf("got %d tools, want %d: %v", len(tools.Tools), len(wantToolNames), tools.Tools)
	}
}

// TestTools_EndToEnd drives the SPEC §16 example flow through real MCP calls
// against a real artifactsd instance: create a collection, put text, read and
// patch its metadata (exercising the typed meta.Document input schema — see
// tool_files.go), list and crawl the tree, compose a public URL, create a
// share, begin a large upload, and delete the file.
func TestTools_EndToEnd(t *testing.T) {
	h := newMCPHarness(t)

	colOut := h.call("artifact_create_collection", map[string]any{
		"name": "run-8f31c2", "visibility": "public", "crawlable": true, "indexable": true,
	})
	if colOut["name"] != "run-8f31c2" || colOut["visibility"] != "public" {
		t.Fatalf("create_collection out = %+v", colOut)
	}

	fileOut := h.call("artifact_put_text", map[string]any{
		"collection": "run-8f31c2", "path": "index.html", "text": "<html>hi</html>", "content_type": "text/html",
	})
	if fileOut["content_type"] != "text/html" {
		t.Fatalf("put_text out = %+v", fileOut)
	}

	metaOut := h.call("artifact_get_metadata", map[string]any{"collection": "run-8f31c2", "path": "index.html"})
	if _, ok := metaOut["effective"]; !ok {
		t.Fatalf("get_metadata out = %+v, want an effective field", metaOut)
	}

	setOut := h.call("artifact_set_metadata", map[string]any{
		"collection": "run-8f31c2", "path": "index.html",
		"metadata": map[string]any{"crawl": map[string]any{"crawlable": true}},
	})
	own, ok := setOut["own"].(map[string]any)
	if !ok {
		t.Fatalf("set_metadata out = %+v, want an own object", setOut)
	}
	crawl, ok := own["crawl"].(map[string]any)
	if !ok || crawl["crawlable"] != true {
		t.Errorf("set_metadata own.crawl = %+v, want crawlable true", own["crawl"])
	}

	treeOut := h.call("artifact_list_tree", map[string]any{"collection": "run-8f31c2"})
	items, _ := treeOut["items"].([]any)
	if len(items) != 1 {
		t.Errorf("list_tree items = %+v, want 1 entry", items)
	}

	crawlOut := h.call("artifact_get_crawl_tree", map[string]any{"collection": "run-8f31c2"})
	if crawlOut["collection"] != "run-8f31c2" {
		t.Errorf("get_crawl_tree out = %+v", crawlOut)
	}

	urlOut := h.call("artifact_get_public_url", map[string]any{"collection": "run-8f31c2", "path": "index.html"})
	wantURL := h.ts.BaseURL + "/c/run-8f31c2/index.html"
	if urlOut["url"] != wantURL {
		t.Errorf("get_public_url = %v, want %v", urlOut["url"], wantURL)
	}

	shareOut := h.call("artifact_create_share", map[string]any{
		"collection": "run-8f31c2", "visibility": "unlisted", "expires_in": "7d",
	})
	if shareOut["share_id"] == "" || shareOut["url"] == "" {
		t.Fatalf("create_share out = %+v", shareOut)
	}

	uploadOut := h.call("artifact_begin_upload", map[string]any{"collection": "run-8f31c2", "path": "report.pdf"})
	if uploadOut["upload_url"] == "" {
		t.Fatalf("begin_upload out = %+v", uploadOut)
	}

	delOut := h.call("artifact_delete", map[string]any{"collection": "run-8f31c2", "path": "index.html"})
	if delOut["deleted"] != true {
		t.Errorf("delete out = %+v, want deleted true", delOut)
	}
}

// TestSetMetadata_Expiry covers the artifactsd-696 bug report: an agent must
// be able to put an expiry on a collection (empty path) and on a file through
// artifact_set_metadata, and the patch must round-trip into own metadata.
func TestSetMetadata_Expiry(t *testing.T) {
	h := newMCPHarness(t)
	h.call("artifact_create_collection", map[string]any{"name": "expiring"})
	h.call("artifact_put_text", map[string]any{"collection": "expiring", "path": "a.txt", "text": "x"})

	colOut := h.call("artifact_set_metadata", map[string]any{
		"collection": "expiring",
		"metadata":   map[string]any{"expiry": map[string]any{"expires_at": "2026-08-01T00:00:00Z"}},
	})
	own, _ := colOut["own"].(map[string]any)
	expiry, _ := own["expiry"].(map[string]any)
	if expiry["expires_at"] != "2026-08-01T00:00:00Z" {
		t.Errorf("collection own.expiry = %+v, want expires_at 2026-08-01T00:00:00Z", own["expiry"])
	}

	fileOut := h.call("artifact_set_metadata", map[string]any{
		"collection": "expiring", "path": "a.txt",
		"metadata": map[string]any{"expiry": map[string]any{"delete_after_creation": "7d"}},
	})
	own, _ = fileOut["own"].(map[string]any)
	expiry, _ = own["expiry"].(map[string]any)
	if expiry["delete_after_creation"] != "7d" {
		t.Errorf("file own.expiry = %+v, want delete_after_creation 7d", own["expiry"])
	}
}

// TestSetMetadata_UnknownExpiryFieldRejected pins down the failure mode from
// the artifactsd-696 bug report: a guessed field like expiry.absolute must be
// rejected by the SDK's input-schema validation (additionalProperties: false)
// with an error naming the offending property, instead of reaching the server.
func TestSetMetadata_UnknownExpiryFieldRejected(t *testing.T) {
	h := newMCPHarness(t)
	h.call("artifact_create_collection", map[string]any{"name": "demo"})

	res, err := h.cs.CallTool(context.Background(), &gosdk.CallToolParams{
		Name: "artifact_set_metadata",
		Arguments: map[string]any{
			"collection": "demo",
			"metadata":   map[string]any{"expiry": map[string]any{"absolute": "2026-08-01T00:00:00Z"}},
		},
	})
	var text string
	switch {
	case err != nil:
		text = err.Error() // schema violations may surface as protocol-level errors
	case res.IsError:
		tc, ok := res.Content[0].(*gosdk.TextContent)
		if !ok {
			t.Fatalf("content[0] = %T, want TextContent", res.Content[0])
		}
		text = tc.Text
	default:
		t.Fatal("want an error for an unknown expiry field, got success")
	}
	if !strings.Contains(text, "absolute") {
		t.Errorf("error %q does not name the offending property", text)
	}
}

// TestSetMetadata_SchemaExposesExpiryFields asserts the published inputSchema
// fully describes the metadata document — the discoverability contract agents
// rely on (artifactsd-696). If metadata ever degrades back to an opaque
// object, this fails.
func TestSetMetadata_SchemaExposesExpiryFields(t *testing.T) {
	h := newMCPHarness(t)
	tools, err := h.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != "artifact_set_metadata" {
			continue
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("marshal input schema: %v", err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal input schema: %v", err)
		}
		expiry := dig(schema, "properties", "metadata", "properties", "expiry")
		if expiry == nil {
			t.Fatalf("inputSchema has no metadata.expiry property: %s", raw)
		}
		for _, field := range []string{"expires_at", "delete_after_creation", "delete_after_last_access"} {
			prop, _ := dig(expiry, "properties", field).(map[string]any)
			if prop == nil {
				t.Errorf("metadata.expiry schema missing field %q", field)
			} else if desc, _ := prop["description"].(string); desc == "" {
				t.Errorf("metadata.expiry.%s has no description", field)
			}
		}
		return
	}
	t.Fatal("artifact_set_metadata not found in tool list")
}

// dig walks nested map[string]any keys, returning nil when any hop is missing.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func TestTools_CreateShareBearerToken(t *testing.T) {
	h := newMCPHarness(t)
	h.call("artifact_create_collection", map[string]any{"name": "demo"})
	out := h.call("artifact_create_share", map[string]any{"collection": "demo", "bearer_token": true})
	if out["generated_bearer_token"] == "" || out["generated_bearer_token"] == nil {
		t.Errorf("create_share out = %+v, want a generated_bearer_token", out)
	}
}

func TestTools_NotFoundMapsToActionableError(t *testing.T) {
	h := newMCPHarness(t)
	res, err := h.cs.CallTool(context.Background(), &gosdk.CallToolParams{
		Name: "artifact_get_metadata", Arguments: map[string]any{"collection": "nope", "path": "x"},
	})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("want a tool error for a missing collection")
	}
	text, ok := res.Content[0].(*gosdk.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want TextContent", res.Content[0])
	}
	if !strings.Contains(text.Text, "404") {
		t.Errorf("error text = %q, want it to mention the HTTP status", text.Text)
	}
}

func TestNew_CarriesClientAndBaseURL(t *testing.T) {
	c := apiclient.New(apiclient.Config{BaseURL: "https://x.example.com", Token: "tok_test"})
	s := New(Config{Client: c, BaseURL: "https://x.example.com"})
	if s.Client != c {
		t.Error("New did not carry the configured apiclient.Client through to Server.Client")
	}
	if s.BaseURL != "https://x.example.com" {
		t.Errorf("BaseURL = %q", s.BaseURL)
	}
	if s.MCP == nil {
		t.Error("New did not build an underlying MCP server")
	}
}
