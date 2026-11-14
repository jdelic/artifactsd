package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// TestE2E_TokenGrants: restricted tokens and their grants from the CLI
// (artifactsd-m9e.29, design/16 §7).
func TestE2E_TokenGrants(t *testing.T) {
	e := newE2E(t)
	for _, c := range []string{"reports", "other"} {
		if code, _, errOut := e.run("collection", "create", c); code != exitOK {
			t.Fatalf("create %s: %s", c, errOut)
		}
	}

	code, out, errOut := e.run("--output", "json", "token", "create", "agent", "--restricted",
		"--grant", "reports=collections:read,collections:write")
	if code != exitOK {
		t.Fatalf("token create: %s", errOut)
	}
	var tok apiclient.CreateTokenResponse
	if err := json.Unmarshal([]byte(out), &tok); err != nil {
		t.Fatal(err)
	}
	if !tok.Restricted || len(tok.Grants) != 1 || tok.Grants[0].Collection != "reports" || len(tok.Grants[0].Scopes) != 2 {
		t.Fatalf("created = %+v", tok)
	}
	id := strconv.FormatInt(tok.ID, 10)

	as := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), append([]string{"--base-url", e.ts.BaseURL, "--token", tok.Secret}, args...),
			&stdout, &stderr, func(string) string { return "" })
		return code, stdout.String(), stderr.String()
	}
	if _, list, _ := as("collection", "list"); !strings.Contains(list, "reports") || strings.Contains(list, "other") {
		t.Errorf("restricted collection list = %q, want only reports", list)
	}
	if code, _, _ := as("collection", "show", "other"); code == exitOK {
		t.Error("restricted token reached an ungranted collection")
	}

	if code, _, errOut := e.run("token", "grant", id, "other", "--scope", "collections:read"); code != exitOK {
		t.Fatalf("token grant: %s", errOut)
	}
	if code, _, errOut := as("collection", "show", "other"); code != exitOK {
		t.Errorf("after grant: %s", errOut)
	}
	_, list, _ := e.run("token", "list")
	if !strings.Contains(list, "restricted: other=collections:read; reports=") {
		t.Errorf("token list = %q, want the grants", list)
	}
	if code, _, errOut := e.run("token", "ungrant", id, "other"); code != exitOK {
		t.Fatalf("token ungrant: %s", errOut)
	}
	if code, _, _ := as("collection", "show", "other"); code == exitOK {
		t.Error("ungrant did not take effect")
	}

	for _, bad := range [][]string{
		{"token", "create", "x", "--restricted", "--scope", "admin"},
		{"token", "create", "x", "--grant", "reports=collections:read"},
		{"token", "create", "x", "--restricted", "--grant", "reports"},
		{"token", "grant", id, "other"},
		{"token", "grant", "nope", "other", "--scope", "collections:read"},
	} {
		if code, _, _ := e.run(bad...); code != exitUsage {
			t.Errorf("%v = %d, want usage error", bad, code)
		}
	}
}

// TestE2E_TokenRestrict: `token restrict` turns an existing token restricted
// (artifactsd-m9e.31).
func TestE2E_TokenRestrict(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "reports"); code != exitOK {
		t.Fatalf("create: %s", errOut)
	}
	code, out, errOut := e.run("--output", "json", "token", "create", "ci", "--scope", "collections:read")
	if code != exitOK {
		t.Fatalf("token create: %s", errOut)
	}
	var tok apiclient.CreateTokenResponse
	if err := json.Unmarshal([]byte(out), &tok); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(tok.ID, 10)
	code, out, errOut = e.run("token", "restrict", id)
	if code != exitOK || !strings.Contains(out, "restricted: no grants") {
		t.Fatalf("token restrict = %d %q %q", code, out, errOut)
	}
	if code, _, _ := e.run("token", "restrict", id); code == exitOK {
		t.Error("restricting twice succeeded")
	}
	if code, _, errOut := e.run("token", "grant", id, "reports", "--scope", "collections:read"); code != exitOK {
		t.Errorf("grant after restrict: %s", errOut)
	}
	if code, _, _ := e.run("token", "restrict", "nope"); code != exitUsage {
		t.Errorf("restrict nope = %d, want usage error", code)
	}
}
