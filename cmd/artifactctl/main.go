// Command artifactctl is the command-line client for an artifactsd server. It
// talks to the management API via pkg/apiclient.
//
// See .agent-docs/design/10-cli.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// Exit codes (design/10 §3).
const (
	exitOK       = 0
	exitError    = 1
	exitUsage    = 2
	exitAuth     = 4
	exitNotFound = 5
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run is the testable entrypoint: it builds the command tree, executes it, and
// maps the resulting error to a process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, env func(string) string) int {
	a := &app{env: env, out: stdout, err: stderr, output: outputText}
	root := newRootCmd(a)
	root.SetArgs(args)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	_, _ = fmt.Fprintln(stderr, "artifactctl:", err)
	return exitCode(err)
}

// exitCode maps an error to a CLI exit code (design/10 §3).
func exitCode(err error) int {
	var usageErr *usageError
	switch {
	case errors.As(err, &usageErr):
		return exitUsage
	case apiclient.IsUnauthorized(err):
		return exitAuth
	case apiclient.IsNotFound(err):
		return exitNotFound
	default:
		return exitError
	}
}
