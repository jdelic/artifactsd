package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
)

// MaintenanceResult is the response from MaintenanceCleanup. Cleanup is left
// as raw JSON since its shape is defined by whichever janitor implementation
// the server has configured (design/04 §8, design/08).
type MaintenanceResult struct {
	Cleanup json.RawMessage `json:"cleanup"`
}

// MaintenanceCleanup triggers one on-demand janitor pass (design/04 §8). The
// server returns 503 if no janitor is configured.
func (c *Client) MaintenanceCleanup(ctx context.Context) (*MaintenanceResult, error) {
	var out MaintenanceResult
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/maintenance/cleanup", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
