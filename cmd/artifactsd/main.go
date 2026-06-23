// Command artifactsd is the artifactsd server: a single self-contained binary
// that stores artifacts (blobs + metadata) and serves them over HTTP.
//
// See .agent-docs/design/01-architecture.md for the component map.
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

// run is the testable entrypoint; the cobra command tree is wired up in a later
// scaffolding ticket (artifactsd-9ff).
func run(_ []string) error {
	return nil
}
