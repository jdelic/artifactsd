// Package testserver boots a full artifactsd server in-process for integration
// and end-to-end tests (design/13 §3). Unlike the httptest handler-level unit
// tests in internal/api, it exercises the complete wiring — config, middleware,
// store, and blob storage — over real HTTP on an ephemeral loopback port, and is
// reused by the CLI (and later MCP) e2e tests.
package testserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/server"
	"github.com/jdelic/artifactsd/internal/store"
)

// Server is a running in-process artifactsd instance for tests.
type Server struct {
	// BaseURL is the server's http://127.0.0.1:PORT root; it matches the config
	// base URL, so URLs returned by the API resolve back to this instance.
	BaseURL string
	// AdminToken is a seeded API token with the admin scope.
	AdminToken string
	// Store is the backing store, exposed for seeding and assertions.
	Store *store.Store

	http *httptest.Server
}

// Close stops the server. Registered automatically via t.Cleanup by New.
func (s *Server) Close() { s.http.Close() }

// New boots a server on an ephemeral loopback port with a temp data dir, applies
// migrations, seeds an admin token, and registers teardown on t.Cleanup.
func New(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	// Reserve a loopback port up front so the config base URL matches the URL the
	// server is actually reached on (the API echoes absolute URLs in responses).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testserver: listen: %v", err)
	}
	baseURL := "http://" + ln.Addr().String()

	cfg, err := config.LoadAndValidate(
		[]string{"--base-url", baseURL, "--data", dir, "--listen", ln.Addr().String()},
		func(string) string { return "" }, io.Discard,
	)
	if err != nil {
		_ = ln.Close()
		t.Fatalf("testserver: config: %v", err)
	}

	db, err := store.Open(ctx, filepath.Join(dir, store.DBFilename))
	if err != nil {
		_ = ln.Close()
		t.Fatalf("testserver: open db: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		_ = ln.Close()
		t.Fatalf("testserver: migrate: %v", err)
	}
	st := store.New(db).WithDefaultDeleteAfterCreation(cfg.Policy.DeleteAfterCreation)

	bs, err := blob.NewFSStore(cfg.Server.DataDir)
	if err != nil {
		_ = ln.Close()
		t.Fatalf("testserver: blob: %v", err)
	}

	srv := server.New(server.Options{
		Config: cfg,
		Logger: audit.NewAppLogger(io.Discard, "error"),
		Access: audit.NewAccessLogger(io.Discard),
		DB:     db,
		Store:  st,
		Blob:   bs,
	})

	ts := httptest.NewUnstartedServer(srv.Handler())
	_ = ts.Listener.Close() // discard the default listener; use our reserved one
	ts.Listener = ln
	ts.Start()

	s := &Server{BaseURL: baseURL, AdminToken: seedAdminToken(t, st), Store: st, http: ts}
	t.Cleanup(func() {
		ts.Close()
		_ = db.Close()
	})
	return s
}

// seedAdminToken mints and stores an admin-scoped API token, returning the
// cleartext secret.
func seedAdminToken(t *testing.T, st *store.Store) string {
	t.Helper()
	secret, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("testserver: generate token: %v", err)
	}
	if err := st.CreateToken(context.Background(), &store.APIToken{
		Name: "test-admin", TokenHash: hash, TokenPrefix: prefix, Scopes: []byte(`["admin"]`),
	}); err != nil {
		t.Fatalf("testserver: create token: %v", err)
	}
	return secret
}

// Client returns a plain http.Client for ad-hoc requests against the server.
func (s *Server) Client() *http.Client { return s.http.Client() }
