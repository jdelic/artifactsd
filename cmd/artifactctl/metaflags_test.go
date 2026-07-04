package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

func TestVisibilityFlags_Resolve(t *testing.T) {
	tests := []struct {
		name    string
		flags   visibilityFlags
		want    string
		wantErr bool
	}{
		{name: "none", flags: visibilityFlags{}, want: ""},
		{name: "shorthand public", flags: visibilityFlags{public: true}, want: "public"},
		{name: "shorthand private", flags: visibilityFlags{private: true}, want: "private"},
		{name: "long form", flags: visibilityFlags{visibility: "unlisted"}, want: "unlisted"},
		{name: "invalid long form", flags: visibilityFlags{visibility: "hidden"}, wantErr: true},
		{name: "two shorthands", flags: visibilityFlags{public: true, private: true}, wantErr: true},
		{name: "shorthand plus long form", flags: visibilityFlags{public: true, visibility: "public"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.flags.resolve()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolve() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve(): %v", err)
			}
			if got != tt.want {
				t.Errorf("resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUploadMetaFlags_Document(t *testing.T) {
	u := uploadMetaFlags{
		vis:     visibilityFlags{protected: true},
		auth:    authFlags{bearerToken: "demo-token"},
		headers: []string{"cache-control=no-store"},
		noIndex: true,
	}
	raw, err := u.document()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Access struct {
			Visibility string `json:"visibility"`
			Auth       []struct {
				Type  string `json:"type"`
				Token string `json:"token"`
			} `json:"auth"`
		} `json:"access"`
		Content struct {
			Headers map[string]string `json:"headers"`
		} `json:"content"`
		Crawl struct {
			Indexable *bool `json:"indexable"`
		} `json:"crawl"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("document json: %v (%s)", err, raw)
	}
	if doc.Access.Visibility != "protected" {
		t.Errorf("visibility = %q", doc.Access.Visibility)
	}
	if len(doc.Access.Auth) != 1 || doc.Access.Auth[0].Type != "bearer" || doc.Access.Auth[0].Token != "demo-token" {
		t.Errorf("auth = %+v", doc.Access.Auth)
	}
	if doc.Content.Headers["cache-control"] != "no-store" {
		t.Errorf("headers = %+v", doc.Content.Headers)
	}
	if doc.Crawl.Indexable == nil || *doc.Crawl.Indexable {
		t.Errorf("indexable = %v, want false", doc.Crawl.Indexable)
	}
}

func TestUploadMetaFlags_Document_Empty(t *testing.T) {
	u := uploadMetaFlags{}
	raw, err := u.document()
	if err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Errorf("document() = %s, want nil", raw)
	}
}

func TestBuildAuthEntries_InvalidBasic(t *testing.T) {
	if _, err := buildAuthEntries("", "no-colon"); err == nil {
		t.Fatal("want usage error for --basic without colon")
	}
}

// TestE2E_CollectionCreate_ShorthandAndExpiry covers the SPEC §20.2 recipe:
// `collection create <name> --private --expires-in 14d`.
func TestE2E_CollectionCreate_ShorthandAndExpiry(t *testing.T) {
	e := newE2E(t)
	code, out, errOut := e.run("--output", "json", "collection", "create", "run-1", "--private", "--expires-in", "14d")
	if code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	var col apiclient.Collection
	if err := json.Unmarshal([]byte(out), &col); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if col.Visibility != "private" {
		t.Errorf("visibility = %q, want private", col.Visibility)
	}
	if col.ExpiresAt == nil || *col.ExpiresAt == "" {
		t.Errorf("expires_at = %v, want set", col.ExpiresAt)
	}

	// Conflicting shorthands are a usage error.
	if code, _, _ := e.run("collection", "create", "run-2", "--public", "--private"); code != exitUsage {
		t.Fatalf("conflicting shorthands: code=%d, want %d", code, exitUsage)
	}
	if code, _, _ := e.run("collection", "create", "run-3", "--public", "--visibility", "private"); code != exitUsage {
		t.Fatalf("shorthand+long conflict: code=%d, want %d", code, exitUsage)
	}
}

// TestE2E_Put_MetadataFlags covers the SPEC §20.3 recipe:
// `put ... --protected --bearer-token demo-token` plus headers/expiry/no-index.
func TestE2E_Put_MetadataFlags(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "client-demo"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(local, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := e.run("put", "client-demo/report.pdf", local,
		"--content-type", "application/pdf",
		"--protected", "--bearer-token", "demo-token",
		"--header", "cache-control=no-store",
		"--no-index", "--expires-in", "7d")
	if code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	code, out, errOut := e.run("--output", "json", "meta", "get", "client-demo/report.pdf")
	if code != exitOK {
		t.Fatalf("meta get: code=%d err=%s", code, errOut)
	}
	var got apiclient.FileMeta
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("meta json: %v (%s)", err, out)
	}
	own := string(got.Own)
	// Header names are canonicalized at write time.
	for _, want := range []string{`"protected"`, `"bearer"`, `"Cache-Control"`, `"expires_at"`} {
		if !strings.Contains(own, want) {
			t.Errorf("own = %s, missing %s", own, want)
		}
	}
	if strings.Contains(own, "demo-token") {
		t.Errorf("own metadata leaks the cleartext token: %s", own)
	}
	var eff struct {
		Visibility string `json:"visibility"`
		Indexable  bool   `json:"indexable"`
	}
	if err := json.Unmarshal(got.Effective, &eff); err != nil {
		t.Fatalf("effective json: %v", err)
	}
	if eff.Visibility != "protected" || eff.Indexable {
		t.Errorf("effective = %s, want protected + non-indexable", got.Effective)
	}
}

// TestE2E_Put_GeneratedBearerToken: --generate-bearer-token creates a token
// and prints it once, mirroring share create.
func TestE2E_Put_GeneratedBearerToken(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := e.run("put", "demo/a.txt", local, "--protected", "--generate-bearer-token")
	if code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "bearer token (shown once, save it):") {
		t.Errorf("output = %q, want generated-token line", out)
	}

	// Explicit value and generation are mutually exclusive.
	if code, _, _ := e.run("put", "demo/b.txt", local, "--bearer-token", "x", "--generate-bearer-token"); code != exitUsage {
		t.Fatalf("conflicting token flags: code=%d, want %d", code, exitUsage)
	}
}

// TestE2E_UniformExpiryFlags: every expiry-taking command accepts both
// --expires and --expires-in (artifactsd-599).
func TestE2E_UniformExpiryFlags(t *testing.T) {
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

	// meta set now takes --expires-in (previously only --expires).
	if code, _, errOut := e.run("meta", "set", "demo/a.txt", "--expires-in", "12h"); code != exitOK {
		t.Fatalf("meta set --expires-in: code=%d err=%s", code, errOut)
	}
	// token create now takes --expires (previously only --expires-in).
	if code, _, errOut := e.run("token", "create", "ci", "--scope", "collections:read",
		"--expires", "2039-01-01T00:00:00Z"); code != exitOK {
		t.Fatalf("token create --expires: code=%d err=%s", code, errOut)
	}
	// The pair stays mutually exclusive everywhere.
	if code, _, _ := e.run("meta", "set", "demo/a.txt", "--expires", "2039-01-01T00:00:00Z", "--expires-in", "1d"); code != exitUsage {
		t.Fatalf("mutually exclusive pair: code=%d, want %d", code, exitUsage)
	}
}

// TestE2E_Rm_FolderGuardAndRecursive: rm refuses folders with a hint; rm -r
// deletes the subtree via the folders endpoint (artifactsd-a0c).
func TestE2E_Rm_FolderGuardAndRecursive(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "demo/docs/a.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	// Plain rm on a folder: refused with the -r hint, nothing deleted.
	code, _, errOut := e.run("rm", "demo/docs")
	if code != exitError {
		t.Fatalf("rm folder: code=%d err=%s, want %d", code, errOut, exitError)
	}
	if !strings.Contains(errOut, "use rm -r") {
		t.Errorf("stderr = %q, want rm -r hint", errOut)
	}
	if code, _, _ := e.run("get", "demo/docs/a.txt"); code != exitOK {
		t.Fatalf("file gone after refused rm: code=%d", code)
	}

	// rm -r removes the folder and its contents.
	if code, _, errOut := e.run("rm", "-r", "demo/docs"); code != exitOK {
		t.Fatalf("rm -r: code=%d err=%s", code, errOut)
	}
	if code, _, _ := e.run("get", "demo/docs/a.txt"); code != exitNotFound {
		t.Fatalf("file after rm -r: code=%d, want %d", code, exitNotFound)
	}

	// rm -r on a file is the inverse conflict.
	if code, _, errOut := e.run("put", "demo/b.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}
	if code, _, _ := e.run("rm", "-r", "demo/b.txt"); code != exitError {
		t.Fatalf("rm -r on file: code=%d, want %d", code, exitError)
	}
}

// TestE2E_PutTree_MetadataFlags covers SPEC §20.1/§20.4: put-tree with
// --public, --no-index, and --header applied to every uploaded file.
func TestE2E_PutTree_MetadataFlags(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "web-assets"); code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := e.run("put-tree", "web-assets", dir,
		"--public", "--no-index", "--header", "access-control-allow-origin=*")
	if code != exitOK {
		t.Fatalf("put-tree: code=%d err=%s", code, errOut)
	}

	code, out, errOut := e.run("--output", "json", "meta", "get", "web-assets/app.css")
	if code != exitOK {
		t.Fatalf("meta get: code=%d err=%s", code, errOut)
	}
	var got apiclient.FileMeta
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("meta json: %v (%s)", err, out)
	}
	var own struct {
		Access struct {
			Visibility string `json:"visibility"`
		} `json:"access"`
		Content struct {
			Headers map[string]string `json:"headers"`
		} `json:"content"`
		Crawl struct {
			Indexable *bool `json:"indexable"`
		} `json:"crawl"`
	}
	if err := json.Unmarshal(got.Own, &own); err != nil {
		t.Fatalf("own json: %v (%s)", err, got.Own)
	}
	if own.Access.Visibility != "public" {
		t.Errorf("visibility = %q, want public", own.Access.Visibility)
	}
	// The server canonicalizes header names at write time.
	if own.Content.Headers["Access-Control-Allow-Origin"] != "*" {
		t.Errorf("headers = %+v, want Access-Control-Allow-Origin=*", own.Content.Headers)
	}
	if own.Crawl.Indexable == nil || *own.Crawl.Indexable {
		t.Errorf("indexable = %v, want false", own.Crawl.Indexable)
	}
}
