package config

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
)

// defaultMCPListen differs from defaultListen so the adapter and the main
// server don't collide when run as two processes on the same host. It binds
// loopback: clients send their API tokens to the adapter, which has no TLS of
// its own (artifactsd-m9e.18).
const defaultMCPListen = "127.0.0.1:8081"

// MCPUsage is the head of `artifactsd mcp --help`: how authentication works,
// before the flag list.
const MCPUsage = `Usage: artifactsd mcp [flags]

Runs the MCP adapter (Streamable HTTP) in front of an artifactsd server.

The adapter stores NO credentials. Every MCP client must send its own
artifactsd API token as "Authorization: Bearer <token>"; the adapter forwards
it, so each agent acts with exactly that token's scopes. Mint one token per
agent with only the scopes it needs (artifactctl token create ...), and keep it
in the MCP client's environment as ARTIFACTSD_TOKEN — never paste it into
shared config files. "artifactctl mcp config --client <name>" prints these:

  Claude Code:  claude mcp add --transport http artifactsd <adapter-url> \
                  --header "Authorization: Bearer $ARTIFACTSD_TOKEN"
  Codex CLI:    ~/.codex/config.toml
                  [mcp_servers.artifactsd]
                  url = "<adapter-url>"
                  bearer_token_env_var = "ARTIFACTSD_TOKEN"
  Hermes Agent: ~/.hermes/config.yaml
                  mcp_servers:
                    artifactsd:
                      url: <adapter-url>
                      headers: {Authorization: "Bearer ${ARTIFACTSD_TOKEN}"}
                (with ARTIFACTSD_TOKEN set in ~/.hermes/.env)

The adapter listens on loopback by default. Tokens travel in clear text to it:
if you expose it beyond localhost, terminate TLS in front of it.

Flags:
`

// MCP configures the MCP adapter (`artifactsd mcp`, design/11 §1). Unlike the
// main server Config, it carries no storage/TLS/admin settings: the adapter is
// a thin HTTP-to-HTTP bridge with its own listen address, calling the
// artifactsd management API as a client — same shape as the CLI's
// --base-url/--token (design/01 §4).
type MCP struct {
	Listen  string
	BaseURL string
	// Token is the removed --token / ARTIFACTSD_MCP_TOKEN setting. It is still
	// read so that Validate can refuse to start when it is set: an operator who
	// believes the adapter is scoped by that token must notice the change.
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
	fs.Usage = func() {
		_, _ = io.WriteString(fs.Output(), MCPUsage)
		fs.PrintDefaults()
	}

	m := &MCP{}
	fs.StringVar(&m.Listen, "listen", envStr(getenv, "ARTIFACTSD_MCP_LISTEN", defaultMCPListen), "MCP HTTP listen address")
	fs.StringVar(&m.BaseURL, "base-url", envStr(getenv, "ARTIFACTSD_BASE_URL", ""), "artifactsd API base URL to call")
	fs.StringVar(&m.Token, "token", envStr(getenv, "ARTIFACTSD_MCP_TOKEN", ""), "REMOVED: clients send their own tokens (setting this is an error)")
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
	if m.Token != "" {
		add("--token / ARTIFACTSD_MCP_TOKEN is no longer supported: the adapter holds no credentials and " +
			"every MCP client must send its own API token as \"Authorization: Bearer <token>\". " +
			"Unset it and configure your clients (see `artifactsd mcp --help`, `artifactctl mcp config`, docs/mcp.md)")
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

// ListenIsLoopback reports whether the listen address binds only loopback.
func (m *MCP) ListenIsLoopback() bool {
	host, _, err := net.SplitHostPort(m.Listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
