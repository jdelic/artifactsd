package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestDeleteScopes: collections:write no longer implies delete; delete needs
// collections:delete / shares:delete (artifactsd-m9e.25).
func TestDeleteScopes(t *testing.T) {
	h := newHarness(t)
	setup := func() string {
		h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
		h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/d/f.txt", []byte("x"), nil)
		h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/g.txt", []byte("x"), nil)
		rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/shares", []byte(`{"target_path":""}`), nil)
		var sh struct {
			ShareID string `json:"share_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &sh)
		return sh.ShareID
	}
	shareID := setup()
	writer := mkToken(t, h.st, "writer", `["collections:write","shares:write"]`)
	deleter := mkToken(t, h.st, "deleter", `["collections:delete","shares:delete"]`)

	deletes := []struct{ name, target string }{
		{"file", "/api/v1/collections/demo/files/g.txt"},
		{"folder", "/api/v1/collections/demo/folders/d?recursive=true"},
		{"share", "/api/v1/shares/" + shareID},
		{"collection", "/api/v1/collections/demo?force=true"},
	}
	for _, d := range deletes {
		rec := h.do(t, writer, http.MethodDelete, d.target, nil, nil)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "requires") ||
			!strings.Contains(rec.Body.String(), ":delete") {
			t.Errorf("write-only DELETE %s = %d (%s), want 403 naming the delete scope", d.name, rec.Code, rec.Body)
		}
	}
	// The write token can still write.
	if c := h.do(t, writer, http.MethodPut, "/api/v1/collections/demo/files/w.txt", []byte("w"), nil).Code; c != http.StatusCreated {
		t.Errorf("write-only PUT = %d", c)
	}
	if c := h.do(t, writer, http.MethodPatch, "/api/v1/collections/demo/meta", []byte(`{"crawl":{"indexable":false}}`), nil).Code; c != http.StatusOK {
		t.Errorf("write-only PATCH = %d", c)
	}

	// The delete token can read and delete, but not write.
	if c := h.do(t, deleter, http.MethodGet, "/api/v1/collections/demo/files/g.txt", nil, nil).Code; c != http.StatusOK {
		t.Errorf("delete-only GET = %d", c)
	}
	for _, w := range []struct{ method, target, body string }{
		{http.MethodPut, "/api/v1/collections/demo/files/z.txt", "z"},
		{http.MethodPatch, "/api/v1/collections/demo/meta", `{}`},
		{http.MethodPost, "/api/v1/collections", `{"name":"other"}`},
	} {
		if c := h.do(t, deleter, w.method, w.target, []byte(w.body), nil).Code; c != http.StatusForbidden {
			t.Errorf("delete-only %s %s = %d, want 403", w.method, w.target, c)
		}
	}
	for _, d := range deletes {
		if rec := h.do(t, deleter, http.MethodDelete, d.target, nil, nil); rec.Code >= 300 {
			t.Errorf("delete-only DELETE %s = %d (%s)", d.name, rec.Code, rec.Body)
		}
	}
}
