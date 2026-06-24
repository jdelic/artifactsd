package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// mkShareHarness returns a harness with a "demo" collection holding one file.
func mkShareHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil); rec.Code != http.StatusCreated {
		t.Fatalf("create collection = %d (%s)", rec.Code, rec.Body)
	}
	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/docs/readme.txt",
		[]byte("hello"), map[string]string{"Content-Type": "text/plain"}); rec.Code != http.StatusCreated {
		t.Fatalf("put file = %d (%s)", rec.Code, rec.Body)
	}
	return h
}

func TestShares_CRUD(t *testing.T) {
	h := mkShareHarness(t)

	// Create a share for the file.
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/shares",
		[]byte(`{"target_path":"docs/readme.txt","metadata":{"access":{"visibility":"unlisted"}}}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share = %d (%s)", rec.Code, rec.Body)
	}
	var sv shareView
	_ = json.Unmarshal(rec.Body.Bytes(), &sv)
	if sv.TargetKind != "file" || sv.TargetPath != "docs/readme.txt" {
		t.Errorf("share view = %+v", sv)
	}
	if sv.ShareID == "" || !bytes.HasPrefix([]byte(sv.ShareID), []byte("shr_")) {
		t.Errorf("share id = %q, want shr_ prefix", sv.ShareID)
	}
	if sv.URL != "https://x.example.com/s/"+sv.ShareID+"/" {
		t.Errorf("share url = %q", sv.URL)
	}

	// Get by share_id.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/shares/"+sv.ShareID, nil, nil); rec.Code != http.StatusOK {
		t.Errorf("get share = %d (%s)", rec.Code, rec.Body)
	}

	// List shares for the collection.
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/shares", nil, nil)
	var list listResponse[shareView]
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Errorf("list shares = %d, want 1", len(list.Items))
	}

	// Patch the share policy (merge).
	if rec := h.do(t, h.token, http.MethodPatch, "/api/v1/shares/"+sv.ShareID,
		[]byte(`{"expiry":{"expires_at":"2099-01-01T00:00:00Z"}}`), nil); rec.Code != http.StatusOK {
		t.Errorf("patch share = %d (%s)", rec.Code, rec.Body)
	}
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/shares/"+sv.ShareID, nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &sv)
	if sv.ExpiresAt == nil || *sv.ExpiresAt != "2099-01-01T00:00:00Z" {
		t.Errorf("share expires_at = %v, want 2099", sv.ExpiresAt)
	}

	// Delete.
	if rec := h.do(t, h.token, http.MethodDelete, "/api/v1/shares/"+sv.ShareID, nil, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete share = %d", rec.Code)
	}
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/shares/"+sv.ShareID, nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", rec.Code)
	}
}

func TestShares_WholeCollection(t *testing.T) {
	h := mkShareHarness(t)
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/shares", []byte(`{"target_path":""}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create collection share = %d (%s)", rec.Code, rec.Body)
	}
	var sv shareView
	_ = json.Unmarshal(rec.Body.Bytes(), &sv)
	if sv.TargetKind != "collection" || sv.TargetPath != "" {
		t.Errorf("collection share = %+v", sv)
	}
}

func TestShares_MissingTarget(t *testing.T) {
	h := mkShareHarness(t)
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/shares",
		[]byte(`{"target_path":"nope.txt"}`), nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing target = %d, want 404", rec.Code)
	}
}

func TestShares_SecretRedaction(t *testing.T) {
	h := mkShareHarness(t)
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/shares",
		[]byte(`{"target_path":"docs/readme.txt","metadata":{"access":{"visibility":"unlisted","auth":[{"type":"bearer","token":"sekret"}]}}}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share = %d (%s)", rec.Code, rec.Body)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("sekret")) {
		t.Errorf("share metadata leaked cleartext token: %s", rec.Body)
	}
}

func TestShares_Scoped(t *testing.T) {
	h := mkShareHarness(t)
	// collections:read token may not create shares.
	if rec := h.do(t, h.roToken, http.MethodPost, "/api/v1/collections/demo/shares",
		[]byte(`{"target_path":""}`), nil); rec.Code != http.StatusForbidden {
		t.Errorf("ro token create share = %d, want 403", rec.Code)
	}
}
