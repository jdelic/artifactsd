package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_CreateShare(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/collections/demo/shares" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req CreateShareRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Share{
			ShareID: "shr_1", Collection: "demo", TargetPath: req.TargetPath,
			URL: "https://example.com/s/shr_1/",
		})
	})
	sh, err := c.CreateShare(context.Background(), "demo", CreateShareRequest{TargetPath: "report.pdf"})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	if sh.ShareID != "shr_1" || sh.TargetPath != "report.pdf" {
		t.Errorf("share = %+v", sh)
	}
}

func TestClient_ListShares(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/collections/demo/shares" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Page[Share]{Items: []Share{{ShareID: "shr_1"}, {ShareID: "shr_2"}}})
	})
	page, err := c.ListShares(context.Background(), "demo")
	if err != nil {
		t.Fatalf("list shares: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("items = %d, want 2", len(page.Items))
	}
}

func TestClient_GetShare(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/shares/shr_1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Share{ShareID: "shr_1"})
	})
	sh, err := c.GetShare(context.Background(), "shr_1")
	if err != nil {
		t.Fatalf("get share: %v", err)
	}
	if sh.ShareID != "shr_1" {
		t.Errorf("share = %+v", sh)
	}
}

func TestClient_PatchShareMeta(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/shares/shr_1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Share{ShareID: "shr_1"})
	})
	sh, err := c.PatchShareMeta(context.Background(), "shr_1", json.RawMessage(`{"expiry":{"expires_at":"2026-07-01T00:00:00Z"}}`))
	if err != nil {
		t.Fatalf("patch share: %v", err)
	}
	if sh.ShareID != "shr_1" {
		t.Errorf("share = %+v", sh)
	}
}

func TestClient_RevokeShare(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/shares/shr_1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.RevokeShare(context.Background(), "shr_1"); err != nil {
		t.Fatalf("revoke share: %v", err)
	}
}
