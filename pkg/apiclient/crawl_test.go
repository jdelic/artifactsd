package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_Crawl(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/collections/demo/crawl" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("path") != "docs" || r.URL.Query().Get("depth") != "2" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(CrawlResponse{
			Collection: "demo", BaseURL: "https://x.example.com/c/demo/",
			Crawlable: true, Indexable: false,
			Entries: []CrawlEntry{
				{Path: "docs/", Type: "folder", Crawlable: true},
				{Path: "docs/readme.txt", Type: "file", Crawlable: true},
			},
		})
	})
	resp, err := c.Crawl(context.Background(), "demo", "docs", 2)
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}
	if resp.Collection != "demo" || !resp.Crawlable || resp.Indexable {
		t.Errorf("resp = %+v", resp)
	}
	if len(resp.Entries) != 2 || resp.Entries[1].Path != "docs/readme.txt" {
		t.Errorf("entries = %+v", resp.Entries)
	}
}

func TestClient_Crawl_NoFilters(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want empty", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(CrawlResponse{})
	})
	if _, err := c.Crawl(context.Background(), "demo", "", 0); err != nil {
		t.Fatalf("crawl: %v", err)
	}
}
