package mcp

import (
	"context"
	"net/http/httptest"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// TestServer_Handler_InitializeAndListTools exercises the Streamable HTTP
// transport end-to-end (design/11 §1): a real MCP client connects, completes
// the initialize handshake, and lists tools. No tools are registered yet —
// that's artifactsd-0gt — so the list is expected to be empty; this test
// covers the transport/wiring this ticket (artifactsd-22p) is responsible
// for, not any tool behavior.
func TestServer_Handler_InitializeAndListTools(t *testing.T) {
	s := New(Config{Client: apiclient.New(apiclient.Config{BaseURL: "https://x.example.com", Token: "tok_test"})})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	client := gosdk.NewClient(&gosdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(context.Background(), &gosdk.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 0 {
		t.Errorf("tools = %+v, want none registered yet", tools.Tools)
	}
}

func TestNew_CarriesClient(t *testing.T) {
	c := apiclient.New(apiclient.Config{BaseURL: "https://x.example.com", Token: "tok_test"})
	s := New(Config{Client: c})
	if s.Client != c {
		t.Error("New did not carry the configured apiclient.Client through to Server.Client")
	}
	if s.MCP == nil {
		t.Error("New did not build an underlying MCP server")
	}
}
