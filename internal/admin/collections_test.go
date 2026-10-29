package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	return &harness{h: h.Routes(), sess: sess}
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

func TestCollectionTree_BrowseUploadDelete(t *testing.T) {
	ha := newFullHarness(t)
	form := url.Values{"name": {"demo-run"}}
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")

	rec := doReq(t, ha, http.MethodGet, "/collections/demo-run/tree", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No files") {
		t.Fatalf("empty tree = %d, body %s", rec.Code, rec.Body.String())
	}
	// The small-upload form carries the progress/status hooks upload.js drives
	// via htmx events; without them a slow or rejected upload shows nothing.
	_, small, found := strings.Cut(rec.Body.String(), "nc-small-upload")
	small, _, _ = strings.Cut(small, "</form>")
	if !found || !strings.Contains(small, "nc-upload-progress") || !strings.Contains(small, "nc-upload-status") {
		t.Errorf("small upload form lacks progress/status elements: %s", small)
	}

	// Upload a small file at the root.
	rec = uploadMultipart(t, ha, "/collections/demo-run/upload", map[string]string{"dir": ""}, "report.txt", "hello world")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "report.txt") {
		t.Fatalf("upload = %d, body %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodGet, "/collections/demo-run/tree", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), "report.txt") {
		t.Fatalf("tree after upload missing file: %s", rec.Body.String())
	}

	// Upload into a subdirectory; the parent folder should be materialized.
	rec = uploadMultipart(t, ha, "/collections/demo-run/upload", map[string]string{"dir": "assets"}, "logo.png", "binarydata")
	if rec.Code != http.StatusOK {
		t.Fatalf("nested upload = %d, body %s", rec.Code, rec.Body.String())
	}
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

func uploadMultipart(t *testing.T, ha *harness, target string, fields map[string]string, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write file content: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return doReq(t, ha, http.MethodPost, target, &buf, w.FormDataContentType())
}
