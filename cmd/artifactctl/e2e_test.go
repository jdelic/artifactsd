package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// e2e drives the real command tree against a live in-process server, with the
// server connection injected via flags and a hermetic empty environment.
type e2e struct {
	t  *testing.T
	ts *testserver.Server
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	return &e2e{t: t, ts: testserver.New(t)}
}

// run executes artifactctl with --base-url/--token prepended, returning exit
// code, stdout, and stderr.
func (e *e2e) run(args ...string) (int, string, string) {
	e.t.Helper()
	full := append([]string{"--base-url", e.ts.BaseURL, "--token", e.ts.AdminToken}, args...)
	var out, errOut bytes.Buffer
	code := run(context.Background(), full, &out, &errOut, func(string) string { return "" })
	return code, out.String(), errOut.String()
}

func TestE2E_CollectionAndFileLifecycle(t *testing.T) {
	e := newE2E(t)

	// Create a collection.
	if code, _, errOut := e.run("collection", "create", "demo", "--visibility", "public"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}

	// It appears in the list (JSON output).
	code, out, errOut := e.run("--output", "json", "collection", "list")
	if code != exitOK {
		t.Fatalf("list: code=%d err=%s", code, errOut)
	}
	var page apiclient.Page[apiclient.Collection]
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("list json: %v (%s)", err, out)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "demo" || page.Items[0].Visibility != "public" {
		t.Fatalf("list items = %+v", page.Items)
	}

	// Upload a file.
	local := filepath.Join(t.TempDir(), "readme.txt")
	if err := os.WriteFile(local, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "demo/docs/readme.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	// Download it back and verify the bytes round-trip.
	code, out, errOut = e.run("get", "demo/docs/readme.txt")
	if code != exitOK {
		t.Fatalf("get: code=%d err=%s", code, errOut)
	}
	if out != "hello world" {
		t.Errorf("downloaded = %q", out)
	}

	// Delete the file, then confirm it is gone (exit code 5, not-found).
	if code, _, errOut := e.run("rm", "demo/docs/readme.txt"); code != exitOK {
		t.Fatalf("rm: code=%d err=%s", code, errOut)
	}
	if code, _, _ := e.run("get", "demo/docs/readme.txt"); code != exitNotFound {
		t.Errorf("get after rm = %d, want %d", code, exitNotFound)
	}

	// Delete the collection. --force is required: the now-empty "docs" folder
	// node left behind by rm still counts as collection content (artifactsd-dfk).
	if code, _, errOut := e.run("collection", "delete", "demo", "--force"); code != exitOK {
		t.Fatalf("delete collection: code=%d err=%s", code, errOut)
	}
	if code, _, _ := e.run("collection", "show", "demo"); code != exitNotFound {
		t.Errorf("show after delete = %d, want %d", code, exitNotFound)
	}
}

// TestE2E_CollectionDelete_NonEmptyRequiresForce covers artifactsd-dfk: the
// server now rejects deleting a non-empty collection unless force=true, so
// `collection delete`'s --force flag is a real safety gate, not a no-op.
func TestE2E_CollectionDelete_NonEmptyRequiresForce(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "demo/a.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	if code, _, _ := e.run("collection", "delete", "demo"); code == exitOK {
		t.Fatalf("delete without --force on non-empty collection succeeded, want an error")
	}
	if code, _, errOut := e.run("collection", "show", "demo"); code != exitOK {
		t.Fatalf("show after rejected delete: code=%d err=%s, want collection to still exist", code, errOut)
	}

	if code, _, errOut := e.run("collection", "delete", "demo", "--force"); code != exitOK {
		t.Fatalf("delete --force: code=%d err=%s", code, errOut)
	}
	if code, _, _ := e.run("collection", "show", "demo"); code != exitNotFound {
		t.Errorf("show after forced delete = %d, want %d", code, exitNotFound)
	}
}

func TestE2E_PutTree(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "site"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}

	// Build a small tree with one excluded file.
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "index.html"), "<h1>hi</h1>")
	mustWrite(t, filepath.Join(root, "assets", "app.js"), "console.log(1)")
	mustWrite(t, filepath.Join(root, "notes.tmp"), "scratch")

	code, out, errOut := e.run("--output", "json", "put-tree", "site", root, "--exclude", "*.tmp")
	if code != exitOK {
		t.Fatalf("put-tree: code=%d err=%s", code, errOut)
	}
	var summary struct {
		Uploaded int `json:"uploaded"`
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("summary json: %v (%s)", err, out)
	}
	if summary.Uploaded != 2 {
		t.Errorf("uploaded = %d, want 2 (the .tmp file excluded)", summary.Uploaded)
	}

	// The nested file is reachable; the excluded one is not.
	if code, body, _ := e.run("get", "site/assets/app.js"); code != exitOK || !strings.Contains(body, "console.log") {
		t.Errorf("get nested = %d %q", code, body)
	}
	if code, _, _ := e.run("get", "site/notes.tmp"); code != exitNotFound {
		t.Errorf("excluded file present (code %d)", code)
	}
}

func TestE2E_CollectionCreate_VisibilityVariants(t *testing.T) {
	e := newE2E(t)
	for _, vis := range []string{"private", "unlisted", "protected"} {
		name := "coll-" + vis
		if code, _, errOut := e.run("collection", "create", name, "--visibility", vis); code != exitOK {
			t.Fatalf("create %s: code=%d err=%s", vis, code, errOut)
		}
		code, out, errOut := e.run("--output", "json", "collection", "show", name)
		if code != exitOK {
			t.Fatalf("show %s: code=%d err=%s", name, code, errOut)
		}
		var col apiclient.Collection
		if err := json.Unmarshal([]byte(out), &col); err != nil {
			t.Fatalf("collection json: %v (%s)", err, out)
		}
		if col.Visibility != vis {
			t.Errorf("collection %s visibility = %q, want %q", name, col.Visibility, vis)
		}
	}
}

func TestE2E_CollectionCreate_ListFlag(t *testing.T) {
	e := newE2E(t)
	listingFlag := func(metadata json.RawMessage) *bool {
		var doc struct {
			Access struct {
				DirectoryListing *bool `json:"directory_listing"`
			} `json:"access"`
		}
		if err := json.Unmarshal(metadata, &doc); err != nil {
			t.Fatalf("metadata json: %v (%s)", err, metadata)
		}
		return doc.Access.DirectoryListing
	}

	if code, _, errOut := e.run("collection", "create", "listed", "--list"); code != exitOK {
		t.Fatalf("create --list: code=%d err=%s", code, errOut)
	}
	code, out, errOut := e.run("--output", "json", "collection", "show", "listed")
	if code != exitOK {
		t.Fatalf("show: code=%d err=%s", code, errOut)
	}
	var col apiclient.Collection
	if err := json.Unmarshal([]byte(out), &col); err != nil {
		t.Fatalf("collection json: %v (%s)", err, out)
	}
	if got := listingFlag(col.Metadata); got == nil || !*got {
		t.Errorf("--list: directory_listing = %v, want true", got)
	}

	if code, _, errOut := e.run("collection", "create", "unlisted-dir", "--no-list"); code != exitOK {
		t.Fatalf("create --no-list: code=%d err=%s", code, errOut)
	}
	code, out, errOut = e.run("--output", "json", "collection", "show", "unlisted-dir")
	if code != exitOK {
		t.Fatalf("show: code=%d err=%s", code, errOut)
	}
	if err := json.Unmarshal([]byte(out), &col); err != nil {
		t.Fatalf("collection json: %v (%s)", err, out)
	}
	if got := listingFlag(col.Metadata); got == nil || *got {
		t.Errorf("--no-list: directory_listing = %v, want false", got)
	}
}

func TestE2E_CollectionCreate_CrawlFlags(t *testing.T) {
	e := newE2E(t)
	crawl := func(metadata json.RawMessage) struct {
		Crawlable *bool `json:"crawlable"`
		Indexable *bool `json:"indexable"`
	} {
		var doc struct {
			Crawl struct {
				Crawlable *bool `json:"crawlable"`
				Indexable *bool `json:"indexable"`
			} `json:"crawl"`
		}
		if err := json.Unmarshal(metadata, &doc); err != nil {
			t.Fatalf("metadata json: %v (%s)", err, metadata)
		}
		return doc.Crawl
	}

	if code, _, errOut := e.run("collection", "create", "indexable-demo", "--crawlable", "--index"); code != exitOK {
		t.Fatalf("create --crawlable --index: code=%d err=%s", code, errOut)
	}
	code, out, errOut := e.run("--output", "json", "collection", "show", "indexable-demo")
	if code != exitOK {
		t.Fatalf("show: code=%d err=%s", code, errOut)
	}
	var col apiclient.Collection
	if err := json.Unmarshal([]byte(out), &col); err != nil {
		t.Fatalf("collection json: %v (%s)", err, out)
	}
	got := crawl(col.Metadata)
	if got.Crawlable == nil || !*got.Crawlable {
		t.Errorf("--crawlable: crawlable = %v, want true", got.Crawlable)
	}
	if got.Indexable == nil || !*got.Indexable {
		t.Errorf("--index: indexable = %v, want true", got.Indexable)
	}

	if code, _, errOut := e.run("collection", "create", "bad", "--index", "--no-index"); code != exitUsage {
		t.Fatalf("create --index --no-index: code=%d err=%s, want exitUsage", code, errOut)
	}
}

func TestE2E_Put_ContentType(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(local, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := e.run("--output", "json", "put", "demo/data.bin", local, "--content-type", "application/x-custom")
	if code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}
	var info apiclient.FileInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("file json: %v (%s)", err, out)
	}
	if info.ContentType != "application/x-custom" {
		t.Errorf("content_type = %q, want application/x-custom", info.ContentType)
	}
}

func TestE2E_Get_OutputFile(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "demo/a.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	dst := filepath.Join(t.TempDir(), "downloaded.txt")
	if code, _, errOut := e.run("get", "demo/a.txt", "-o", dst); code != exitOK {
		t.Fatalf("get -o: code=%d err=%s", code, errOut)
	}
	got, err := os.ReadFile(dst) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("downloaded content = %q, want hello", got)
	}
}

func TestE2E_PutTree_IncludeGlob(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "site"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "index.html"), "<h1>hi</h1>")
	mustWrite(t, filepath.Join(root, "notes.tmp"), "scratch")

	code, out, errOut := e.run("--output", "json", "put-tree", "site", root, "--include", "*.html")
	if code != exitOK {
		t.Fatalf("put-tree: code=%d err=%s", code, errOut)
	}
	var summary struct {
		Uploaded int `json:"uploaded"`
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("summary json: %v (%s)", err, out)
	}
	if summary.Uploaded != 1 {
		t.Errorf("uploaded = %d, want 1 (only *.html included)", summary.Uploaded)
	}
	if code, _, _ := e.run("get", "site/notes.tmp"); code != exitNotFound {
		t.Errorf("non-included file present (code %d)", code)
	}
}

// TestE2E_PutTree_LargeUploadThreshold covers artifactsd-crx.1: a low
// --large-upload-threshold forces every file through the begin-upload +
// capability-PUT path (design/07 §3) instead of a direct PUT, and the content
// still round-trips correctly.
func TestE2E_PutTree_LargeUploadThreshold(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "site"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "hello from a")
	mustWrite(t, filepath.Join(root, "nested", "b.txt"), "hello from b")

	code, out, errOut := e.run("--output", "json", "put-tree", "site", root, "--large-upload-threshold", "1")
	if code != exitOK {
		t.Fatalf("put-tree: code=%d err=%s", code, errOut)
	}
	var summary struct {
		Uploaded int   `json:"uploaded"`
		Bytes    int64 `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("summary json: %v (%s)", err, out)
	}
	if summary.Uploaded != 2 {
		t.Errorf("uploaded = %d, want 2", summary.Uploaded)
	}

	if code, body, errOut := e.run("get", "site/a.txt"); code != exitOK || body != "hello from a" {
		t.Errorf("get a.txt = %d %q (err=%s)", code, body, errOut)
	}
	if code, body, errOut := e.run("get", "site/nested/b.txt"); code != exitOK || body != "hello from b" {
		t.Errorf("get nested/b.txt = %d %q (err=%s)", code, body, errOut)
	}
}

func TestE2E_AuthFailure(t *testing.T) {
	ts := testserver.New(t)
	var out, errOut bytes.Buffer
	code := run(context.Background(),
		[]string{"--base-url", ts.BaseURL, "--token", "tok_wrong", "collection", "list"},
		&out, &errOut, func(string) string { return "" })
	if code != exitAuth {
		t.Errorf("bad token exit = %d, want %d", code, exitAuth)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
