package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestAdminUpload_AdminHostUploadURLIsSameOrigin: with ARTIFACTSD_ADMIN_HOST
// set, the admin UI's begin-upload returns an upload_url on the admin host —
// a public-host URL would be a cross-origin PUT the server has no CORS for —
// and the PUT to it succeeds there (artifactsd-m9e.15).
func TestAdminUpload_AdminHostUploadURLIsSameOrigin(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	bs, err := blob.NewFSStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("s3cret")
	env := map[string]string{
		"ARTIFACTSD_ADMIN_BASIC_USER":          "root",
		"ARTIFACTSD_ADMIN_BASIC_PASSWORD_HASH": hash,
		"ARTIFACTSD_BASE_URL":                  "https://pub.example.com",
		"ARTIFACTSD_ADMIN_HOST":                "admin.example.com",
		"ARTIFACTSD_DATA_DIR":                  dir,
	}
	cfg, err := config.LoadAndValidate(nil, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Config: cfg, Logger: audit.NewAppLogger(io.Discard, "error"), Access: audit.NewAccessLogger(io.Discard),
		DB: db, Store: st, Blob: bs,
	})
	if err := st.CreateCollection(context.Background(), &store.Collection{Name: "demo", Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	send := func(method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://admin.example.com"+target, strings.NewReader(body))
		r.Host = "admin.example.com"
		r.SetBasicAuth("root", "s3cret")
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	dash := send(http.MethodGet, "/admin/", "", nil)
	if dash.Code != http.StatusOK {
		t.Fatalf("dashboard on admin host = %d", dash.Code)
	}
	token := extractCSRFToken(t, dash.Body.String())
	rec := send(http.MethodPost, "/admin/collections/demo/upload/begin", `{"path":"f.txt"}`, map[string]string{
		"Content-Type": "application/json", "X-CSRF-Token": token, "Origin": "https://admin.example.com",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("begin upload = %d (%s)", rec.Code, rec.Body)
	}
	var resp struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(resp.UploadURL)
	if err != nil || u.Host != "admin.example.com" {
		t.Fatalf("upload_url = %q, want it on the admin host", resp.UploadURL)
	}

	put := httptest.NewRequest(http.MethodPut, resp.UploadURL, strings.NewReader("bytes"))
	put.Host = u.Host
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, put)
	if putRec.Code != http.StatusCreated {
		t.Errorf("PUT to upload_url on the admin host = %d (%s)", putRec.Code, putRec.Body)
	}
}
