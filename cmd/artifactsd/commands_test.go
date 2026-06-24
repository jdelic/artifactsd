package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/config"
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

func TestRunServe_StartsAndShutsDown(t *testing.T) {
	cfg, err := config.LoadAndValidate(
		[]string{"--base-url", "https://x.example.com", "--data", t.TempDir(), "--listen", "127.0.0.1:0"},
		func(string) string { return "" }, io.Discard,
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, cfg, io.Discard) }()

	time.Sleep(50 * time.Millisecond) // let it bind + auto-migrate
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runServe returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServe did not return after cancellation")
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

func TestMCP_MissingTokenFails(t *testing.T) {
	_, _, err := execute(t, "", "mcp", "--base-url", "https://x.example.com")
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected a token validation error, got %v", err)
	}
}

func TestMCP_MissingBaseURLFails(t *testing.T) {
	_, _, err := execute(t, "", "mcp", "--token", "tok_abc")
	if err == nil || !strings.Contains(err.Error(), "base-url") {
		t.Fatalf("expected a base-url validation error, got %v", err)
	}
}

func TestRunMCP_StartsAndShutsDown(t *testing.T) {
	m, err := config.LoadAndValidateMCP(
		[]string{"--listen", "127.0.0.1:0", "--base-url", "https://x.example.com", "--token", "tok_abc"},
		func(string) string { return "" }, io.Discard,
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runMCP(ctx, m, io.Discard) }()

	time.Sleep(50 * time.Millisecond) // let it bind
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runMCP returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCP did not return after cancellation")
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if _, _, err := execute(t, "", "frobnicate"); err == nil {
		t.Error("expected error for unknown command")
	}
}
