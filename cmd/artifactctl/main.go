// Command artifactctl is the command-line client for an artifactsd server. It
// talks to the management API via pkg/apiclient.
//
// See .agent-docs/design/10-cli.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "artifactctl:", err)
		os.Exit(1)
	}
}

// run is the testable entrypoint; the cobra command tree is wired up in the CLI
// epic (artifactsd-crx).
func run(_ []string) error {
	return nil
}
