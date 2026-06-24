package config

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// defaultMCPListen differs from defaultListen so the adapter and the main
// server don't collide when run as two processes on the same host.
const defaultMCPListen = ":8081"

// MCP configures the MCP adapter (`artifactsd mcp`, design/11 §1). Unlike the
// main server Config, it carries no storage/TLS/admin settings: the adapter is
// a thin HTTP-to-HTTP bridge with its own listen address, calling the
// artifactsd management API as a client — same shape as the CLI's
// --base-url/--token (design/01 §4).
type MCP struct {
	Listen   string
	BaseURL  string
	Token    string
	LogLevel string
}

// LoadMCP resolves the MCP adapter's flags/env, flags > env > defaults, with
// its own minimal flag set (not the full server Config — the adapter never
// touches local storage, TLS, or admin/OIDC settings).
func LoadMCP(args []string, getenv Getenv, out io.Writer) (*MCP, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	fs := flag.NewFlagSet("artifactsd mcp", flag.ContinueOnError)
	fs.SetOutput(out)

	m := &MCP{}
	fs.StringVar(&m.Listen, "listen", envStr(getenv, "ARTIFACTSD_MCP_LISTEN", defaultMCPListen), "MCP HTTP listen address")
	fs.StringVar(&m.BaseURL, "base-url", envStr(getenv, "ARTIFACTSD_BASE_URL", ""), "artifactsd API base URL to call")
	fs.StringVar(&m.Token, "token", envStr(getenv, "ARTIFACTSD_MCP_TOKEN", ""), "management API token the adapter authenticates with")
	fs.StringVar(&m.LogLevel, "log-level", envStr(getenv, "ARTIFACTSD_LOG_LEVEL", defaultLogLevel), "log level (debug|info|warn|error)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return m, nil
}

// Validate fails fast, listing every problem found (mirrors Config.Validate).
func (m *MCP) Validate() error {
	var probs []string
	add := func(format string, args ...any) {
		probs = append(probs, fmt.Sprintf(format, args...))
	}

	if m.Listen == "" {
		add("listen address must not be empty")
	}
	if m.BaseURL == "" {
		add("base-url is required (ARTIFACTSD_BASE_URL / --base-url)")
	} else if u, err := url.Parse(m.BaseURL); err != nil {
		add("base-url %q is not a valid URL: %v", m.BaseURL, err)
	} else if !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		add("base-url %q must be an absolute http(s) URL", m.BaseURL)
	}
	if m.Token == "" {
		add("token is required (ARTIFACTSD_MCP_TOKEN / --token)")
	}
	if !validLogLevel(m.LogLevel) {
		add("log-level %q must be one of debug, info, warn, error", m.LogLevel)
	}

	if len(probs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid mcp configuration:\n  - %s", strings.Join(probs, "\n  - "))
}

// LoadAndValidateMCP loads the MCP adapter's configuration and validates it.
func LoadAndValidateMCP(args []string, getenv Getenv, out io.Writer) (*MCP, error) {
	m, err := LoadMCP(args, getenv, out)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}
