package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// fakeCleaner is a Cleaner test double, configurable to succeed or fail.
type fakeCleaner struct {
	result any
	err    error
	calls  int
}

func (f *fakeCleaner) RunOnce(context.Context) (any, error) {
	f.calls++
	return f.result, f.err
}

type harness struct {
	h       http.Handler
	sess    *session.Manager
	cleaner *fakeCleaner
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)

	cfg, err := config.LoadAndValidate(
		[]string{"--base-url", "https://x.example.com", "--data", dir},
		func(string) string { return "" }, io.Discard,
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	key, err := session.GenerateKey()
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	sess := session.NewManager(key, "", true)
	cleaner := &fakeCleaner{}

	h := New(Deps{
		Store:   st,
		Config:  cfg,
		Audit:   audit.NewRecorder(st.AuditSink(), nil),
		Cleaner: cleaner,
		Session: sess,
		Logger:  audit.NewAppLogger(io.Discard, "error"),
	})
	return &harness{h: h.Routes(), sess: sess, cleaner: cleaner}
}

// withIdentity simulates what adminGate does in the real deployment: stash
// the authenticated identity in the request context before the admin
// package's own handlers run.
func withIdentity(r *http.Request, id *session.Identity) *http.Request {
	return r.WithContext(session.NewContext(r.Context(), id))
}

func TestDashboard_RendersStats(t *testing.T) {
	ha := newHarness(t)
	id := &session.Identity{Subject: "u1", Email: "admin@example.com", Expiry: time.Now().Add(time.Hour)}
	r := withIdentity(httptest.NewRequest(http.MethodGet, "/", http.NoBody), id)
	rec := httptest.NewRecorder()
	ha.h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard = %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Collections") || !strings.Contains(body, "Storage used") {
		t.Errorf("dashboard body missing expected stat labels: %s", body)
	}
	if !strings.Contains(body, "admin@example.com") {
		t.Errorf("dashboard body missing identity email: %s", body)
	}
}

func TestMaintenanceCleanup_Success(t *testing.T) {
	ha := newHarness(t)
	ha.cleaner.result = &struct {
		NodesDeleted          int
		CollectionsDeleted    int
		SharesDeleted         int
		UploadSessionsExpired int
		BlobsDeleted          int
		Errors                []string
	}{NodesDeleted: 3, BlobsDeleted: 2}

	id := &session.Identity{Subject: "u1", Email: "admin@example.com", Expiry: time.Now().Add(time.Hour)}
	r := withIdentity(httptest.NewRequest(http.MethodPost, "/maintenance/cleanup", http.NoBody), id)
	rec := httptest.NewRecorder()
	ha.h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup = %d, body %s", rec.Code, rec.Body.String())
	}
	if ha.cleaner.calls != 1 {
		t.Errorf("RunOnce calls = %d, want 1", ha.cleaner.calls)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "nodes deleted: 3") || !strings.Contains(body, "blobs deleted: 2") {
		t.Errorf("cleanup result body = %s", body)
	}
}

func TestMaintenanceCleanup_NoCleanerConfigured(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)
	cfg, err := config.LoadAndValidate(
		[]string{"--base-url", "https://x.example.com", "--data", dir},
		func(string) string { return "" }, io.Discard,
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	key, err := session.GenerateKey()
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	sess := session.NewManager(key, "", true)

	h := New(Deps{Store: st, Config: cfg, Session: sess, Logger: audit.NewAppLogger(io.Discard, "error")})

	r := httptest.NewRequest(http.MethodPost, "/maintenance/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup without cleaner = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cleanup failed") {
		t.Errorf("expected an error fragment, got %s", rec.Body.String())
	}
}
