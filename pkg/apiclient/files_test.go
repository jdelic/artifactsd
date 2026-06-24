package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClient_PutFile(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/api/v1/collections/demo/files/docs/readme.txt" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "text/plain" {
			t.Errorf("content-type = %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(FileInfo{Collection: "demo", Path: "docs/readme.txt", Size: int64(len(body))})
	})

	info, err := c.PutFile(context.Background(), "demo", "docs/readme.txt",
		strings.NewReader("hello"), PutFileOptions{ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if info.Size != 5 || info.Path != "docs/readme.txt" {
		t.Errorf("file info = %+v", info)
	}
}

func TestClient_PutFile_MetadataHeader(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(metadataHeader) == "" {
			t.Error("missing metadata header")
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(FileInfo{Path: "a.txt"})
	})
	_, err := c.PutFile(context.Background(), "demo", "a.txt", strings.NewReader("x"),
		PutFileOptions{Metadata: json.RawMessage(`{"access":{"visibility":"public"}}`)})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
}

func TestClient_GetFile(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("file-bytes"))
	})
	rc, err := c.GetFile(context.Background(), "demo", "a.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, _ := io.ReadAll(rc)
	if string(body) != "file-bytes" {
		t.Errorf("body = %q", body)
	}
}

func TestClient_GetFile_NotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"nope"}}`))
	})
	if _, err := c.GetFile(context.Background(), "demo", "a.txt"); !IsNotFound(err) {
		t.Errorf("IsNotFound = false for %v", err)
	}
}

func TestEscapeSegments(t *testing.T) {
	if got := escapeSegments("a/b c/d"); got != "a/b%20c/d" {
		t.Errorf("escapeSegments = %q", got)
	}
}
