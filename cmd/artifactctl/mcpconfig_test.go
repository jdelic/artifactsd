package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runCtl(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, &out, &errOut, func(string) string { return "" })
	return code, out.String(), errOut.String()
}

// TestMCPConfig: every client snippet reads the token from ARTIFACTSD_TOKEN and
// never contains a token value (artifactsd-m9e.18).
func TestMCPConfig(t *testing.T) {
	want := map[string][]string{
		"claude": {"claude mcp add --transport http artifactsd http://127.0.0.1:8081/", `"Authorization: Bearer $ARTIFACTSD_TOKEN"`, `"Bearer ${ARTIFACTSD_TOKEN}"`},
		"codex":  {"[mcp_servers.artifactsd]", `url = "http://127.0.0.1:8081/"`, `bearer_token_env_var = "ARTIFACTSD_TOKEN"`},
		"hermes": {"mcp_servers:", "url: http://127.0.0.1:8081/", `Authorization: "Bearer ${ARTIFACTSD_TOKEN}"`, "~/.hermes/.env"},
	}
	for client, parts := range want {
		code, out, errOut := runCtl("mcp", "config", "--client", client, "--url", "http://127.0.0.1:8081/")
		if code != exitOK {
			t.Fatalf("%s: code %d: %s", client, code, errOut)
		}
		for _, p := range parts {
			if !strings.Contains(out, p) {
				t.Errorf("%s snippet lacks %q:\n%s", client, p, out)
			}
		}
		if strings.Contains(out, "tok_") {
			t.Errorf("%s snippet contains a token value:\n%s", client, out)
		}
	}
	if code, _, _ := runCtl("mcp", "config", "--client", "nope", "--url", "http://x/"); code == exitOK {
		t.Error("unknown client accepted")
	}
	if code, _, _ := runCtl("mcp", "config", "--client", "codex", "--url", "not a url"); code == exitOK {
		t.Error("bad url accepted")
	}
	_, help, _ := runCtl("mcp", "config", "--help")
	if !strings.Contains(help, "ARTIFACTSD_TOKEN") || !strings.Contains(help, "stores no credentials") {
		t.Errorf("help text: %s", help)
	}
}
