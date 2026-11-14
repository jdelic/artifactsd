package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestFileCap covers ARTIFACTSD_MAX_FILES_PER_COLLECTION on the token paths:
// a new file beyond the cap is rejected with 409 on PUT and large upload,
// replacing an existing file at the cap still works, and folders do not count.
func TestFileCap(t *testing.T) {
	h := newHarnessEnv(t, map[string]string{"ARTIFACTSD_MAX_FILES_PER_COLLECTION": "2"})
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	put := func(p, body string) int {
		return h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/"+p, []byte(body), nil).Code
	}
	// Two files in nested folders: the folders must not count toward the cap.
	if c := put("a/b/one.txt", "1"); c != http.StatusCreated {
		t.Fatalf("first put = %d", c)
	}
	if c := put("a/two.txt", "2"); c != http.StatusCreated {
		t.Fatalf("second put = %d", c)
	}

	rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/three.txt", []byte("3"), nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "file limit (2)") {
		t.Fatalf("put over cap = %d (%s), want 409 with the limit", rec.Code, rec.Body)
	}
	if c := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/three.txt", nil, nil).Code; c != http.StatusNotFound {
		t.Errorf("rejected file exists: GET = %d", c)
	}

	// Replacing an existing file at the cap is fine.
	if c := put("a/two.txt", "2b"); c != http.StatusOK {
		t.Errorf("overwrite at cap = %d, want 200", c)
	}

	// Large upload: begin is rejected early for a new path...
	rec = h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/uploads", []byte(`{"path":"big.bin"}`), nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("begin upload over cap = %d (%s), want 409", rec.Code, rec.Body)
	}
	// ...but allowed for an existing one.
	rec = h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/uploads", []byte(`{"path":"a/two.txt"}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("begin upload replacing a file = %d (%s)", rec.Code, rec.Body)
	}
	var sess uploadSessionView
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if c := h.do(t, "", http.MethodPut, "/api/v1/uploads/"+sess.UploadID, []byte("2c"), nil).Code; c != http.StatusOK {
		t.Errorf("large-upload overwrite at cap = %d, want 200", c)
	}
}

// TestFileCap_LargeUploadRecheckedAtCommit: a session begun while there was
// room is rejected at PUT time once the collection has filled up.
func TestFileCap_LargeUploadRecheckedAtCommit(t *testing.T) {
	h := newHarnessEnv(t, map[string]string{"ARTIFACTSD_MAX_FILES_PER_COLLECTION": "1"})
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/uploads", []byte(`{"path":"late.bin"}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("begin = %d (%s)", rec.Code, rec.Body)
	}
	var sess uploadSessionView
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if c := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/first.txt", []byte("1"), nil).Code; c != http.StatusCreated {
		t.Fatalf("put = %d", c)
	}
	if rec := h.do(t, "", http.MethodPut, "/api/v1/uploads/"+sess.UploadID, []byte("x"), nil); rec.Code != http.StatusConflict {
		t.Errorf("PUT after the collection filled = %d (%s), want 409", rec.Code, rec.Body)
	}
}
