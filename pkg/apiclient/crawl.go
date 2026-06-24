package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// CrawlEntry is one file/folder in a crawl response (SPEC §10.5).
type CrawlEntry struct {
	Path        string  `json:"path"`
	Type        string  `json:"type"` // "file"|"folder"
	URL         string  `json:"url,omitempty"`
	ContentType string  `json:"content_type,omitempty"`
	Size        *int64  `json:"size,omitempty"`
	UpdatedAt   string  `json:"updated_at,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
	Crawlable   bool    `json:"crawlable"`
	Indexable   bool    `json:"indexable"`
}

// CrawlResponse is the per-collection crawl response (SPEC §10.5).
type CrawlResponse struct {
	Collection string       `json:"collection"`
	BaseURL    string       `json:"base_url"`
	Crawlable  bool         `json:"crawlable"`
	Indexable  bool         `json:"indexable"`
	Entries    []CrawlEntry `json:"entries"`
}

// Crawl fetches a collection's policy-filtered crawl tree (design/04 §7,
// design/05 §5). path scopes the listing to a subtree ("" for the root);
// depth limits how many levels below path are returned (0 for the server
// default of immediate children only, negative for unlimited).
func (c *Client) Crawl(ctx context.Context, collection, path string, depth int) (*CrawlResponse, error) {
	q := url.Values{}
	if path != "" {
		q.Set("path", path)
	}
	if depth != 0 {
		q.Set("depth", strconv.Itoa(depth))
	}
	var out CrawlResponse
	if err := c.sendJSON(ctx, http.MethodGet, "/api/v1/collections/"+url.PathEscape(collection)+"/crawl"+encodeQuery(q), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
