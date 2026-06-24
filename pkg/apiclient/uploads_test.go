package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_BeginUpload(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/collections/demo/uploads" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req BeginUploadRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Path != "big.bin" {
			t.Errorf("path = %q", req.Path)
		}
		_ = json.NewEncoder(w).Encode(UploadSession{
			UploadURL: "https://x.example.com/api/v1/uploads/upl_1",
			UploadID:  "upl_1", Method: "PUT", ExpiresAt: "2026-07-01T00:00:00Z",
		})
	})
	session, err := c.BeginUpload(context.Background(), "demo", BeginUploadRequest{Path: "big.bin"})
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	if session.UploadID != "upl_1" || session.UploadURL == "" {
		t.Errorf("session = %+v", session)
	}
}
