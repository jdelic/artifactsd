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

// TestRun_HelpDocumentsConceptsAndEnv pins where the shared concepts are
// documented: the root help stays a short orientation naming the environment
// variables (so the token can be supplied without --token), and each concept
// lives on the command a user reaches for it from.
func TestRun_HelpDocumentsConceptsAndEnv(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
		not  []string
	}{
		{
			args: []string{"--help"},
			want: []string{envBaseURL, envToken, "artifactctl help config", "artifactctl token --help"},
			not:  []string{"Visibility", "--crawlable", "Shares"},
		},
		{args: []string{"help", "config"}, want: []string{envBaseURL, envToken, envConfig, "current:", "--profile"}},
		{args: []string{"token", "--help"}, want: []string{"ARTIFACTSD_BOOTSTRAP_TOKEN", "not the admin UI's login"}},
		{
			args: []string{"meta", "--help"},
			want: []string{"public", "unlisted", "protected", "private", "--bearer-token", "--basic", "--crawlable", "--index", "meta explain"},
		},
		{args: []string{"share", "create", "--help"}, want: []string{"capability link", "artifactctl meta --help"}},
		{args: []string{"collection", "create", "--help"}, want: []string{"artifactctl meta --help"}},
		{args: []string{"put", "--help"}, want: []string{"artifactctl meta --help"}},
		{args: []string{"put-tree", "--help"}, want: []string{"artifactctl meta --help"}},
		{args: []string{"meta", "set", "--help"}, want: []string{"artifactctl meta --help"}},
	} {
		code, out, _ := runCLI(t, tc.args...)
		if code != exitOK {
			t.Fatalf("%v exit = %d", tc.args, code)
		}
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Errorf("%v output missing %q", tc.args, want)
			}
		}
		for _, not := range tc.not {
			if strings.Contains(out, not) {
				t.Errorf("%v output unexpectedly contains %q", tc.args, not)
			}
		}
	}
}
