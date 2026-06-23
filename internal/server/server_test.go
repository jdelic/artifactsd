package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/store"
)

func testConfig(t *testing.T, listen string) *config.Config {
	t.Helper()
	cfg, err := config.LoadAndValidate(
		[]string{"--base-url", "https://x.example.com", "--data", t.TempDir(), "--listen", listen},
		func(string) string { return "" }, io.Discard,
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig(t, "127.0.0.1:0")
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.DBFilename))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(Options{
		Config: cfg,
		Logger: audit.NewAppLogger(io.Discard, "error"),
		Access: audit.NewAccessLogger(io.Discard),
		DB:     db,
	})
}

func do(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, http.NoBody))
	return rec
}

func TestHealthz(t *testing.T) {
	s := newTestServer(t)
	rec := do(t, s.routes(), http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok\n" {
		t.Errorf("healthz body = %q", rec.Body.String())
	}
}

func TestReadyz_OK(t *testing.T) {
	s := newTestServer(t)
	rec := do(t, s.routes(), http.MethodGet, "/readyz")
	if rec.Code != http.StatusOK {
		t.Errorf("readyz status = %d, want 200", rec.Code)
	}
}

func TestReadyz_ClosedDB(t *testing.T) {
	s := newTestServer(t)
	_ = s.db.Close() // simulate an unreachable database
	rec := do(t, s.routes(), http.MethodGet, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz with closed DB = %d, want 503", rec.Code)
	}
}

func TestRouteStubs_NotImplemented(t *testing.T) {
	s := newTestServer(t)
	// /admin/ is gated (covered in admin_test); the public stubs are open 501s.
	for _, path := range []string{"/c/demo/x", "/s/abc", "/api/v1/collections"} {
		rec := do(t, s.routes(), http.MethodGet, path)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s status = %d, want 501", path, rec.Code)
		}
	}
}

func TestRoot(t *testing.T) {
	s := newTestServer(t)
	if rec := do(t, s.routes(), http.MethodGet, "/"); rec.Code != http.StatusOK {
		t.Errorf("/ status = %d, want 200", rec.Code)
	}
	if rec := do(t, s.routes(), http.MethodGet, "/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("/nope status = %d, want 404", rec.Code)
	}
}

func TestRun_GracefulShutdown(t *testing.T) {
	s := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Give the listener a moment to come up, then signal shutdown.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error on graceful shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancelation")
	}
}
