package serve_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/testserver"
)

func call(t *testing.T, ts *testserver.Server, method, path, token, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, ts.BaseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func metaHdr(doc string) map[string]string {
	return map[string]string{"Artifactsd-Metadata": base64.RawURLEncoding.EncodeToString([]byte(doc))}
}

func TestContentHeaders_Allowlist(t *testing.T) {
	ts := testserver.New(t)
	call(t, ts, http.MethodPost, "/api/v1/collections", ts.AdminToken, `{"name":"demo"}`, map[string]string{"Content-Type": "application/json"})

	if r := call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/a.txt", ts.AdminToken, "a",
		metaHdr(`{"content":{"headers":{"NEL":"{\"report_to\":\"x\",\"max_age\":1}"}}}`)); r.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT with NEL = %d, want 400", r.StatusCode)
	}
	if r := call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/a.txt", ts.AdminToken, "a",
		metaHdr(`{"content":{"headers":{"X-Foo":"a\r\nSet-Cookie: x=1"}}}`)); r.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT with CRLF value = %d, want 400", r.StatusCode)
	}
	if r := call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/a.txt", ts.AdminToken, "a",
		metaHdr(`{"content":{"headers":{"Cache-Control":"no-store"}}}`)); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT with Cache-Control = %d, want 201", r.StatusCode)
	}
	if r := call(t, ts, http.MethodGet, "/c/demo/a.txt", "", "", nil); r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("served Cache-Control = %q, want no-store", r.Header.Get("Cache-Control"))
	}
}

func TestContentHeaders_LegacyStoredHeaderNotServed(t *testing.T) {
	ts := testserver.New(t)
	call(t, ts, http.MethodPost, "/api/v1/collections", ts.AdminToken, `{"name":"demo"}`, map[string]string{"Content-Type": "application/json"})
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/old.txt", ts.AdminToken, "x", nil)

	// Simulate a document stored before the allowlist existed.
	ctx := context.Background()
	c, err := ts.Store.GetCollectionByName(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	n, err := ts.Store.GetNode(ctx, c.ID, "old.txt")
	if err != nil {
		t.Fatal(err)
	}
	n.Metadata = []byte(`{"content":{"headers":{"NEL":"{}","Clear-Site-Data":"\"cookies\"","X-Ok":"1"}}}`)
	if err := ts.Store.UpsertNode(ctx, n); err != nil {
		t.Fatal(err)
	}

	r := call(t, ts, http.MethodGet, "/c/demo/old.txt", "", "", nil)
	if r.StatusCode != http.StatusOK || r.Header.Get("Nel") != "" || r.Header.Get("Clear-Site-Data") != "" {
		t.Errorf("legacy headers served: status %d NEL=%q CSD=%q", r.StatusCode, r.Header.Get("Nel"), r.Header.Get("Clear-Site-Data"))
	}
	if r.Header.Get("X-Ok") != "1" {
		t.Errorf("allowed legacy header dropped")
	}
	// Patching an unrelated field still works.
	if r := call(t, ts, http.MethodPatch, "/api/v1/collections/demo/files/old.txt?meta", ts.AdminToken,
		`{"crawl":{"indexable":false}}`, map[string]string{"Content-Type": "application/json"}); r.StatusCode != http.StatusOK {
		t.Errorf("unrelated PATCH on a legacy document = %d, want 200", r.StatusCode)
	}
}

func TestContentHeaders_OperatorExtension(t *testing.T) {
	ts := testserver.NewWithEnv(t, map[string]string{"ARTIFACTSD_EXTRA_ALLOWED_HEADERS": "Server-Timing"})
	call(t, ts, http.MethodPost, "/api/v1/collections", ts.AdminToken, `{"name":"demo"}`, map[string]string{"Content-Type": "application/json"})
	if r := call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/a.txt", ts.AdminToken, "a",
		metaHdr(`{"content":{"headers":{"Server-Timing":"db;dur=1"}}}`)); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT with extra-allowed header = %d", r.StatusCode)
	}
	if r := call(t, ts, http.MethodGet, "/c/demo/a.txt", "", "", nil); r.Header.Get("Server-Timing") != "db;dur=1" {
		t.Errorf("served Server-Timing = %q", r.Header.Get("Server-Timing"))
	}

	_, err := config.LoadAndValidate([]string{"--base-url", "https://x.example.com", "--data", t.TempDir()},
		func(k string) string {
			if k == "ARTIFACTSD_EXTRA_ALLOWED_HEADERS" {
				return "Set-Cookie"
			}
			return ""
		}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Set-Cookie") {
		t.Errorf("startup with Set-Cookie extra header: err = %v, want error", err)
	}
}
