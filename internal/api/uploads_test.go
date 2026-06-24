package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestUploads_BeginPutStatus(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/uploads",
		[]byte(`{"path":"audio/summary.mp3","metadata":{"content":{"content_type":"audio/mpeg"}}}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("begin = %d (%s)", rec.Code, rec.Body)
	}
	var sess uploadSessionView
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sess.UploadID == "" || sess.Method != http.MethodPut || sess.UploadURL == "" {
		t.Fatalf("session view = %+v", sess)
	}

	// PUT with no token at all — possession of the upload_id is the capability.
	content := []byte("fake mp3 bytes")
	rec = h.do(t, "", http.MethodPut, "/api/v1/uploads/"+sess.UploadID, content, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("put = %d (%s)", rec.Code, rec.Body)
	}
	var fv fileView
	_ = json.Unmarshal(rec.Body.Bytes(), &fv)
	if fv.Path != "audio/summary.mp3" || fv.ContentType != "audio/mpeg" || fv.Size != int64(len(content)) {
		t.Errorf("file view = %+v", fv)
	}

	// The file is now reachable through the normal files API.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/audio/summary.mp3", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("get file = %d", rec.Code)
	}

	// Single-use: a second PUT to the same upload_id must be rejected.
	if rec := h.do(t, "", http.MethodPut, "/api/v1/uploads/"+sess.UploadID, []byte("again"), nil); rec.Code != http.StatusConflict {
		t.Errorf("second put = %d, want 409", rec.Code)
	}

	// Status, also without a token.
	rec = h.do(t, "", http.MethodGet, "/api/v1/uploads/"+sess.UploadID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	var status uploadStatusView
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if status.Status != "completed" || status.CompletedAt == nil {
		t.Errorf("status view = %+v", status)
	}
}

func TestUploads_AbortAndUnknown(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/uploads", []byte(`{"path":"big.bin"}`), nil)
	var sess uploadSessionView
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	// Abort without a token.
	if rec := h.do(t, "", http.MethodDelete, "/api/v1/uploads/"+sess.UploadID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("abort = %d (%s)", rec.Code, rec.Body)
	}
	// Aborted session is gone.
	if rec := h.do(t, "", http.MethodGet, "/api/v1/uploads/"+sess.UploadID, nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("status after abort = %d, want 404", rec.Code)
	}
	if rec := h.do(t, "", http.MethodPut, "/api/v1/uploads/"+sess.UploadID, []byte("x"), nil); rec.Code != http.StatusNotFound {
		t.Errorf("put after abort = %d, want 404", rec.Code)
	}

	// Unknown id entirely.
	if rec := h.do(t, "", http.MethodGet, "/api/v1/uploads/upl_doesnotexist", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown status = %d, want 404", rec.Code)
	}
}

func TestUploads_ExpiredSessionRejected(t *testing.T) {
	h := newHarnessEnv(t, map[string]string{"ARTIFACTSD_UPLOAD_TTL": "1ns"})
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/uploads", []byte(`{"path":"slow.bin"}`), nil)
	var sess uploadSessionView
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	if rec := h.do(t, "", http.MethodPut, "/api/v1/uploads/"+sess.UploadID, []byte("x"), nil); rec.Code != http.StatusNotFound {
		t.Errorf("put expired = %d, want 404", rec.Code)
	}
}

func TestUploads_RequiresWriteScope(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	if rec := h.do(t, h.roToken, http.MethodPost, "/api/v1/collections/demo/uploads", []byte(`{"path":"x.bin"}`), nil); rec.Code != http.StatusForbidden {
		t.Errorf("ro begin = %d, want 403", rec.Code)
	}
	if rec := h.do(t, "", http.MethodPost, "/api/v1/collections/demo/uploads", []byte(`{"path":"x.bin"}`), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no-token begin = %d, want 401", rec.Code)
	}
}
