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

	// Delete the collection.
	if code, _, errOut := e.run("collection", "delete", "demo"); code != exitOK {
		t.Fatalf("delete collection: code=%d err=%s", code, errOut)
	}
	if code, _, _ := e.run("collection", "show", "demo"); code != exitNotFound {
		t.Errorf("show after delete = %d, want %d", code, exitNotFound)
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
