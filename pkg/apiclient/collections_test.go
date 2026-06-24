package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_Tree(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/collections/demo/tree" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("path") != "docs" || r.URL.Query().Get("depth") != "2" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		size := int64(42)
		_ = json.NewEncoder(w).Encode(Page[TreeNode]{Items: []TreeNode{
			{Path: "docs", Kind: "folder"},
			{Path: "docs/readme.txt", Kind: "file", Size: &size},
		}})
	})
	page, err := c.Tree(context.Background(), "demo", "docs", 2)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if len(page.Items) != 2 || page.Items[1].Path != "docs/readme.txt" {
		t.Errorf("items = %+v", page.Items)
	}
}

func TestClient_Tree_NoFilters(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want empty", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(Page[TreeNode]{})
	})
	if _, err := c.Tree(context.Background(), "demo", "", 0); err != nil {
		t.Fatalf("tree: %v", err)
	}
}
