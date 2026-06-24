// Package mcp implements the MCP adapter exposing artifactsd tools over
// Streamable HTTP — the single committed transport (design/11 §1, decisions
// E4/N1). This package owns the SDK wiring, the apiclient dependency, and
// error mapping; the ten tools (SPEC §16) are registered onto Server.MCP via
// the SDK's AddTool by the tools epic (artifactsd-0gt).
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
}

// Server wraps the MCP SDK server and the apiclient it hands to tool
// handlers registered on MCP.
type Server struct {
	// MCP is the underlying SDK server. Tool handlers are added with
	// gosdk.AddTool(s.MCP, ...) (artifactsd-0gt).
	MCP *gosdk.Server
	// Client is the apiclient tool handlers use to call the management API.
	Client *apiclient.Client
}

// New builds the MCP server with no tools registered. Tools are added by
// callers (artifactsd-0gt) via gosdk.AddTool(s.MCP, ...).
func New(cfg Config) *Server {
	return &Server{
		MCP: gosdk.NewServer(&gosdk.Implementation{
			Name:    "artifactsd",
			Version: buildinfo.Version,
		}, nil),
		Client: cfg.Client,
	}
}

// Handler returns the Streamable HTTP handler serving this MCP server
// (design/11 §1). It is the adapter's single transport: no stdio.
func (s *Server) Handler() http.Handler {
	return gosdk.NewStreamableHTTPHandler(func(*http.Request) *gosdk.Server {
		return s.MCP
	}, nil)
}
