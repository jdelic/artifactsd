package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/internal/durations"
	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// CreateShareIn is the input for artifact_create_share.
type CreateShareIn struct {
	Collection  string `json:"collection" jsonschema:"Collection name"`
	Path        string `json:"path,omitempty" jsonschema:"Folder or file path to share; the whole collection if omitted"`
	Visibility  string `json:"visibility,omitempty" jsonschema:"unlisted or protected; omit to keep the shared object's own visibility"`
	BearerToken bool   `json:"bearer_token,omitempty" jsonschema:"Require a bearer token; a random one is generated and returned once"`
	ExpiresIn   string `json:"expires_in,omitempty" jsonschema:"Duration like 24h, 7d, or 2w; the share never expires if omitted"`
}

// CreateShareOut is the output of artifact_create_share.
type CreateShareOut struct {
	ShareID              string  `json:"share_id"`
	URL                  string  `json:"url"`
	ExpiresAt            *string `json:"expires_at,omitempty"`
	GeneratedBearerToken string  `json:"generated_bearer_token,omitempty"`
}

// createShare maps to POST .../shares (design/11 §2). Auth/expiry are
// expressed as a metadata document, mirroring cmd/artifactctl's
// buildShareMetadata (cmd/artifactctl/share.go).
func (s *Server) createShare(ctx context.Context, _ *gosdk.CallToolRequest, in *CreateShareIn) (*gosdk.CallToolResult, CreateShareOut, error) {
	if in.Visibility != "" && in.Visibility != "unlisted" && in.Visibility != "protected" {
		return nil, CreateShareOut{}, fmt.Errorf("invalid visibility %q: want unlisted or protected", in.Visibility)
	}

	access := map[string]any{}
	if in.Visibility != "" {
		access["visibility"] = in.Visibility
	}
	var genToken string
	if in.BearerToken {
		var err error
		genToken, err = generateShareToken()
		if err != nil {
			return nil, CreateShareOut{}, fmt.Errorf("generating share token: %w", err)
		}
		access["auth"] = []map[string]any{{"type": "bearer", "token": genToken}}
	}

	doc := map[string]any{}
	if len(access) > 0 {
		doc["access"] = access
	}
	if in.ExpiresIn != "" {
		d, err := durations.Parse(in.ExpiresIn)
		if err != nil {
			return nil, CreateShareOut{}, fmt.Errorf("invalid expires_in %q: %w", in.ExpiresIn, err)
		}
		doc["expiry"] = map[string]any{"expires_at": time.Now().UTC().Add(d).Format(time.RFC3339)}
	}

	var metaJSON json.RawMessage
	if len(doc) > 0 {
		var err error
		metaJSON, err = json.Marshal(doc)
		if err != nil {
			return nil, CreateShareOut{}, err
		}
	}

	sh, err := s.Client.CreateShare(ctx, in.Collection, apiclient.CreateShareRequest{TargetPath: in.Path, Metadata: metaJSON})
	if err != nil {
		return nil, CreateShareOut{}, MapError(err)
	}
	return nil, CreateShareOut{
		ShareID: sh.ShareID, URL: sh.URL, ExpiresAt: sh.ExpiresAt, GeneratedBearerToken: genToken,
	}, nil
}

// generateShareToken returns a random hex bearer token, mirroring
// cmd/artifactctl's --bearer-token auto-generation (cmd/artifactctl/share.go).
func generateShareToken() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
