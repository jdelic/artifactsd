package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// newFullHarness is like newHarness but also wires a real blob store, for
// tests that exercise uploads.
func newFullHarness(t *testing.T) *harness {
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

	key, err := session.GenerateKey()
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	sess := session.NewManager(key, "", true)

	h := New(Deps{
		Store:   st,
		Blob:    bs,
		Config:  cfg,
		Audit:   audit.NewRecorder(st.AuditSink(), nil),
		Session: sess,
		Logger:  audit.NewAppLogger(io.Discard, "error"),
	})
	return &harness{h: h.Routes(), st: st, blob: bs, sess: sess}
}

func testIdentity() *session.Identity {
	return &session.Identity{Subject: "u1", Email: "admin@example.com", Expiry: time.Now().Add(time.Hour)}
}

func doReq(t *testing.T, ha *harness, method, target string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, body)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r = withIdentity(r, testIdentity())
	rec := httptest.NewRecorder()
	ha.h.ServeHTTP(rec, r)
	return rec
}

func TestCollections_CreateListDelete(t *testing.T) {
	ha := newFullHarness(t)

	rec := doReq(t, ha, http.MethodGet, "/collections", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No collections yet") {
		t.Fatalf("initial list = %d, body %s", rec.Code, rec.Body.String())
	}

	form := url.Values{"name": {"demo-run"}}
	rec = doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "demo-run") {
		t.Fatalf("create = %d, body %s", rec.Code, rec.Body.String())
	}

	// Duplicate name surfaces an inline error, not a 500.
	rec = doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("duplicate create = %d, body %s", rec.Code, rec.Body.String())
	}

	// Invalid name surfaces a validation error.
	bad := url.Values{"name": {"Not Valid!"}}
	rec = doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(bad.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "invalid collection name") {
		t.Fatalf("invalid name create = %d, body %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/delete", http.NoBody, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "demo-run") {
		t.Fatalf("delete = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestCollectionTree_BrowseDelete(t *testing.T) {
	ha := newFullHarness(t)
	form := url.Values{"name": {"demo-run"}}
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")

	rec := doReq(t, ha, http.MethodGet, "/collections/demo-run/tree", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No files") {
		t.Fatalf("empty tree = %d, body %s", rec.Code, rec.Body.String())
	}
	// The page offers one upload form: the session upload with progress, which
	// also carries the Alt-U shortcut.
	if n := strings.Count(rec.Body.String(), "<form class=\"nc-form nc-upload"); n != 1 {
		t.Errorf("tree page has %d upload forms, want 1", n)
	}
	_, uploadForm, _ := strings.Cut(rec.Body.String(), "nc-large-upload")
	uploadForm, _, _ = strings.Cut(uploadForm, "</form>")
	if !strings.Contains(uploadForm, `data-shortcut="u"`) || !strings.Contains(uploadForm, "nc-upload-progress") {
		t.Errorf("upload form lacks shortcut or progress elements: %s", uploadForm)
	}

	// Seed a file at the root (the UI uploads via the session flow below,
	// which completes on the API's capability URL).
	seedFile(t, ha, "demo-run", "report.txt", "hello world")

	rec = doReq(t, ha, http.MethodGet, "/collections/demo-run/tree", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), "report.txt") {
		t.Fatalf("tree after upload missing file: %s", rec.Body.String())
	}

	// A file in a subdirectory; the parent folder is materialized.
	seedFile(t, ha, "demo-run", "assets/logo.png", "binarydata")
	rec = doReq(t, ha, http.MethodGet, "/collections/demo-run/tree", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), "assets") {
		t.Fatalf("tree missing materialized assets folder: %s", rec.Body.String())
	}
	rec = doReq(t, ha, http.MethodGet, "/collections/demo-run/tree?path=assets", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), "logo.png") {
		t.Fatalf("assets tree missing logo.png: %s", rec.Body.String())
	}

	// Delete the file, then the now-empty folder.
	delForm := url.Values{"dir": {"assets"}}
	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/delete-file/assets/logo.png", strings.NewReader(delForm.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "logo.png") {
		t.Fatalf("delete file = %d, body %s", rec.Code, rec.Body.String())
	}

	delForm = url.Values{"dir": {""}}
	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/delete-folder/assets", strings.NewReader(delForm.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "assets") {
		t.Fatalf("delete folder = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestBeginUpload_ThenCompleteViaCapabilityURL(t *testing.T) {
	ha := newFullHarness(t)
	form := url.Values{"name": {"demo-run"}}
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")

	body, _ := json.Marshal(map[string]string{"path": "big.bin", "content_type": "application/octet-stream"})
	rec := doReq(t, ha, http.MethodPost, "/collections/demo-run/upload/begin", bytes.NewReader(body), "application/json")
	if rec.Code != http.StatusCreated {
		t.Fatalf("begin upload = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp beginUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode begin-upload response: %v", err)
	}
	if resp.UploadID == "" || !strings.Contains(resp.UploadURL, "/api/v1/uploads/"+resp.UploadID) {
		t.Fatalf("unexpected begin-upload response: %+v", resp)
	}
}

func seedFile(t *testing.T, ha *harness, collection, p, content string) {
	t.Helper()
	ctx := context.Background()
	c, err := ha.st.GetCollectionByName(ctx, collection)
	if err != nil {
		t.Fatalf("seed %s/%s: collection: %v", collection, p, err)
	}
	ref, size, sum, err := ha.blob.Put(ctx, strings.NewReader(content))
	if err != nil {
		t.Fatalf("seed %s/%s: blob: %v", collection, p, err)
	}
	parentID, err := ha.st.EnsureParentFolders(ctx, c.ID, p)
	if err != nil {
		t.Fatalf("seed %s/%s: folders: %v", collection, p, err)
	}
	refStr := string(ref)
	node := &store.Node{
		CollectionID: c.ID, ParentID: parentID, Path: p, Name: path.Base(p), Kind: store.KindFile,
		BlobID: &refStr, Size: &size, Checksum: &sum, Metadata: json.RawMessage(`{}`),
	}
	if err := ha.st.UpsertNode(ctx, node); err != nil {
		t.Fatalf("seed %s/%s: node: %v", collection, p, err)
	}
	if err := ha.st.RecomputeNodeDenormalized(ctx, node.ID); err != nil {
		t.Fatalf("seed %s/%s: recompute: %v", collection, p, err)
	}
}

// TestBeginUpload_InfersContentTypeFromExtension: browsers send no type for
// extensions they don't know, so begin-upload falls back to the extension.
func TestBeginUpload_InfersContentTypeFromExtension(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")

	for _, tc := range []struct{ path, sent, want string }{
		{"page.html", "", "text/html; charset=utf-8"},
		{"page2.html", "application/octet-stream", "text/html; charset=utf-8"},
		{"data.bin", "application/x-custom", "application/x-custom"},
	} {
		body, _ := json.Marshal(map[string]string{"path": tc.path, "content_type": tc.sent})
		rec := doReq(t, ha, http.MethodPost, "/collections/demo-run/upload/begin", bytes.NewReader(body), "application/json")
		if rec.Code != http.StatusCreated {
			t.Fatalf("begin %s = %d: %s", tc.path, rec.Code, rec.Body.String())
		}
		var resp beginUploadResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		sess, err := ha.st.GetUploadSession(context.Background(), resp.UploadID)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if sess.ContentType != nil {
			got = *sess.ContentType
		}
		if got != tc.want {
			t.Errorf("%s (sent %q): content type = %q, want %q", tc.path, tc.sent, got, tc.want)
		}
	}
}
