// Package buildinfo exposes build-time version metadata, injected via -ldflags
// at release time and falling back to Go module build info otherwise.
package buildinfo

import "runtime/debug"

// These are overridden at build time with -ldflags "-X ...".
var (
	// Version is the release version (e.g. "v1.2.3"); "dev" for local builds.
	Version = "dev"
	// Commit is the git commit hash the binary was built from.
	Commit = "none"
	// Date is the build timestamp (RFC3339).
	Date = "unknown"
)

// ResolvedVersion returns Version, or for a "dev" build the Go module version
// when one is recorded (e.g. after `go install ...@vX`).
func ResolvedVersion() string {
	v := Version
	if v == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	return v
}

// String returns a human-readable one-line version summary.
func String() string {
	return ResolvedVersion() + " (commit " + Commit + ", built " + Date + ")"
}
