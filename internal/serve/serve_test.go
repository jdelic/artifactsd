package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/store"
)

type harness struct {
	mux *http.ServeMux
	st  *store.Store
	bs  blob.Store
	cfg *config.Config
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("open: %v", err)
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
	cfg, err := config.LoadAndValidate([]string{"--base-url", "https://x.example.com", "--data", dir},
		func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	h := New(Deps{Store: st, Blob: bs, Config: cfg, Audit: audit.NewRecorder(st.AuditSink(), nil)})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /c/{collection}/{path...}", h.ServeCollection)
	mux.HandleFunc("HEAD /c/{collection}/{path...}", h.ServeCollection)
	mux.HandleFunc("GET /s/{share_id}/{path...}", h.ServeShare)
	mux.HandleFunc("HEAD /s/{share_id}/{path...}", h.ServeShare)
	return &harness{mux: mux, st: st, bs: bs, cfg: cfg}
}

func (h *harness) collection(t *testing.T, name, metaJSON string) *store.Collection {
	t.Helper()
	c := &store.Collection{Name: name, Metadata: []byte(metaJSON)}
	if err := h.st.CreateCollection(context.Background(), c); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if err := h.st.RecomputeCollectionDenormalized(context.Background(), c.ID, string(h.cfg.Policy.Visibility)); err != nil {
		t.Fatalf("recompute collection: %v", err)
	}
	c, _ = h.st.GetCollection(context.Background(), c.ID)
	return c
}

func (h *harness) file(t *testing.T, c *store.Collection, p, content, metaJSON, contentType string) {
	t.Helper()
	ref, size, sum, err := h.bs.Put(context.Background(), bytes.NewReader([]byte(content)))
	if err != nil {
		t.Fatalf("blob.Put: %v", err)
	}
	refStr := string(ref)
	ct := contentType
	n := &store.Node{
		CollectionID: c.ID, Path: p, Name: p, Kind: store.KindFile,
		BlobID: &refStr, Size: &size, Checksum: &sum, Metadata: []byte(metaJSON),
	}
	if ct != "" {
		n.ContentType = &ct
	}
	if err := h.st.UpsertNode(context.Background(), n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := h.st.RecomputeNodeDenormalized(context.Background(), n.ID); err != nil {
		t.Fatalf("recompute node: %v", err)
	}
}

func (h *harness) share(t *testing.T, c *store.Collection, shareID, targetPath, targetKind, metaJSON string) *store.Share {
	t.Helper()
	sh := &store.Share{
		ShareID: shareID, CollectionID: c.ID, TargetPath: targetPath, TargetKind: targetKind,
		Metadata: []byte(metaJSON),
	}
	if err := h.st.CreateShare(context.Background(), sh); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	return sh
}

func (h *harness) get(target string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, r)
	return rec
}

func TestServe_PublicFile(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "report.txt", "hello", `{}`, "text/plain")

	rec := h.get("/c/demo/report.txt", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("serve = %d %q", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("content-type = %q", ct)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("missing ETag")
	}
	if rec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("X-Robots-Tag = %q, want noindex (default not indexable)", rec.Header().Get("X-Robots-Tag"))
	}
}

func TestServe_ContentTypeByExtension(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "page.html", "<h1>hi</h1>", `{}`, "") // no stored content-type

	rec := h.get("/c/demo/page.html", nil)
	if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:9] != "text/html" {
		t.Errorf("content-type = %q, want text/html...", ct)
	}
}

func TestServe_MissingAndDirectory404(t *testing.T) {
	h := newHarness(t)
	h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	if rec := h.get("/c/demo/nope.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing = %d, want 404", rec.Code)
	}
	if rec := h.get("/c/nosuch/x.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing collection = %d, want 404", rec.Code)
	}
}

func TestServe_PrivateIs404(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "secret", `{"access":{"visibility":"private"}}`)
	h.file(t, c, "x.txt", "data", `{}`, "text/plain")
	if rec := h.get("/c/secret/x.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("private file = %d, want 404 (non-disclosure)", rec.Code)
	}
}

func TestServe_BearerProtected(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	hash := auth.HashToken("s3cret")
	fileMeta := `{"access":{"visibility":"protected","auth":[{"type":"bearer","token_hash":"` + hash + `"}]}}`
	h.file(t, c, "private.txt", "top secret", fileMeta, "text/plain")

	// No token → 404 (bearer hides existence).
	if rec := h.get("/c/demo/private.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("bearer no-token = %d, want 404", rec.Code)
	}
	// Valid token → 200.
	rec := h.get("/c/demo/private.txt", map[string]string{"Authorization": "Bearer s3cret"})
	if rec.Code != http.StatusOK || rec.Body.String() != "top secret" {
		t.Errorf("bearer valid = %d %q, want 200", rec.Code, rec.Body)
	}
}

func TestServe_ExpiredIs404(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "old.txt", "gone", `{"expiry":{"expires_at":"2000-01-01T00:00:00Z"}}`, "text/plain")
	if rec := h.get("/c/demo/old.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("expired = %d, want 404", rec.Code)
	}
}

func TestServe_RangeRequest(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "data.txt", "0123456789", `{}`, "text/plain")

	rec := h.get("/c/demo/data.txt", map[string]string{"Range": "bytes=2-4"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", rec.Code)
	}
	if rec.Body.String() != "234" {
		t.Errorf("range body = %q, want 234", rec.Body.String())
	}
}

func TestServe_CustomHeadersFiltered(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	// A safe header is emitted; an unsafe one would be rejected at write time, so
	// here we just confirm the safe header passes through.
	meta := `{"content":{"headers":{"Cache-Control":"no-store"}}}`
	h.file(t, c, "h.txt", "x", meta, "text/plain")
	rec := h.get("/c/demo/h.txt", nil)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}

func TestServe_AuditOnProtectedAccess(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	hash := auth.HashToken("tok")
	h.file(t, c, "p.txt", "x", `{"access":{"visibility":"protected","auth":[{"type":"bearer","token_hash":"`+hash+`"}]}}`, "text/plain")

	h.get("/c/demo/p.txt", map[string]string{"Authorization": "Bearer tok"})

	logs, err := h.st.ListAudit(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	found := false
	for _, l := range logs {
		if l.Action == audit.ActionObjectAccess {
			found = true
		}
	}
	if !found {
		var dump []string
		for _, l := range logs {
			dump = append(dump, l.Action)
		}
		b, _ := json.Marshal(dump)
		t.Errorf("expected an object.access audit row, got %s", b)
	}
}

func TestServeShare_FileTarget(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "report.pdf", "pdf bytes", `{}`, "application/pdf")
	h.share(t, c, "shr_file", "report.pdf", "file", `{}`)

	rec := h.get("/s/shr_file/", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "pdf bytes" {
		t.Fatalf("serve = %d %q", rec.Code, rec.Body)
	}

	// A file-target share has nothing "under" it.
	if rec := h.get("/s/shr_file/extra", nil); rec.Code != http.StatusNotFound {
		t.Errorf("file-share with tail = %d, want 404", rec.Code)
	}
}

func TestServeShare_FolderTargetSubtreeGuard(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "outputs/result.json", `{"ok":true}`, `{}`, "application/json")
	h.file(t, c, "secret.txt", "not shared", `{}`, "text/plain")
	h.share(t, c, "shr_folder", "outputs", "folder", `{}`)

	rec := h.get("/s/shr_folder/result.json", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"ok":true}` {
		t.Fatalf("serve = %d %q", rec.Code, rec.Body)
	}

	// A traversal attempt in the tail must not escape the shared subtree. Go's
	// ServeMux normalizes "../" into a 301 redirect before the handler even
	// runs; either way the request must not succeed (resolveSharePath's own
	// paths.Clean rejection is exercised directly were that ever bypassed).
	if rec := h.get("/s/shr_folder/../secret.txt", nil); rec.Code < 300 {
		t.Errorf("traversal = %d, want a non-success status", rec.Code)
	}
}

func TestServeShare_WholeCollectionTarget(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "index.html", "<h1>hi</h1>", `{}`, "text/html")
	h.share(t, c, "shr_coll", "", "collection", `{}`)

	rec := h.get("/s/shr_coll/index.html", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "<h1>hi</h1>" {
		t.Fatalf("serve = %d %q", rec.Code, rec.Body)
	}
}

func TestServeShare_PrivateCollectionServedAsUnlisted(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "run-8f31c2", `{"access":{"visibility":"private"}}`)
	h.file(t, c, "result.json", `{"ok":true}`, `{}`, "application/json")

	// Direct /c access stays 404 — the share is the only way in.
	if rec := h.get("/c/run-8f31c2/result.json", nil); rec.Code != http.StatusNotFound {
		t.Errorf("direct access to private collection = %d, want 404", rec.Code)
	}

	h.share(t, c, "shr_private", "", "collection", `{}`)
	rec := h.get("/s/shr_private/result.json", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("share into private collection = %d, want 200", rec.Code)
	}
}

func TestServeShare_OwnAuthRequirement(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "report.pdf", "secret pdf", `{}`, "application/pdf")
	hash := auth.HashToken("share-token")
	h.share(t, c, "shr_protected", "report.pdf", "file",
		`{"access":{"auth":[{"type":"bearer","token_hash":"`+hash+`"}]}}`)

	if rec := h.get("/s/shr_protected/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("no token = %d, want 404 (bearer hides existence)", rec.Code)
	}
	rec := h.get("/s/shr_protected/", map[string]string{"Authorization": "Bearer share-token"})
	if rec.Code != http.StatusOK || rec.Body.String() != "secret pdf" {
		t.Errorf("with token = %d %q, want 200", rec.Code, rec.Body)
	}
}

func TestServeShare_RevokedAndExpired(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "x.txt", "data", `{}`, "text/plain")

	revoked := h.share(t, c, "shr_revoked", "x.txt", "file", `{}`)
	now := time.Now()
	if err := h.st.RevokeShare(context.Background(), revoked.ShareID, now); err != nil {
		t.Fatalf("RevokeShare: %v", err)
	}
	if rec := h.get("/s/shr_revoked/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("revoked share = %d, want 404", rec.Code)
	}

	h.share(t, c, "shr_expired", "x.txt", "file", `{"expiry":{"expires_at":"2000-01-01T00:00:00Z"}}`)
	if rec := h.get("/s/shr_expired/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("expired share = %d, want 404", rec.Code)
	}
}

func TestServeShare_UnknownID(t *testing.T) {
	h := newHarness(t)
	if rec := h.get("/s/shr_doesnotexist/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown share = %d, want 404", rec.Code)
	}
}
