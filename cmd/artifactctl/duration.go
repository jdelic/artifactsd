package main

import (
	"time"

	"github.com/jdelic/artifactsd/internal/durations"
)

// expiresAtFrom resolves the --expires/--expires-in pair into an RFC3339
// timestamp string, or "" when neither is set.
func expiresAtFrom(expires, expiresIn string) (string, error) {
	if expires != "" && expiresIn != "" {
		return "", usagef("--expires and --expires-in are mutually exclusive")
	}
	if expires != "" {
		return expires, nil
	}
	if expiresIn != "" {
		d, err := durations.Parse(expiresIn)
		if err != nil {
			return "", usagef("invalid --expires-in %q: %v", expiresIn, err)
		}
		return time.Now().UTC().Add(d).Format(time.RFC3339), nil
	}
	return "", nil
}
