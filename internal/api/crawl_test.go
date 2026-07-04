package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCrawlRoot_FiltersPrivateAndNonCrawlableByScope(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections",
		[]byte(`{"name":"pub","metadata":{"crawl":{"crawlable":true,"indexable":true}}}`), nil)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections",
		[]byte(`{"name":"priv","metadata":{"access":{"visibility":"private"},"crawl":{"crawlable":true}}}`), nil)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"hidden"}`), nil) // crawlable defaults false

	// Non-admin (collections:read only): private always hidden; non-crawlable
	// hidden too.
	rec := h.do(t, h.roToken, http.MethodGet, "/api/v1/crawl", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("crawl root = %d (%s)", rec.Code, rec.Body)
	}
	var body struct {
		Collections []crawlCollectionSummary `json:"collections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if names := names(body.Collections); len(names) != 1 || names[0] != "pub" {
		t.Errorf("non-admin crawl root = %v, want [pub]", names)
	}

	// Admin scope also sees the non-crawlable "hidden" collection, but never
	// the private one.
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/crawl", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	got := names(body.Collections)
	if len(got) != 2 || !contains(got, "pub") || !contains(got, "hidden") || contains(got, "priv") {
		t.Errorf("admin crawl root = %v, want [pub hidden] (no priv)", got)
	}
}

func TestCrawlCollection_FiltersAndDepth(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	put := func(p, content, md string) {
		t.Helper()
		headers := map[string]string{}
		if md != "" {
			headers[metadataHeader] = base64urlNoPad(md)
		}
		rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/"+p, []byte(content), headers)
		if rec.Code != http.StatusCreated {
			t.Fatalf("put %s = %d (%s)", p, rec.Code, rec.Body)
		}
	}
	put("index.html", "<h1>hi</h1>", `{"crawl":{"crawlable":true}}`)
	put("secret.txt", "shh", `{"access":{"visibility":"private"}}`)
	put("draft.txt", "wip", "") // crawlable defaults false
	put("assets/logo.png", "img", "")

	if rec := h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/folders/assets/meta",
		[]byte(`{"crawl":{"crawlable":true}}`), nil); rec.Code != http.StatusOK {
		t.Fatalf("patch assets folder = %d (%s)", rec.Code, rec.Body)
	}

	// Non-admin, default depth (full tree): index.html, assets/, and
	// assets/logo.png (inherits crawlable:true from the assets folder layer) —
	// not secret.txt (private) or draft.txt (non-crawlable).
	rec := h.do(t, h.roToken, http.MethodGet, "/api/v1/collections/demo/crawl", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("crawl demo = %d (%s)", rec.Code, rec.Body)
	}
	var resp crawlResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	got := entryPaths(resp.Entries)
	if len(got) != 3 || !contains(got, "index.html") || !contains(got, "assets/") || !contains(got, "assets/logo.png") {
		t.Errorf("non-admin default entries = %v, want [index.html assets/ assets/logo.png]", got)
	}

	// Admin scope: draft.txt now visible too (non-crawlable, admin-only);
	// still no secret.txt (private always hidden).
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/crawl", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	got = entryPaths(resp.Entries)
	if len(got) != 4 || !contains(got, "draft.txt") || contains(got, "secret.txt") {
		t.Errorf("admin default entries = %v, want [index.html assets/ assets/logo.png draft.txt] (no secret.txt)", got)
	}

	// depth=1 stops at immediate children: no recursion into assets/.
	rec = h.do(t, h.roToken, http.MethodGet, "/api/v1/collections/demo/crawl?depth=1", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	got = entryPaths(resp.Entries)
	if len(got) != 2 || !contains(got, "index.html") || !contains(got, "assets/") || contains(got, "assets/logo.png") {
		t.Errorf("depth=1 entries = %v, want [index.html assets/]", got)
	}

	// depth=2 recurses into assets/.
	rec = h.do(t, h.roToken, http.MethodGet, "/api/v1/collections/demo/crawl?depth=2", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	got = entryPaths(resp.Entries)
	if !contains(got, "assets/logo.png") {
		t.Errorf("depth=2 entries = %v, want assets/logo.png included", got)
	}

	// Negative and non-numeric depths are rejected on both tree and crawl
	// (uniform ?depth= contract, design/04 §4, design/05 §5).
	for _, d := range []string{"-1", "abc"} {
		if rec = h.do(t, h.roToken, http.MethodGet, "/api/v1/collections/demo/crawl?depth="+d, nil, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("crawl depth=%s = %d, want 400", d, rec.Code)
		}
		if rec = h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/tree?depth="+d, nil, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("tree depth=%s = %d, want 400", d, rec.Code)
		}
	}
}

func TestCrawlCollection_PathScopesToSubtree(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/assets/logo.png", []byte("img"), nil)
	h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/folders/assets/meta",
		[]byte(`{"crawl":{"crawlable":true}}`), nil)

	rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/crawl?path=assets", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("crawl path=assets = %d (%s)", rec.Code, rec.Body)
	}
	var resp crawlResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if got := entryPaths(resp.Entries); len(got) != 1 || got[0] != "assets/logo.png" {
		t.Errorf("path=assets entries = %v, want [assets/logo.png]", got)
	}
	if !strings.HasSuffix(resp.BaseURL, "/c/demo/assets/") {
		t.Errorf("base_url = %q, want suffix /c/demo/assets/", resp.BaseURL)
	}
}

func TestCrawlCollection_PrivateAncestorHidesDescendants(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/secret/data.txt",
		[]byte("x"), map[string]string{metadataHeader: base64urlNoPad(`{"crawl":{"crawlable":true}}`)})
	h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/folders/secret/meta",
		[]byte(`{"access":{"visibility":"private"},"crawl":{"crawlable":true}}`), nil)

	// Even admin scope, even unlimited depth (the default): a private ancestor
	// hides everything below it (existence non-disclosure), and the folder
	// itself is never listed.
	rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/crawl", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("crawl demo = %d (%s)", rec.Code, rec.Body)
	}
	var resp crawlResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if got := entryPaths(resp.Entries); len(got) != 0 {
		t.Errorf("entries = %v, want none (private ancestor)", got)
	}
}

func TestCrawlCollection_PrivateBaseIs404(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/secret/data.txt", []byte("x"), nil)
	h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/folders/secret/meta",
		[]byte(`{"access":{"visibility":"private"}}`), nil)

	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/crawl?path=secret", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("crawl private base = %d, want 404", rec.Code)
	}
}

func TestCrawlCollection_FileBaseIsConflict(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/a.txt", []byte("x"), nil)

	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/crawl?path=a.txt", nil, nil); rec.Code != http.StatusConflict {
		t.Errorf("crawl file base = %d, want 409", rec.Code)
	}
}

func TestCrawlCollection_ExpiredExcluded(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/old.txt", []byte("x"),
		map[string]string{metadataHeader: base64urlNoPad(`{"crawl":{"crawlable":true},"expiry":{"expires_at":"2000-01-01T00:00:00Z"}}`)})

	rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/crawl", nil, nil)
	var resp crawlResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if got := entryPaths(resp.Entries); len(got) != 0 {
		t.Errorf("entries = %v, want none (expired)", got)
	}
}

func TestCrawlCollection_RequiresAuth(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	if rec := h.do(t, "", http.MethodGet, "/api/v1/collections/demo/crawl", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("crawl no token = %d, want 401", rec.Code)
	}
	if rec := h.do(t, "", http.MethodGet, "/api/v1/crawl", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("crawl root no token = %d, want 401", rec.Code)
	}
}

func names(items []crawlCollectionSummary) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Collection
	}
	return out
}

func entryPaths(entries []crawlEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
