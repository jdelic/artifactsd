package apiclient

import (
	"context"
	"net/http"
	"testing"
)

func TestClient_MaintenanceCleanup(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/maintenance/cleanup" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"cleanup":{"deleted_files":3}}`))
	})
	result, err := c.MaintenanceCleanup(context.Background())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if string(result.Cleanup) != `{"deleted_files":3}` {
		t.Errorf("cleanup = %s", result.Cleanup)
	}
}

func TestClient_MaintenanceCleanup_Unavailable(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"cleanup is not configured"}}`))
	})
	if _, err := c.MaintenanceCleanup(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
