package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// mcpTokenEnv is the environment variable every client snippet reads the
// artifactsd API token from (artifactsd-m9e.18).
const mcpTokenEnv = "ARTIFACTSD_TOKEN"

// mcpSnippets render a ready-to-paste MCP client configuration for an adapter
// URL. They reference $ARTIFACTSD_TOKEN and never embed a token value.
var mcpSnippets = map[string]func(adapterURL string) string{
	"claude": func(u string) string {
		return "# Claude Code. Run in a shell where " + mcpTokenEnv + " is set; the header is\n" +
			"# stored with the server entry:\n" +
			"claude mcp add --transport http artifactsd " + u + " \\\n" +
			"  --header \"Authorization: Bearer $" + mcpTokenEnv + "\"\n\n" +
			"# Or in a project's .mcp.json (Claude Code expands ${VAR} in headers at\n" +
			"# startup, so the token stays in your environment):\n" +
			"{\n" +
			"  \"mcpServers\": {\n" +
			"    \"artifactsd\": {\n" +
			"      \"type\": \"http\",\n" +
			"      \"url\": \"" + u + "\",\n" +
			"      \"headers\": { \"Authorization\": \"Bearer ${" + mcpTokenEnv + "}\" }\n" +
			"    }\n" +
			"  }\n" +
			"}\n"
	},
	"codex": func(u string) string {
		return "# Codex CLI: add to ~/.codex/config.toml (Codex reads the token from the\n" +
			"# named environment variable when it starts):\n" +
			"[mcp_servers.artifactsd]\n" +
			"url = \"" + u + "\"\n" +
			"bearer_token_env_var = \"" + mcpTokenEnv + "\"\n\n" +
			"# Or: codex mcp add artifactsd --url " + u + " --bearer-token-env-var " + mcpTokenEnv + "\n"
	},
	"hermes": func(u string) string {
		return "# Hermes Agent: add to ~/.hermes/config.yaml, and put\n" +
			"#   " + mcpTokenEnv + "=<your token>\n" +
			"# in ~/.hermes/.env (Hermes expands ${VAR} from it):\n" +
			"mcp_servers:\n" +
			"  artifactsd:\n" +
			"    url: " + u + "\n" +
			"    headers:\n" +
			"      Authorization: \"Bearer ${" + mcpTokenEnv + "}\"\n"
	},
}

func mcpClientNames() []string {
	names := make([]string, 0, len(mcpSnippets))
	for n := range mcpSnippets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (a *app) newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Help connect MCP clients to an artifactsd MCP adapter",
	}
	cmd.AddCommand(a.newMCPConfigCmd())
	return cmd
}

func (a *app) newMCPConfigCmd() *cobra.Command {
	var client, adapterURL string
	cmd := &cobra.Command{
		Use:   "config --client " + strings.Join(mcpClientNames(), "|") + " --url <adapter-url>",
		Short: "Print the MCP client configuration for an artifactsd MCP adapter",
		Long: `Print a ready-to-paste configuration that connects an MCP client to an
artifactsd MCP adapter ("artifactsd mcp").

The adapter stores no credentials: each MCP client sends its own artifactsd API
token as "Authorization: Bearer <token>". The snippets read that token from the
environment variable ` + mcpTokenEnv + ` and never contain the token itself, so the
config can be shared or committed safely. Mint one token per agent with only the
scopes it needs ("artifactctl token create <name> --scope ...") and set
` + mcpTokenEnv + ` where the client runs:

  claude  Claude Code expands ${` + mcpTokenEnv + `} in .mcp.json headers from the
          environment it was started in; "claude mcp add --header" stores the
          header value from your current shell.
  codex   Codex CLI reads the variable named by bearer_token_env_var from the
          environment it was started in.
  hermes  Hermes Agent expands ${` + mcpTokenEnv + `} from ~/.hermes/.env.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			render, ok := mcpSnippets[client]
			if !ok {
				return usagef("--client must be one of: %s", strings.Join(mcpClientNames(), ", "))
			}
			u, err := url.Parse(adapterURL)
			if adapterURL == "" || err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return usagef("--url must be the adapter's absolute http(s) URL, e.g. http://127.0.0.1:8081/")
			}
			_, err = fmt.Fprint(a.out, render(adapterURL))
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&client, "client", "", "MCP client: "+strings.Join(mcpClientNames(), "|"))
	f.StringVar(&adapterURL, "url", "", "the artifactsd MCP adapter URL (e.g. http://127.0.0.1:8081/)")
	return cmd
}
