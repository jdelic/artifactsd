package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/auth"
)

// execute runs the root command with args and captured stdin/stdout/stderr.
func execute(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestVersionCommand(t *testing.T) {
	out, _, err := execute(t, "", "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, "artifactsd") {
		t.Errorf("version output missing program name: %q", out)
	}
}

func TestHashPassword_FromArg(t *testing.T) {
	out, _, err := execute(t, "", "hash-password", "s3cret")
	if err != nil {
		t.Fatalf("hash-password: %v", err)
	}
	hash := strings.TrimSpace(out)
	ok, verr := auth.VerifyPassword("s3cret", hash)
	if verr != nil || !ok {
		t.Errorf("emitted hash does not verify: ok=%v err=%v hash=%q", ok, verr, hash)
	}
}

func TestHashPassword_FromStdin(t *testing.T) {
	out, _, err := execute(t, "fromstdin\n", "hash-password")
	if err != nil {
		t.Fatalf("hash-password: %v", err)
	}
	ok, _ := auth.VerifyPassword("fromstdin", strings.TrimSpace(out))
	if !ok {
		t.Error("stdin password hash did not verify")
	}
}

func TestHashPassword_Empty(t *testing.T) {
	if _, _, err := execute(t, "\n", "hash-password"); err == nil {
		t.Error("expected error for empty password")
	}
}

func TestServe_MissingBaseURLFails(t *testing.T) {
	_, _, err := execute(t, "", "serve", "--data", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "base-url") {
		t.Fatalf("expected base-url validation error, got %v", err)
	}
}

func TestServe_ValidatesAndReturns(t *testing.T) {
	_, stderr, err := execute(t, "", "serve",
		"--base-url", "https://x.example.com", "--data", t.TempDir())
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.Contains(stderr, "configuration validated") {
		t.Errorf("expected validation log, got %q", stderr)
	}
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	_, stderr, err := execute(t, "", "migrate",
		"--base-url", "https://x.example.com", "--data", dir)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !strings.Contains(stderr, "migrations applied") {
		t.Errorf("expected applied log, got %q", stderr)
	}
	// Re-running must not error.
	if _, _, err := execute(t, "", "migrate",
		"--base-url", "https://x.example.com", "--data", dir); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if _, _, err := execute(t, "", "frobnicate"); err == nil {
		t.Error("expected error for unknown command")
	}
}
