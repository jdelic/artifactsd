package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/store"
)

type harness struct {
	h       http.Handler
	token   string
	roToken string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessEnv(t, nil)
}

// newHarnessEnv is like newHarness but lets a test override env-only settings
// (e.g. ARTIFACTSD_UPLOAD_TTL) that have no flag equivalent.
func newHarnessEnv(t *testing.T, env map[string]string) *harness {
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

	cfg, err := config.LoadAndValidate(
		[]string{"--base-url", "https://x.example.com", "--data", dir},
		func(k string) string { return env[k] }, io.Discard,
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	h := New(Deps{
		Store: st, Blob: bs, Config: cfg,
		Audit:   audit.NewRecorder(st.AuditSink(), nil),
		Authn:   auth.NewAuthenticator(st.AuthTokenSource()),
		BaseURL: cfg.Server.BaseURL,
	})

	return &harness{h: h.Routes(), token: mkToken(t, st, "admin", `["admin"]`), roToken: mkToken(t, st, "ro", `["collections:read"]`)}
}

func base64urlNoPad(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func mkToken(t *testing.T, st *store.Store, name, scopes string) string {
	t.Helper()
	tok, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if err := st.CreateToken(context.Background(), &store.APIToken{
		Name: name, TokenHash: hash, TokenPrefix: prefix, Scopes: []byte(scopes),
	}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	return tok
}

func (h *harness) do(t *testing.T, token, method, target string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader = http.NoBody
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rdr)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	return rec
}

func TestCollections_CRUD(t *testing.T) {
	h := newHarness(t)

	// Create.
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections",
		[]byte(`{"name":"demo","metadata":{"access":{"visibility":"public","directory_listing":true}}}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body)
	}
	var cv collectionView
	_ = json.Unmarshal(rec.Body.Bytes(), &cv)
	if cv.Name != "demo" || cv.Visibility != "public" {
		t.Errorf("created collection = %+v", cv)
	}

	// Duplicate → 409.
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil); rec.Code != http.StatusConflict {
		t.Errorf("duplicate = %d, want 409", rec.Code)
	}

	// Get.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("get = %d", rec.Code)
	}
	// Missing → 404.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/nope", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("get missing = %d, want 404", rec.Code)
	}

	// List.
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/collections", nil, nil)
	var list listResponse[collectionView]
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Errorf("list items = %d, want 1", len(list.Items))
	}

	// Patch metadata (merge).
	if rec := h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/meta",
		[]byte(`{"crawl":{"indexable":true}}`), nil); rec.Code != http.StatusOK {
		t.Errorf("patch = %d (%s)", rec.Code, rec.Body)
	}

	// Delete.
	if rec := h.do(t, h.token, http.MethodDelete, "/api/v1/collections/demo", nil, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", rec.Code)
	}
}

func TestCollections_BadName(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"Bad Name"}`), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad name = %d, want 400", rec.Code)
	}
}

func TestAuth_RequiredAndScoped(t *testing.T) {
	h := newHarness(t)
	// No token → 401.
	if rec := h.do(t, "", http.MethodGet, "/api/v1/collections", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
	// Read-only token on a write endpoint → 403.
	if rec := h.do(t, h.roToken, http.MethodPost, "/api/v1/collections", []byte(`{"name":"x"}`), nil); rec.Code != http.StatusForbidden {
		t.Errorf("ro token write = %d, want 403", rec.Code)
	}
	// Read-only token can read.
	if rec := h.do(t, h.roToken, http.MethodGet, "/api/v1/collections", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("ro token read = %d, want 200", rec.Code)
	}
}

func TestFiles_PutGetDelete(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	content := []byte("hello world")
	rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/docs/readme.txt", content,
		map[string]string{"Content-Type": "text/plain"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("put = %d (%s)", rec.Code, rec.Body)
	}
	var fv fileView
	_ = json.Unmarshal(rec.Body.Bytes(), &fv)
	if fv.Size != int64(len(content)) || fv.ContentType != "text/plain" || fv.Checksum == "" {
		t.Errorf("file view = %+v", fv)
	}
	if fv.URL != "https://x.example.com/c/demo/docs/readme.txt" {
		t.Errorf("url = %q", fv.URL)
	}

	// Download.
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/docs/readme.txt", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello world" {
		t.Errorf("get file = %d %q", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("content-type = %q", ct)
	}

	// Replace (idempotent PUT) → 200.
	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/docs/readme.txt", []byte("v2"), nil); rec.Code != http.StatusOK {
		t.Errorf("replace = %d, want 200", rec.Code)
	}

	// Parent folder was materialized.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/docs/readme.txt/meta", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("meta = %d", rec.Code)
	}

	// Delete.
	if rec := h.do(t, h.token, http.MethodDelete, "/api/v1/collections/demo/files/docs/readme.txt", nil, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d", rec.Code)
	}
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/docs/readme.txt", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", rec.Code)
	}
}

func TestFiles_TraversalRejected(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	// net/http's mux cleans "../" into a 301 redirect before the handler; either
	// way the upload must not succeed (paths.Clean rejection is unit-tested too).
	rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/../escape", []byte("x"), nil)
	if rec.Code < 300 {
		t.Errorf("traversal put = %d, want a non-success status", rec.Code)
	}
}

func TestFiles_MetadataHeaderAndSecretRedaction(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)

	// Upload with a bearer-protected metadata document via the header.
	md := `{"access":{"visibility":"protected","auth":[{"type":"bearer","token":"sekret"}]}}`
	enc := base64urlNoPad(md)
	rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/secret.txt", []byte("x"),
		map[string]string{metadataHeader: enc})
	if rec.Code != http.StatusCreated {
		t.Fatalf("put with metadata = %d (%s)", rec.Code, rec.Body)
	}

	// The returned own metadata must not leak the cleartext token or its hash.
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/secret.txt/meta", nil, nil)
	body := rec.Body.String()
	if bytes.Contains([]byte(body), []byte("sekret")) {
		t.Errorf("metadata leaked cleartext token: %s", body)
	}
	if !bytes.Contains([]byte(body), []byte("has_token")) {
		t.Errorf("expected has_token presence flag: %s", body)
	}
}
