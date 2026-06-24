package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/store"
)

// serverWithStore is like serverWithEnv but also wires a real Store/Blob, so
// s.adminUI (and s.api/s.serve) are non-nil — exercising the full admin route
// wiring (adminGate -> csrfGate -> admin.Routes()) rather than the
// notImplemented stub.
func serverWithStore(t *testing.T) *Server {
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
	bs, err := blob.NewFSStore(dir)
	if err != nil {
		t.Fatalf("blob: %v", err)
	}

	env := map[string]string{
		"ARTIFACTSD_OIDC_ISSUER":       "https://idp.example.com",
		"ARTIFACTSD_OIDC_CLIENT_ID":    "artifactsd",
		"ARTIFACTSD_OIDC_REDIRECT_URL": "https://x.example.com/auth/oidc/callback",
		"ARTIFACTSD_ADMIN_GROUP":       "admins",
		"ARTIFACTSD_BASE_URL":          "https://x.example.com",
		"ARTIFACTSD_DATA_DIR":          dir,
	}
	cfg, err := config.LoadAndValidate(nil, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	return New(Options{
		Config: cfg,
		Logger: audit.NewAppLogger(io.Discard, "error"),
		Access: audit.NewAccessLogger(io.Discard),
		DB:     db,
		Store:  st,
		Blob:   bs,
	})
}

func TestAdminUI_DashboardEndToEnd(t *testing.T) {
	s := serverWithStore(t)
	r := httptest.NewRequest(http.MethodGet, "/admin/", http.NoBody)
	r.AddCookie(adminSession(t, s, []string{"admins"}))
	r.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Storage used") {
		t.Errorf("dashboard body missing stats: %s", rec.Body.String())
	}
}

func TestAdminUI_CleanupRequiresCSRFToken(t *testing.T) {
	s := serverWithStore(t)
	cookie := adminSession(t, s, []string{"admins"})

	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cleanup without CSRF token = %d, want 403", rec.Code)
	}
}

// TestAdminUI_BasicAuthCSRFRoundTrip exercises the gap a synthetic
// session-cookie-only test would miss: the basic-auth fallback carries no
// session cookie at all, so the CSRF token minted while rendering the
// dashboard (GET) must still be derivable and verifiable on a later request
// (POST) purely from the synthesized identity (adminGate's job — see its doc
// comment) rather than from any per-request state.
func TestAdminUI_BasicAuthCSRFRoundTrip(t *testing.T) {
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
	bs, err := blob.NewFSStore(dir)
	if err != nil {
		t.Fatalf("blob: %v", err)
	}

	hash, _ := auth.HashPassword("s3cret")
	env := map[string]string{
		"ARTIFACTSD_ADMIN_BASIC_USER":          "root",
		"ARTIFACTSD_ADMIN_BASIC_PASSWORD_HASH": hash,
		"ARTIFACTSD_BASE_URL":                  "https://x.example.com",
		"ARTIFACTSD_DATA_DIR":                  dir,
	}
	cfg, err := config.LoadAndValidate(nil, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	s := New(Options{
		Config: cfg,
		Logger: audit.NewAppLogger(io.Discard, "error"),
		Access: audit.NewAccessLogger(io.Discard),
		DB:     db,
		Store:  st,
		Blob:   bs,
	})

	// GET renders the dashboard (and mints a CSRF token) using only the basic
	// auth header, no cookie.
	get := httptest.NewRequest(http.MethodGet, "/admin/", http.NoBody)
	get.SetBasicAuth("root", "s3cret")
	getRec := httptest.NewRecorder()
	s.routes().ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET dashboard = %d, body %s", getRec.Code, getRec.Body.String())
	}
	token := extractCSRFToken(t, getRec.Body.String())

	// POST with that token, again only basic auth, no cookie, must succeed.
	post := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	post.SetBasicAuth("root", "s3cret")
	post.Header.Set("X-CSRF-Token", token)
	postRec := httptest.NewRecorder()
	s.routes().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST cleanup = %d, body %s", postRec.Code, postRec.Body.String())
	}
}

// extractCSRFToken pulls the token out of the rendered hx-headers attribute.
func extractCSRFToken(t *testing.T, body string) string {
	t.Helper()
	const marker = `X-CSRF-Token":"`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("CSRF token not found in body: %s", body)
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("malformed CSRF token attribute in body: %s", body)
	}
	return rest[:j]
}

func TestAdminUI_CleanupWithValidCSRFToken(t *testing.T) {
	s := serverWithStore(t)
	cookie := adminSession(t, s, []string{"admins"})

	idReq := httptest.NewRequest(http.MethodGet, "/admin/", http.NoBody)
	idReq.AddCookie(cookie)
	id, err := s.session.FromRequest(idReq)
	if err != nil {
		t.Fatalf("FromRequest: %v", err)
	}
	token := s.session.CSRFToken(id)

	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", token)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup with valid CSRF token = %d, body %s", rec.Code, rec.Body.String())
	}
}
