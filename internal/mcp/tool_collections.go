package mcp

import (
	"context"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// CreateCollectionIn is the input for artifact_create_collection.
type CreateCollectionIn struct {
	Name             string `json:"name" jsonschema:"Collection name"`
	Visibility       string `json:"visibility,omitempty" jsonschema:"public, unlisted, protected, or private; the server default if omitted"`
	DirectoryListing *bool  `json:"directory_listing,omitempty" jsonschema:"Enable a browsable directory listing"`
	Crawlable        *bool  `json:"crawlable,omitempty" jsonschema:"Allow authenticated agent crawl APIs to enumerate this collection"`
	Indexable        *bool  `json:"indexable,omitempty" jsonschema:"Allow public search-engine indexing"`
}

func (s *Server) createCollection(ctx context.Context, _ *gosdk.CallToolRequest, in *CreateCollectionIn) (*gosdk.CallToolResult, CollectionOut, error) {
	meta, err := buildCollectionMetadata(in.Visibility, in.DirectoryListing, in.Crawlable, in.Indexable)
	if err != nil {
		return nil, CollectionOut{}, err
	}
	col, err := s.Client.CreateCollection(ctx, apiclient.CreateCollectionRequest{Name: in.Name, Metadata: meta})
	if err != nil {
		return nil, CollectionOut{}, MapError(err)
	}
	out, err := toCollectionOut(col)
	if err != nil {
		return nil, CollectionOut{}, err
	}
	return nil, out, nil
}
