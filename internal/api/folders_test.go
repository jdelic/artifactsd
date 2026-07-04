package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestFolders_MetaAndDelete(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/docs/a.txt", []byte("a"), nil)

	// PATCH folder meta materializes the folder.
	if rec := h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/folders/docs/meta",
		[]byte(`{"access":{"directory_listing":true}}`), nil); rec.Code != http.StatusOK {
		t.Fatalf("patch folder = %d (%s)", rec.Code, rec.Body)
	}
	// GET folder meta reflects it.
	rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/folders/docs/meta", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get folder meta = %d", rec.Code)
	}

	// DELETE folder removes the subtree (the file under it too).
	if rec := h.do(t, h.token, http.MethodDelete, "/api/v1/collections/demo/folders/docs", nil, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete folder = %d, want 204", rec.Code)
	}
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/docs/a.txt", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("file under deleted folder = %d, want 404", rec.Code)
	}
}

func TestFolders_DeleteFileViaFolderConflict(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/a.txt", []byte("a"), nil)
	// Deleting a file path via the folders endpoint → 409.
	if rec := h.do(t, h.token, http.MethodDelete, "/api/v1/collections/demo/folders/a.txt", nil, nil); rec.Code != http.StatusConflict {
		t.Errorf("delete file via folders = %d, want 409", rec.Code)
	}
}

// TestFiles_DeleteFolderViaFileConflict is the symmetric guard
// (artifactsd-a0c): before it, DELETE .../files/{folder} cascade-deleted the
// whole subtree through the single-file route.
func TestFiles_DeleteFolderViaFileConflict(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/docs/a.txt", []byte("a"), nil)

	// Deleting a folder path via the files endpoint → 409, nothing removed.
	if rec := h.do(t, h.token, http.MethodDelete, "/api/v1/collections/demo/files/docs", nil, nil); rec.Code != http.StatusConflict {
		t.Errorf("delete folder via files = %d, want 409", rec.Code)
	}
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/docs/a.txt", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("file after rejected delete = %d, want 200 (subtree must survive)", rec.Code)
	}
}

func TestTree(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	for _, p := range []string{"a.txt", "docs/b.txt", "docs/sub/c.txt"} {
		h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/"+p, []byte("x"), nil)
	}

	// Full tree: 3 files + 2 folders (docs, docs/sub).
	rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/tree", nil, nil)
	var full listResponse[treeNode]
	_ = json.Unmarshal(rec.Body.Bytes(), &full)
	if len(full.Items) != 5 {
		t.Errorf("full tree = %d items, want 5: %+v", len(full.Items), full.Items)
	}

	// Scoped to docs/ with depth 1: docs, docs/b.txt, docs/sub (not docs/sub/c.txt).
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/tree?path=docs&depth=1", nil, nil)
	var scoped listResponse[treeNode]
	_ = json.Unmarshal(rec.Body.Bytes(), &scoped)
	for _, it := range scoped.Items {
		if it.Path == "docs/sub/c.txt" {
			t.Errorf("depth=1 should exclude docs/sub/c.txt: %+v", scoped.Items)
		}
	}
	if len(scoped.Items) != 3 {
		t.Errorf("scoped tree = %d items, want 3: %+v", len(scoped.Items), scoped.Items)
	}
}
