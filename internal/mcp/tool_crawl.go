package mcp

import (
	"context"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// GetCrawlTreeIn is the input for artifact_get_crawl_tree.
type GetCrawlTreeIn struct {
	Collection string `json:"collection" jsonschema:"Collection name"`
	Path       string `json:"path,omitempty" jsonschema:"Scope the crawl to this subtree; the collection root if omitted"`
	Depth      int    `json:"depth,omitempty" jsonschema:"Levels below path to return; omit or 0 for the full tree"`
}

func (s *Server) getCrawlTree(ctx context.Context, _ *gosdk.CallToolRequest, in *GetCrawlTreeIn) (*gosdk.CallToolResult, apiclient.CrawlResponse, error) {
	resp, err := s.Client.Crawl(ctx, in.Collection, in.Path, in.Depth)
	if err != nil {
		return nil, apiclient.CrawlResponse{}, MapError(err)
	}
	return nil, *resp, nil
}
