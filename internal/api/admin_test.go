package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/store"
)

func TestTokens_CRUD(t *testing.T) {
	h := newHarness(t)

	// List: the harness pre-creates the admin and ro tokens.
	rec := h.do(t, h.token, http.MethodGet, "/api/v1/tokens", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list tokens = %d (%s)", rec.Code, rec.Body)
	}
	var list listResponse[tokenView]
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 2 {
		t.Errorf("token count = %d, want 2", len(list.Items))
	}

	// Create a scoped token; the secret is returned exactly once.
	rec = h.do(t, h.token, http.MethodPost, "/api/v1/tokens",
		[]byte(`{"name":"ci","scopes":["collections:read"]}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token = %d (%s)", rec.Code, rec.Body)
	}
	var created createTokenResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Token == "" || created.Token[:4] != "tok_" {
		t.Errorf("created token secret = %q", created.Token)
	}
	if created.Prefix == "" || created.ID == 0 {
		t.Errorf("created token view = %+v", created.tokenView)
	}

	// The new token authenticates and is scoped.
	if rec := h.do(t, created.Token, http.MethodGet, "/api/v1/collections", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("new token read = %d, want 200", rec.Code)
	}

	// Unknown scope → 400.
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/tokens",
		[]byte(`{"name":"bad","scopes":["bogus:scope"]}`), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown scope = %d, want 400", rec.Code)
	}
	// Missing name → 400.
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/tokens",
		[]byte(`{"scopes":["admin"]}`), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}

	// Delete (revoke) the created token.
	target := "/api/v1/tokens/" + strconv.FormatInt(created.ID, 10)
	if rec := h.do(t, h.token, http.MethodDelete, target, nil, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete token = %d, want 204", rec.Code)
	}
	// Revoked token no longer authenticates.
	if rec := h.do(t, created.Token, http.MethodGet, "/api/v1/collections", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token = %d, want 401", rec.Code)
	}
	// Second delete → 404 (already revoked).
	if rec := h.do(t, h.token, http.MethodDelete, target, nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("re-delete = %d, want 404", rec.Code)
	}
}

func TestTokens_AdminScoped(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(t, h.roToken, http.MethodGet, "/api/v1/tokens", nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("ro token list tokens = %d, want 403", rec.Code)
	}
}

func TestAudit_Query(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/a.txt", []byte("x"), nil)

	rec := h.do(t, h.token, http.MethodGet, "/api/v1/audit", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit = %d (%s)", rec.Code, rec.Body)
	}
	var all listResponse[auditView]
	_ = json.Unmarshal(rec.Body.Bytes(), &all)
	if len(all.Items) < 2 {
		t.Errorf("audit entries = %d, want >= 2", len(all.Items))
	}

	// Filter by action.
	rec = h.do(t, h.token, http.MethodGet, "/api/v1/audit?action=collection.create", nil, nil)
	var filtered listResponse[auditView]
	_ = json.Unmarshal(rec.Body.Bytes(), &filtered)
	if len(filtered.Items) != 1 || filtered.Items[0].Action != audit.ActionCollectionCreate {
		t.Errorf("filtered audit = %+v", filtered.Items)
	}

	// Bad since → 400.
	if rec := h.do(t, h.token, http.MethodGet, "/api/v1/audit?since=notatime", nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad since = %d, want 400", rec.Code)
	}
}

func TestStats(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/docs/a.txt", []byte("hello"), nil)

	rec := h.do(t, h.token, http.MethodGet, "/api/v1/stats", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats = %d (%s)", rec.Code, rec.Body)
	}
	var st store.Stats
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Collections != 1 || st.Files != 1 || st.Folders != 1 {
		t.Errorf("counts = %+v", st)
	}
	if st.StorageBytes != int64(len("hello")) || st.Blobs != 1 {
		t.Errorf("storage = %d bytes, %d blobs", st.StorageBytes, st.Blobs)
	}
}

type fakeCleaner struct {
	called bool
	result any
	err    error
}

func (f *fakeCleaner) RunOnce(context.Context) (any, error) {
	f.called = true
	return f.result, f.err
}

func TestMaintenance_NoCleaner(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/maintenance/cleanup", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("cleanup without cleaner = %d, want 503", rec.Code)
	}
}

func TestMaintenance_WithCleaner(t *testing.T) {
	fc := &fakeCleaner{result: map[string]int{"nodes_deleted": 3}}
	h, token := newHarnessWithCleaner(t, fc)
	rec := h.do(t, token, http.MethodPost, "/api/v1/maintenance/cleanup", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup = %d (%s)", rec.Code, rec.Body)
	}
	if !fc.called {
		t.Errorf("cleaner was not invoked")
	}
	var body struct {
		Cleanup map[string]int `json:"cleanup"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Cleanup["nodes_deleted"] != 3 {
		t.Errorf("cleanup result = %+v", body.Cleanup)
	}
}

// newHarnessWithCleaner builds a harness whose API handler has a Cleaner wired.
func newHarnessWithCleaner(t *testing.T, c Cleaner) (*harness, string) {
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
		func(string) string { return "" }, io.Discard,
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	h := New(Deps{
		Store: st, Blob: bs, Config: cfg, Cleaner: c,
		Audit:   audit.NewRecorder(st.AuditSink(), nil),
		Authn:   auth.NewAuthenticator(st.AuthTokenSource()),
		BaseURL: cfg.Server.BaseURL,
	})
	return &harness{h: h.Routes()}, mkToken(t, st, "admin", `["admin"]`)
}
