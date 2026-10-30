package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// runCLI executes the command tree with a hermetic environment (an empty temp
// HOME so no real user config leaks in) and returns the exit code and output.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := func(k string) string {
		if k == "HOME" {
			return t.TempDir()
		}
		return ""
	}
	code := run(context.Background(), args, &out, &errOut, env)
	return code, out.String(), errOut.String()
}

func TestRun_Version(t *testing.T) {
	code, out, _ := runCLI(t, "version")
	if code != exitOK {
		t.Fatalf("version exit = %d", code)
	}
	if !strings.Contains(out, "artifactctl") {
		t.Errorf("version output = %q", out)
	}
}

func TestRun_InvalidOutput(t *testing.T) {
	code, _, errOut := runCLI(t, "--output", "xml", "collection", "list")
	if code != exitUsage {
		t.Fatalf("invalid output exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errOut, "invalid --output") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestRun_NoServerConfigured(t *testing.T) {
	code, _, errOut := runCLI(t, "collection", "list")
	if code != exitUsage {
		t.Fatalf("no-server exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errOut, "no server configured") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestRun_MissingArg(t *testing.T) {
	code, _, _ := runCLI(t, "--base-url", "https://x.example", "--token", "tok_x", "collection", "show")
	if code != exitUsage {
		t.Fatalf("missing arg exit = %d, want %d", code, exitUsage)
	}
}

func TestRun_UnknownFlag(t *testing.T) {
	code, _, _ := runCLI(t, "--nope")
	if code != exitUsage {
		t.Fatalf("unknown flag exit = %d, want %d", code, exitUsage)
	}
}

// TestRun_HelpDocumentsConceptsAndEnv pins that --help names the environment
// variables the client reads (so the token can be supplied without --token)
// and explains the visibility levels and the flags that set access.
func TestRun_HelpDocumentsConceptsAndEnv(t *testing.T) {
	code, out, _ := runCLI(t, "--help")
	if code != exitOK {
		t.Fatalf("--help exit = %d", code)
	}
	for _, want := range []string{
		envBaseURL, envToken, envConfig,
		"public", "unlisted", "protected", "private", "--bearer-token", "--crawlable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output missing %q", want)
		}
	}
}
