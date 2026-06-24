package mcp

import (
	"context"
	"encoding/json"
	"strings"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// PutTextIn is the input for artifact_put_text.
type PutTextIn struct {
	Collection  string `json:"collection" jsonschema:"Collection name"`
	Path        string `json:"path" jsonschema:"File path within the collection"`
	Text        string `json:"text" jsonschema:"File content"`
	ContentType string `json:"content_type,omitempty" jsonschema:"MIME type; inferred from the path extension if omitted"`
}

func (s *Server) putText(ctx context.Context, _ *gosdk.CallToolRequest, in *PutTextIn) (*gosdk.CallToolResult, apiclient.FileInfo, error) {
	info, err := s.Client.PutFile(ctx, in.Collection, in.Path, strings.NewReader(in.Text), apiclient.PutFileOptions{ContentType: in.ContentType})
	if err != nil {
		return nil, apiclient.FileInfo{}, MapError(err)
	}
	return nil, *info, nil
}

// GetMetadataIn is the input for artifact_get_metadata.
type GetMetadataIn struct {
	Collection string `json:"collection" jsonschema:"Collection name"`
	Path       string `json:"path" jsonschema:"File or folder path within the collection"`
}

func (s *Server) getMetadata(ctx context.Context, _ *gosdk.CallToolRequest, in *GetMetadataIn) (*gosdk.CallToolResult, FileMetaOut, error) {
	meta, err := s.Client.GetFileMeta(ctx, in.Collection, in.Path)
	if err != nil {
		return nil, FileMetaOut{}, MapError(err)
	}
	out, err := toFileMetaOut(meta)
	if err != nil {
		return nil, FileMetaOut{}, err
	}
	return nil, out, nil
}

// SetMetadataIn is the input for artifact_set_metadata. Metadata is a
// map (not json.RawMessage) so the SDK infers an "object" input schema
// instead of treating the field as an opaque byte array (see metadata.go).
type SetMetadataIn struct {
	Collection string         `json:"collection" jsonschema:"Collection name"`
	Path       string         `json:"path" jsonschema:"File or folder path within the collection"`
	Metadata   map[string]any `json:"metadata" jsonschema:"Metadata document patch (access/crawl/expiry/content sections), merged onto the existing metadata"`
}

func (s *Server) setMetadata(ctx context.Context, _ *gosdk.CallToolRequest, in *SetMetadataIn) (*gosdk.CallToolResult, FileMetaOut, error) {
	patch, err := json.Marshal(in.Metadata)
	if err != nil {
		return nil, FileMetaOut{}, err
	}
	meta, err := s.Client.PatchFileMeta(ctx, in.Collection, in.Path, patch)
	if err != nil {
		return nil, FileMetaOut{}, MapError(err)
	}
	out, err := toFileMetaOut(meta)
	if err != nil {
		return nil, FileMetaOut{}, err
	}
	return nil, out, nil
}

// DeleteIn is the input for artifact_delete.
type DeleteIn struct {
	Collection string `json:"collection" jsonschema:"Collection name"`
	Path       string `json:"path" jsonschema:"File path within the collection"`
}

// DeleteOut confirms a deletion.
type DeleteOut struct {
	Deleted bool `json:"deleted"`
}

func (s *Server) deleteArtifact(ctx context.Context, _ *gosdk.CallToolRequest, in *DeleteIn) (*gosdk.CallToolResult, DeleteOut, error) {
	if err := s.Client.DeleteFile(ctx, in.Collection, in.Path); err != nil {
		return nil, DeleteOut{}, MapError(err)
	}
	return nil, DeleteOut{Deleted: true}, nil
}

// ListTreeIn is the input for artifact_list_tree.
type ListTreeIn struct {
	Collection string `json:"collection" jsonschema:"Collection name"`
	Path       string `json:"path,omitempty" jsonschema:"Scope the listing to this subtree; the collection root if omitted"`
	Depth      int    `json:"depth,omitempty" jsonschema:"Levels below path to return; 0 for the server default (immediate children)"`
}

func (s *Server) listTree(ctx context.Context, _ *gosdk.CallToolRequest, in *ListTreeIn) (*gosdk.CallToolResult, apiclient.Page[apiclient.TreeNode], error) {
	page, err := s.Client.Tree(ctx, in.Collection, in.Path, in.Depth)
	if err != nil {
		return nil, apiclient.Page[apiclient.TreeNode]{}, MapError(err)
	}
	return nil, *page, nil
}

// GetPublicURLIn is the input for artifact_get_public_url.
type GetPublicURLIn struct {
	Collection string `json:"collection" jsonschema:"Collection name"`
	Path       string `json:"path,omitempty" jsonschema:"File path within the collection; the collection root if omitted"`
}

// GetPublicURLOut carries the composed URL.
type GetPublicURLOut struct {
	URL string `json:"url"`
}

func (s *Server) getPublicURL(_ context.Context, _ *gosdk.CallToolRequest, in *GetPublicURLIn) (*gosdk.CallToolResult, GetPublicURLOut, error) {
	return nil, GetPublicURLOut{URL: s.publicURL(in.Collection, in.Path)}, nil
}

// publicURL composes a /c/{collection}/{path} URL under BaseURL (SPEC §5.1)
// purely locally, without a server round trip.
func (s *Server) publicURL(collection, path string) string {
	u := strings.TrimRight(s.BaseURL, "/") + "/c/" + collection + "/"
	if path != "" {
		u += strings.TrimLeft(path, "/")
	}
	return u
}
