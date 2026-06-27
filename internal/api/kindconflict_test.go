package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestKindConflict_FileAncestorRejectsNestedUpload covers artifactsd-2bw: once a
// file exists at a path, uploading "under" it (treating the file as a folder)
// must be rejected rather than wiring the new node's parent to a file.
func TestKindConflict_FileAncestorRejectsNestedUpload(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/a", []byte("file"), nil); rec.Code != http.StatusCreated {
		t.Fatalf("create file a = %d (%s)", rec.Code, rec.Body)
	}
	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/a/b.txt", []byte("x"), nil); rec.Code != http.StatusConflict {
		t.Fatalf("upload under a file = %d, want 409 (%s)", rec.Code, rec.Body)
	}
	// The file is untouched and 'a' did not become a folder.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/a", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("get a after conflict = %d", rec.Code)
	}
}

// TestKindConflict_FolderPathNotReplacedByFile covers artifactsd-2bw: a folder
// with descendants must not be overwritten in place by a file (which would
// orphan its children).
func TestKindConflict_FolderPathNotReplacedByFile(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/d/x.txt", []byte("x"), nil); rec.Code != http.StatusCreated {
		t.Fatalf("create d/x.txt = %d (%s)", rec.Code, rec.Body)
	}
	// 'd' is now a folder; PUT to 'd' as a file must 409.
	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/d", []byte("nope"), nil); rec.Code != http.StatusConflict {
		t.Fatalf("replace folder with file = %d, want 409 (%s)", rec.Code, rec.Body)
	}
	// The descendant survives.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/d/x.txt", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("get d/x.txt after conflict = %d", rec.Code)
	}
}

// TestKindConflict_LargeUploadRespectsInvariant covers the large-upload path.
func TestKindConflict_LargeUploadRespectsInvariant(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/a", []byte("file"), nil)

	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/uploads", []byte(`{"path":"a/b.bin"}`), nil)
	var sess uploadSessionView
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if rec := h.do(t, "", http.MethodPut, "/api/v1/uploads/"+sess.UploadID, []byte("x"), nil); rec.Code != http.StatusConflict {
		t.Fatalf("large upload under a file = %d, want 409 (%s)", rec.Code, rec.Body)
	}
}

// TestKindConflict_FolderMaterializationUnderFile covers folder metadata
// materialization: patching a folder whose ancestor is a file must 409.
func TestKindConflict_FolderMaterializationUnderFile(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/a", []byte("file"), nil)

	if rec := h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/folders/a/sub/meta",
		[]byte(`{"access":{"visibility":"public"}}`), nil); rec.Code != http.StatusConflict {
		t.Fatalf("materialize folder under a file = %d, want 409 (%s)", rec.Code, rec.Body)
	}
	// Patching a folder directly at an existing file path also 409s.
	if rec := h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/folders/a/meta",
		[]byte(`{"access":{"visibility":"public"}}`), nil); rec.Code != http.StatusConflict {
		t.Fatalf("folder meta at a file path = %d, want 409 (%s)", rec.Code, rec.Body)
	}
}
