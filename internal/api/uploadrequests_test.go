package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func mkUploadRequestHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"inbox"}`), nil); rec.Code != http.StatusCreated {
		t.Fatalf("create collection = %d (%s)", rec.Code, rec.Body)
	}
	return h
}

func TestUploadRequests_API_CRUD(t *testing.T) {
	h := mkUploadRequestHarness(t)

	// Create.
	body := []byte(`{
		"collection": "inbox",
		"target_folder": "uploads",
		"label": "Test Link",
		"file_metadata": {"access":{"visibility":"private"}},
		"allow_uploader_access": false,
		"single_use": false
	}`)
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/upload-requests", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create upload request = %d (%s)", rec.Code, rec.Body)
	}
	var uv uploadRequestView
	if err := json.Unmarshal(rec.Body.Bytes(), &uv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !bytes.HasPrefix([]byte(uv.RequestID), []byte("urq_")) {
		t.Errorf("request_id = %q, want urq_ prefix", uv.RequestID)
	}
	if uv.URL != "https://x.example.com/r/"+uv.RequestID {
		t.Errorf("url = %q", uv.URL)
	}
	if uv.Collection != "inbox" || uv.TargetFolder != "uploads" {
		t.Errorf("unexpected view: %+v", uv)
	}
	if uv.Label == nil || *uv.Label != "Test Link" {
		t.Errorf("label = %v, want 'Test Link'", uv.Label)
	}
	if uv.Status != "active" {
		t.Errorf("status = %q, want active", uv.Status)
	}

	// Get by request_id.
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/upload-requests/"+uv.RequestID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get upload request = %d (%s)", rec.Code, rec.Body)
	}

	// List.
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/upload-requests", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list upload requests = %d (%s)", rec.Code, rec.Body)
	}
	var list listResponse[uploadRequestView]
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Errorf("list items = %d, want 1", len(list.Items))
	}

	// Patch label.
	rec = h.do(t, h.token, http.MethodPatch, "/api/v1/upload-requests/"+uv.RequestID,
		[]byte(`{"label":"Updated Label"}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch upload request = %d (%s)", rec.Code, rec.Body)
	}
	var patched uploadRequestView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.Label == nil || *patched.Label != "Updated Label" {
		t.Errorf("patched label = %v, want 'Updated Label'", patched.Label)
	}

	// List submissions (empty).
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/upload-requests/"+uv.RequestID+"/submissions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list submissions = %d (%s)", rec.Code, rec.Body)
	}
	var subList listResponse[uploadRequestSubmissionView]
	_ = json.Unmarshal(rec.Body.Bytes(), &subList)
	if len(subList.Items) != 0 {
		t.Errorf("submissions = %d, want 0", len(subList.Items))
	}

	// Delete (revoke).
	rec = h.do(t, h.token, http.MethodDelete, "/api/v1/upload-requests/"+uv.RequestID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete upload request = %d (%s)", rec.Code, rec.Body)
	}

	// Get after revoke still returns the record (with revoked status).
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/upload-requests/"+uv.RequestID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get after revoke = %d (%s)", rec.Code, rec.Body)
	}
	var revoked uploadRequestView
	_ = json.Unmarshal(rec.Body.Bytes(), &revoked)
	if revoked.Status != "revoked" {
		t.Errorf("status after revoke = %q, want revoked", revoked.Status)
	}
}

func TestUploadRequests_API_RequiresAdmin(t *testing.T) {
	h := mkUploadRequestHarness(t)

	// Collections:read token must not reach upload-requests endpoints.
	rec := h.do(t, h.roToken, http.MethodGet, "/api/v1/upload-requests", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("list with ro token = %d, want 403", rec.Code)
	}
}

func TestUploadRequests_API_UnknownCollection(t *testing.T) {
	h := mkUploadRequestHarness(t)

	rec := h.do(t, h.token, http.MethodPost, "/api/v1/upload-requests",
		[]byte(`{"collection":"nosuchcollection"}`), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("create for missing collection = %d, want 404", rec.Code)
	}
}

func TestUploadRequests_API_MaxFileSizeValidation(t *testing.T) {
	h := mkUploadRequestHarness(t)

	// max_file_size over the server limit (default 1 GiB = 1073741824).
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/upload-requests",
		[]byte(`{"collection":"inbox","max_file_size":9999999999}`), nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with too-large max_file_size = %d, want 400", rec.Code)
	}
}

func TestUploadRequests_API_SingleUse(t *testing.T) {
	h := mkUploadRequestHarness(t)

	rec := h.do(t, h.token, http.MethodPost, "/api/v1/upload-requests",
		[]byte(`{"collection":"inbox","single_use":true}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create single_use = %d (%s)", rec.Code, rec.Body)
	}
	var uv uploadRequestView
	_ = json.Unmarshal(rec.Body.Bytes(), &uv)
	if !uv.SingleUse {
		t.Error("single_use should be true in response")
	}
}

func TestUploadRequests_API_NotFound(t *testing.T) {
	h := mkUploadRequestHarness(t)

	rec := h.do(t, h.token, http.MethodGet, "/api/v1/upload-requests/urq_doesnotexist", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get missing = %d, want 404", rec.Code)
	}
}
