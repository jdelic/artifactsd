package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL, Token: "tok_test", HTTP: srv.Client()})
}

func TestClient_CreateCollection(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/collections" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok_test" {
			t.Errorf("auth header = %q", got)
		}
		var req CreateCollectionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Collection{Name: req.Name, Visibility: "private"})
	})

	col, err := c.CreateCollection(context.Background(), CreateCollectionRequest{Name: "demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if col.Name != "demo" || col.Visibility != "private" {
		t.Errorf("collection = %+v", col)
	}
}

func TestClient_ListCollections_Query(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit query = %q", r.URL.Query().Get("limit"))
		}
		_ = json.NewEncoder(w).Encode(Page[Collection]{Items: []Collection{{Name: "a"}, {Name: "b"}}})
	})
	page, err := c.ListCollections(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("items = %d, want 2", len(page.Items))
	}
}

func TestClient_ErrorEnvelope(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"collection does not exist"}}`))
	})
	_, err := c.GetCollection(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound = false for %v", err)
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "not_found" {
		t.Errorf("error = %+v", err)
	}
}

func TestClient_Unauthorized(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"authentication required"}}`))
	})
	err := c.DeleteCollection(context.Background(), "x", false)
	if !IsUnauthorized(err) {
		t.Errorf("IsUnauthorized = false for %v", err)
	}
}

func TestClient_DeleteForceQuery(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Query().Get("force") != "true" {
			t.Errorf("force query = %q", r.URL.Query().Get("force"))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.DeleteCollection(context.Background(), "demo", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
