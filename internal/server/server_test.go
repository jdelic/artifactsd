package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/buildinfo"
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
	return newTestServerAt(t, "127.0.0.1:0")
}

// newTestServerAt is newTestServer listening on listen.
func newTestServerAt(t *testing.T, listen string) *Server {
	t.Helper()
	cfg := testConfig(t, listen)
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

// waitHTTP polls url until it answers with an HTTP response (any status when
// wantStatus is 0), instead of sleeping a fixed time and hoping the listener is
// up. It fails the test if done delivers first (the server exited during
// startup) or after 15s.
func waitHTTP(t *testing.T, client *http.Client, url string, wantStatus int, done <-chan error) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("server exited before %s answered: %v", url, err)
		case <-deadline:
			t.Fatalf("%s did not answer within 15s", url)
		case <-time.After(20 * time.Millisecond):
			resp, err := client.Get(url)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if wantStatus == 0 || resp.StatusCode == wantStatus {
				return
			}
		}
	}
}

func TestRun_GracefulShutdown(t *testing.T) {
	addr := freePort(t)
	s := newTestServerAt(t, addr)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Wait until the server actually answers, then signal shutdown.
	waitHTTP(t, &http.Client{Timeout: time.Second}, "http://"+addr+"/healthz", http.StatusOK, done)
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

// /version reports the running build (artifactsd-u2p).
func TestVersion(t *testing.T) {
	oldV, oldC, oldD := buildinfo.Version, buildinfo.Commit, buildinfo.Date
	buildinfo.Version, buildinfo.Commit, buildinfo.Date = "v9.9.9", "abc1234", "2026-11-11T00:00:00Z"
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit, buildinfo.Date = oldV, oldC, oldD })

	s := newTestServer(t)
	rec := do(t, s.routes(), http.MethodGet, "/version")
	if rec.Code != http.StatusOK {
		t.Fatalf("version status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	want := map[string]string{"version": "v9.9.9", "commit": "abc1234", "date": "2026-11-11T00:00:00Z"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
