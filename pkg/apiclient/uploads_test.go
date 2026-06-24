package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestClient_PutUploadURL(t *testing.T) {
	var gotAuth, gotContentType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/uploads/upl_1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_ = json.NewEncoder(w).Encode(FileInfo{Collection: "demo", Path: "big.bin", Size: int64(len(gotBody))})
	}))
	defer srv.Close()

	// A client configured with a token must NOT attach it to the upload
	// URL request: the capability is the unguessable URL itself (design/07 §3).
	c := New(Config{BaseURL: "https://unused.example.com", Token: "tok_test", HTTP: srv.Client()})
	info, err := c.PutUploadURL(context.Background(), srv.URL+"/api/v1/uploads/upl_1", strings.NewReader("hello"), "text/plain")
	if err != nil {
		t.Fatalf("put upload url: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header = %q, want none", gotAuth)
	}
	if gotContentType != "text/plain" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotBody != "hello" {
		t.Errorf("body = %q", gotBody)
	}
	if info.Path != "big.bin" {
		t.Errorf("info = %+v", info)
	}
}
