// Command artifactsd is the artifactsd server: a single self-contained binary
// that stores artifacts (blobs + metadata) and serves them over HTTP.
//
// See .agent-docs/design/01-architecture.md for the component map and
// design/12 §6 for the subcommand list.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "artifactsd:", err)
		os.Exit(1)
	}
}

// run is the testable entrypoint: it builds the cobra command tree and executes
// it against the given args.
func run(args []string) error {
	cmd := newRootCmd()
	cmd.SetArgs(args)
	return cmd.Execute()
}
