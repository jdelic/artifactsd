package mcp

import (
	"fmt"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// MapError turns an apiclient error into an actionable message for the agent
// (design/11 §5: "errors map artifactsd API error codes → MCP tool errors with
// actionable messages"). Tool handlers (artifactsd-0gt) call this on every
// apiclient error before returning it, so agents see the HTTP status and the
// server's error code/message rather than a bare Go error string.
//
// The returned error is what a ToolHandlerFor should return as its err value;
// the SDK turns it into an isError CallToolResult.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	apiErr, ok := err.(*apiclient.Error)
	if !ok {
		return fmt.Errorf("artifactsd API request failed: %w", err)
	}
	if apiErr.Message != "" {
		return fmt.Errorf("artifactsd API error: %s (HTTP %d, %s)", apiErr.Message, apiErr.Status, apiErr.Code)
	}
	return fmt.Errorf("artifactsd API error: HTTP %d (%s)", apiErr.Status, apiErr.Code)
}
