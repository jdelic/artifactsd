package mcp

import (
	"context"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// BeginUploadIn is the input for artifact_begin_upload.
type BeginUploadIn struct {
	Collection string `json:"collection" jsonschema:"Collection name"`
	Path       string `json:"path" jsonschema:"File path the upload will be stored at"`
}

// BeginUpload returns a direct HTTP upload URL; the agent PUTs the file's
// bytes there out of band (SPEC §16 §3), keeping large binaries out of the
// MCP message channel.
func (s *Server) beginUpload(ctx context.Context, _ *gosdk.CallToolRequest, in *BeginUploadIn) (*gosdk.CallToolResult, apiclient.UploadSession, error) {
	session, err := s.Client.BeginUpload(ctx, in.Collection, apiclient.BeginUploadRequest{Path: in.Path})
	if err != nil {
		return nil, apiclient.UploadSession{}, MapError(err)
	}
	return nil, *session, nil
}
