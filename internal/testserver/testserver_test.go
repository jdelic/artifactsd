package testserver_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
)

func TestServer_BootsAndServes(t *testing.T) {
	ts := testserver.New(t)

	// Health endpoint is reachable over real HTTP.
	resp, err := ts.Client().Get(ts.BaseURL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d", resp.StatusCode)
	}
}

func TestServer_AdminTokenAuthorizes(t *testing.T) {
	ts := testserver.New(t)

	// The seeded admin token authorizes a management write.
	body := strings.NewReader(`{"name":"demo"}`)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.BaseURL+"/api/v1/collections", body)
	req.Header.Set("Authorization", "Bearer "+ts.AdminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d (%s)", resp.StatusCode, out)
	}

	// Without the token, the same call is rejected.
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.BaseURL+"/api/v1/collections", http.NoBody)
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("unauth list: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauth list status = %d, want 401", resp2.StatusCode)
	}
}
