// Package mcp implements the MCP adapter exposing artifactsd tools over
// Streamable HTTP — the single committed transport (design/11 §1, decisions
// E4/N1). The ten tools (SPEC §16) call the management API through
// pkg/apiclient, the same client the CLI uses; MCP is an adapter, not the
// canonical storage layer.
package mcp

import (
	"net/http"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/internal/buildinfo"
	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// Config configures the adapter.
type Config struct {
	// Client is the configured artifactsd API client (base_url + token) that
	// tool handlers call into (design/11 §1). Tools never talk to artifactsd
	// auth directly; they go through this client, same as the CLI.
	Client *apiclient.Client
	// BaseURL is the artifactsd public base URL, used to compose /c/... and
	// /s/... URLs locally (e.g. artifact_get_public_url) without a round trip.
	BaseURL string
}

// Server wraps the MCP SDK server and the apiclient its tool handlers call.
type Server struct {
	// MCP is the underlying SDK server.
	MCP *gosdk.Server
	// Client is the apiclient tool handlers use to call the management API.
	Client *apiclient.Client
	// BaseURL is the artifactsd public base URL (see Config.BaseURL).
	BaseURL string
}

// New builds the MCP server with the ten SPEC §16 tools registered.
func New(cfg Config) *Server {
	s := &Server{
		MCP: gosdk.NewServer(&gosdk.Implementation{
			Name:    "artifactsd",
			Version: buildinfo.Version,
		}, nil),
		Client:  cfg.Client,
		BaseURL: cfg.BaseURL,
	}
	registerTools(s)
	return s
}

// Handler returns the Streamable HTTP handler serving this MCP server
// (design/11 §1). It is the adapter's single transport: no stdio.
func (s *Server) Handler() http.Handler {
	return gosdk.NewStreamableHTTPHandler(func(*http.Request) *gosdk.Server {
		return s.MCP
	}, nil)
}
