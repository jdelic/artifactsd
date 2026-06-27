package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
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
	h   *Handler
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
	mux.HandleFunc("GET /robots.txt", h.RobotsRoot)
	mux.HandleFunc("HEAD /robots.txt", h.RobotsRoot)
	mux.HandleFunc("GET /sitemap.xml", h.SitemapRoot)
	mux.HandleFunc("HEAD /sitemap.xml", h.SitemapRoot)
	return &harness{mux: mux, st: st, bs: bs, cfg: cfg, h: h}
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
		CollectionID: c.ID, ParentID: h.ensureParents(t, c, p), Path: p, Name: path.Base(p), Kind: store.KindFile,
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

// ensureParents materializes folder nodes for p's ancestors (mirroring the
// real upload pipeline's design/07 §4 invariant: parent folder rows always
// exist), returning the immediate parent's id.
func (h *harness) ensureParents(t *testing.T, c *store.Collection, p string) *int64 {
	t.Helper()
	segs := strings.Split(p, "/")
	if len(segs) == 1 {
		return nil
	}
	var parentID *int64
	prefix := ""
	for _, seg := range segs[:len(segs)-1] {
		if prefix == "" {
			prefix = seg
		} else {
			prefix += "/" + seg
		}
		if existing, err := h.st.GetNode(context.Background(), c.ID, prefix); err == nil {
			id := existing.ID
			parentID = &id
			continue
		}
		folder := &store.Node{
			CollectionID: c.ID, ParentID: parentID, Path: prefix, Name: path.Base(prefix),
			Kind: store.KindFolder, Metadata: []byte(`{}`),
		}
		if err := h.st.UpsertNode(context.Background(), folder); err != nil {
			t.Fatalf("UpsertNode folder: %v", err)
		}
		if err := h.st.RecomputeNodeDenormalized(context.Background(), folder.ID); err != nil {
			t.Fatalf("recompute folder: %v", err)
		}
		id := folder.ID
		parentID = &id
	}
	return parentID
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

func TestServe_SandboxByDefault(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "report.html", "<h1>hi</h1>", `{}`, "text/html")

	rec := h.get("/c/demo/report.html", nil)
	if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox allow-scripts allow-downloads" {
		t.Errorf("CSP = %q, want the default sandbox", got)
	}
}

func TestServe_SandboxOptOut(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "trusted.html", "<h1>hi</h1>", `{"content":{"sandbox":false}}`, "text/html")

	rec := h.get("/c/demo/trusted.html", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("serve = %d", rec.Code)
	}
	for _, v := range rec.Header().Values("Content-Security-Policy") {
		if strings.Contains(v, "sandbox") {
			t.Errorf("opted-out content still sandboxed: %q", v)
		}
	}
}

// TestServe_SandboxLayersWithAuthorCSP confirms an author-supplied CSP is kept
// alongside (not replaced by) the server's sandbox CSP — both are emitted, and
// the browser intersects them so the author CSP can only tighten the sandbox.
func TestServe_SandboxLayersWithAuthorCSP(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "x.html", "<h1>hi</h1>", `{"content":{"headers":{"Content-Security-Policy":"default-src 'none'"}}}`, "text/html")

	rec := h.get("/c/demo/x.html", nil)
	vals := rec.Header().Values("Content-Security-Policy")
	var hasSandbox, hasAuthor bool
	for _, v := range vals {
		if strings.Contains(v, "sandbox") {
			hasSandbox = true
		}
		if strings.Contains(v, "default-src 'none'") {
			hasAuthor = true
		}
	}
	if !hasSandbox || !hasAuthor {
		t.Errorf("want both sandbox and author CSP headers, got %q", vals)
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

func TestServeDirectory_ListingJSON(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public","directory_listing":true}}`)
	h.file(t, c, "a.txt", "a", `{}`, "text/plain")
	h.file(t, c, "b.txt", "b", `{}`, "text/plain")

	rec := h.get("/c/demo/?format=json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing = %d (%s)", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var view struct {
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body)
	}
	if len(view.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2", view.Entries)
	}
}

func TestServeDirectory_ListingHTML(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public","directory_listing":true}}`)
	h.file(t, c, "a.txt", "a", `{}`, "text/plain")

	rec := h.get("/c/demo/", map[string]string{"Accept": "text/html"})
	if rec.Code != http.StatusOK {
		t.Fatalf("listing = %d (%s)", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "a.txt") {
		t.Errorf("listing body missing a.txt: %s", rec.Body)
	}
}

func TestServeDirectory_HidesPrivateAndUnlisted(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public","directory_listing":true}}`)
	h.file(t, c, "public.txt", "x", `{}`, "text/plain")
	h.file(t, c, "secret.txt", "x", `{"access":{"visibility":"private"}}`, "text/plain")
	h.file(t, c, "hidden.txt", "x", `{"access":{"visibility":"unlisted"}}`, "text/plain")

	rec := h.get("/c/demo/?format=json", nil)
	if !strings.Contains(rec.Body.String(), "public.txt") {
		t.Errorf("listing missing public.txt: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret.txt") || strings.Contains(rec.Body.String(), "hidden.txt") {
		t.Errorf("listing leaked a private/unlisted child: %s", rec.Body)
	}

	// Unlisted stays directly fetchable even though hidden from the listing.
	if rec := h.get("/c/demo/hidden.txt", nil); rec.Code != http.StatusOK {
		t.Errorf("unlisted direct fetch = %d, want 200", rec.Code)
	}
}

func TestServeDirectory_HidesExpiredChildren(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public","directory_listing":true}}`)
	h.file(t, c, "live.txt", "x", `{}`, "text/plain")
	h.file(t, c, "gone.txt", "x", `{"expiry":{"expires_at":"2000-01-01T00:00:00Z"}}`, "text/plain")

	rec := h.get("/c/demo/?format=json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing = %d (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "live.txt") {
		t.Errorf("listing missing live.txt: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "gone.txt") {
		t.Errorf("listing leaked an expired child: %s", rec.Body)
	}
	// The listing must agree with the direct fetch, which already 404s.
	if rec := h.get("/c/demo/gone.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("expired direct fetch = %d, want 404", rec.Code)
	}
}

func TestServeShare_DirectoryListingHidesExpired(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "run-9c", `{"access":{"visibility":"private"}}`)
	h.file(t, c, "outputs/new.json", `{"ok":true}`, `{}`, "application/json")
	h.file(t, c, "outputs/old.json", `{"ok":false}`, `{"expiry":{"expires_at":"2000-01-01T00:00:00Z"}}`, "application/json")
	h.share(t, c, "shr_x", "outputs", "folder", `{"access":{"directory_listing":true}}`)

	rec := h.get("/s/shr_x/?format=json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("share listing = %d (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "new.json") {
		t.Errorf("share listing missing new.json: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "old.json") {
		t.Errorf("share listing leaked an expired child: %s", rec.Body)
	}
}

func TestServeDirectory_IndexFallbackWhenListingOff(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`) // directory_listing defaults false
	h.file(t, c, "sub/index.html", "<h1>sub index</h1>", `{}`, "text/html")

	rec := h.get("/c/demo/sub/", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "<h1>sub index</h1>" {
		t.Fatalf("index fallback = %d %q", rec.Code, rec.Body)
	}

	// No index.html at the root → 404, not a listing.
	if rec := h.get("/c/demo/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("root with no index = %d, want 404", rec.Code)
	}
}

func TestServeDirectory_FolderTargetWithoutTrailingSlash(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public","directory_listing":true}}`)
	h.file(t, c, "assets/logo.png", "img", `{}`, "image/png")

	rec := h.get("/c/demo/assets?format=json", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "logo.png") {
		t.Errorf("folder target (no slash) = %d %q", rec.Code, rec.Body)
	}
}

func TestServeDirectory_PrivateCollectionRootStays404(t *testing.T) {
	h := newHarness(t)
	h.collection(t, "secret", `{"access":{"visibility":"private","directory_listing":true}}`)
	if rec := h.get("/c/secret/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("private root = %d, want 404 (non-disclosure, not an index fallback)", rec.Code)
	}
}

func TestServeShare_DirectoryListing(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "run-8f31c2", `{"access":{"visibility":"private"}}`)
	h.file(t, c, "outputs/result.json", `{"ok":true}`, `{}`, "application/json")
	h.share(t, c, "shr_dir", "outputs", "folder", `{"access":{"directory_listing":true}}`)

	rec := h.get("/s/shr_dir/?format=json", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "result.json") {
		t.Errorf("share directory listing = %d %q", rec.Code, rec.Body)
	}
}

// TestServe_TouchesLastAccessThrottled exercises the design/08 §3 wiring
// end-to-end: serving a file advances its last_access_at and re-derives its
// denormalized expires_at from delete_after_last_access; a second serve within
// the throttle window is coalesced (no further write); once the injected clock
// advances past the window, the next serve persists again.
func TestServe_TouchesLastAccessThrottled(t *testing.T) {
	h := newHarness(t)
	window := h.cfg.Limits.AccessUpdateThrottle
	clock := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	h.h.WithClock(func() time.Time { return clock })

	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "report.txt", "hello", `{"expiry":{"delete_after_last_access":"3d"}}`, "text/plain")
	node, err := h.st.GetNode(context.Background(), c.ID, "report.txt")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.LastAccessAt != nil {
		t.Fatalf("last_access_at = %v before any serve, want nil", node.LastAccessAt)
	}

	if rec := h.get("/c/demo/report.txt", nil); rec.Code != http.StatusOK {
		t.Fatalf("serve 1 = %d", rec.Code)
	}
	node, _ = h.st.GetNode(context.Background(), c.ID, "report.txt")
	if node.LastAccessAt == nil || !node.LastAccessAt.Equal(clock) {
		t.Fatalf("last_access_at after serve 1 = %v, want %v", node.LastAccessAt, clock)
	}
	if node.ExpiresAt == nil || !node.ExpiresAt.Equal(clock.Add(3*24*time.Hour)) {
		t.Errorf("expires_at after serve 1 = %v, want %v", node.ExpiresAt, clock.Add(3*24*time.Hour))
	}

	// Within the throttle window: a second serve is coalesced, no write.
	clock = clock.Add(window / 2)
	if rec := h.get("/c/demo/report.txt", nil); rec.Code != http.StatusOK {
		t.Fatalf("serve 2 = %d", rec.Code)
	}
	node, _ = h.st.GetNode(context.Background(), c.ID, "report.txt")
	firstTouch := clock.Add(-window / 2)
	if !node.LastAccessAt.Equal(firstTouch) {
		t.Errorf("last_access_at after serve 2 (within window) = %v, want unchanged %v", node.LastAccessAt, firstTouch)
	}

	// Past the throttle window: the next serve persists again.
	clock = clock.Add(window)
	if rec := h.get("/c/demo/report.txt", nil); rec.Code != http.StatusOK {
		t.Fatalf("serve 3 = %d", rec.Code)
	}
	node, _ = h.st.GetNode(context.Background(), c.ID, "report.txt")
	if node.LastAccessAt == nil || !node.LastAccessAt.Equal(clock) {
		t.Errorf("last_access_at after serve 3 (past window) = %v, want %v", node.LastAccessAt, clock)
	}
	if node.ExpiresAt == nil || !node.ExpiresAt.Equal(clock.Add(3*24*time.Hour)) {
		t.Errorf("expires_at after serve 3 = %v, want %v", node.ExpiresAt, clock.Add(3*24*time.Hour))
	}
}
