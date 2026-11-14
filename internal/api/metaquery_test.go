package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestMetaQuery: metadata is addressed with ?meta, so a file whose path ends
// in "/meta" is an ordinary file (artifactsd-m9e.23).
func TestMetaQuery(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	hdr := map[string]string{metadataHeader: base64urlNoPad(`{"access":{"visibility":"unlisted"}}`)}
	if c := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/docs/meta", []byte("i am a file"), hdr).Code; c != http.StatusCreated {
		t.Fatalf("PUT docs/meta = %d", c)
	}
	if c := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/a/meta/b.txt", []byte("nested"), nil).Code; c != http.StatusCreated {
		t.Fatalf("PUT a/meta/b.txt = %d", c)
	}

	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/docs/meta", nil, nil); rec.Code != http.StatusOK || rec.Body.String() != "i am a file" {
		t.Errorf("GET docs/meta = %d %q, want the file bytes", rec.Code, rec.Body)
	}
	rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/docs/meta?meta", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"docs/meta"`) || !strings.Contains(rec.Body.String(), "unlisted") {
		t.Errorf("GET docs/meta?meta = %d %s, want that file's metadata", rec.Code, rec.Body)
	}
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/a/meta/b.txt", nil, nil); rec.Body.String() != "nested" {
		t.Errorf("GET a/meta/b.txt = %q", rec.Body)
	}
	// docs is a folder; the files route resolves any node, as before.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/docs?meta", nil, nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"path":"docs"`) {
		t.Errorf("GET files/docs?meta = %d %s, want the folder's metadata", rec.Code, rec.Body)
	}
	if c := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/folders/docs?meta", nil, nil).Code; c != http.StatusOK {
		t.Errorf("GET folders/docs?meta = %d, want 200", c)
	}

	// PATCH works with ?meta (also on a file named .../meta) and is refused without.
	if c := h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/files/docs/meta?meta", []byte(`{"crawl":{"indexable":false}}`), nil).Code; c != http.StatusOK {
		t.Errorf("PATCH docs/meta?meta = %d, want 200", c)
	}
	for _, target := range []string{
		"/api/v1/collections/demo/files/docs/meta",
		"/api/v1/collections/demo/folders/docs",
	} {
		rec := h.do(t, h.token, http.MethodPatch, target, []byte(`{}`), nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "add ?meta") {
			t.Errorf("PATCH %s without ?meta = %d %s, want 400", target, rec.Code, rec.Body)
		}
	}
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/folders/docs", nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("GET folder without ?meta = %d, want 400", rec.Code)
	}
}
