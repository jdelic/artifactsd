package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

func TestE2E_MetaGetSet(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create collection: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "demo/a.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	if code, _, errOut := e.run("meta", "set", "demo/a.txt", "--visibility", "unlisted", "--crawlable"); code != exitOK {
		t.Fatalf("meta set: code=%d err=%s", code, errOut)
	}

	code, out, errOut := e.run("--output", "json", "meta", "get", "demo/a.txt")
	if code != exitOK {
		t.Fatalf("meta get: code=%d err=%s", code, errOut)
	}
	var got apiclient.FileMeta
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("meta json: %v (%s)", err, out)
	}
	if !strings.Contains(string(got.Own), "unlisted") {
		t.Errorf("own = %s, want visibility unlisted", got.Own)
	}
	var eff struct {
		Crawlable bool `json:"crawlable"`
	}
	if err := json.Unmarshal(got.Effective, &eff); err != nil {
		t.Fatalf("effective json: %v (%s)", err, got.Effective)
	}
	if !eff.Crawlable {
		t.Errorf("effective = %s, want crawlable true", got.Effective)
	}
}

// TestE2E_MetaGetSet_Folder confirms `meta get`/`meta set` also works against
// a folder path, not just a file: the underlying .../files/{path}/meta
// endpoint resolves any node by path regardless of kind (internal/api
// resolveFile doesn't discriminate on Kind), so the CLI's single `meta`
// command happens to cover folders too.
func TestE2E_MetaGetSet_Folder(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create collection: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Uploading a nested file materializes its parent folder.
	if code, _, errOut := e.run("put", "demo/docs/a.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	if code, _, errOut := e.run("meta", "set", "demo/docs", "--visibility", "unlisted"); code != exitOK {
		t.Fatalf("meta set folder: code=%d err=%s", code, errOut)
	}
	code, out, errOut := e.run("--output", "json", "meta", "get", "demo/docs")
	if code != exitOK {
		t.Fatalf("meta get folder: code=%d err=%s", code, errOut)
	}
	var got apiclient.FileMeta
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("meta json: %v (%s)", err, out)
	}
	if !strings.Contains(string(got.Own), "unlisted") {
		t.Errorf("folder own = %s, want visibility unlisted", got.Own)
	}
}

func TestE2E_ShareLifecycle(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create collection: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "demo/a.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	code, out, errOut := e.run("--output", "json", "share", "create", "demo/a.txt", "--unlisted", "--expires-in", "1h")
	if code != exitOK {
		t.Fatalf("share create: code=%d err=%s", code, errOut)
	}
	var sh apiclient.Share
	if err := json.Unmarshal([]byte(out), &sh); err != nil {
		t.Fatalf("share json: %v (%s)", err, out)
	}
	if sh.ShareID == "" || sh.ExpiresAt == nil {
		t.Fatalf("share = %+v", sh)
	}

	code, out, errOut = e.run("--output", "json", "share", "list", "demo")
	if code != exitOK {
		t.Fatalf("share list: code=%d err=%s", code, errOut)
	}
	var page apiclient.Page[apiclient.Share]
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("share list json: %v (%s)", err, out)
	}
	if len(page.Items) != 1 || page.Items[0].ShareID != sh.ShareID {
		t.Errorf("shares = %+v", page.Items)
	}

	if code, _, errOut := e.run("share", "revoke", sh.ShareID); code != exitOK {
		t.Fatalf("share revoke: code=%d err=%s", code, errOut)
	}
	code, out, errOut = e.run("--output", "json", "share", "list", "demo")
	if code != exitOK {
		t.Fatalf("share list after revoke: code=%d err=%s", code, errOut)
	}
	page = apiclient.Page[apiclient.Share]{}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("share list json: %v (%s)", err, out)
	}
	if len(page.Items) != 0 {
		t.Errorf("shares after revoke = %+v, want none", page.Items)
	}
}

func TestE2E_ShareCreate_GeneratedBearerToken(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create collection: code=%d err=%s", code, errOut)
	}
	code, out, errOut := e.run("--output", "json", "share", "create", "demo", "--protected", "--bearer-token")
	if code != exitOK {
		t.Fatalf("share create: code=%d err=%s", code, errOut)
	}
	var resp struct {
		GeneratedBearerToken string `json:"generated_bearer_token"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("share json: %v (%s)", err, out)
	}
	if resp.GeneratedBearerToken == "" {
		t.Errorf("stdout = %q, want a generated_bearer_token field", out)
	}
}

func TestE2E_ShareCreate_FolderTarget(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "demo"); code != exitOK {
		t.Fatalf("create collection: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "demo/assets/a.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	code, out, errOut := e.run("--output", "json", "share", "create", "demo/assets")
	if code != exitOK {
		t.Fatalf("share create folder: code=%d err=%s", code, errOut)
	}
	var sh apiclient.Share
	if err := json.Unmarshal([]byte(out), &sh); err != nil {
		t.Fatalf("share json: %v (%s)", err, out)
	}
	if sh.TargetKind != "folder" || sh.TargetPath != "assets" {
		t.Errorf("share = %+v, want target_kind=folder target_path=assets", sh)
	}
}

func TestE2E_Tree_PathAndDepth(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "site"); code != exitOK {
		t.Fatalf("create collection: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "deep.txt")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "site/a/b/deep.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}
	if code, _, errOut := e.run("put", "site/top.txt", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	// --path scopes the listing to the "a" subtree, excluding top.txt.
	code, out, errOut := e.run("--output", "json", "tree", "site", "--path", "a")
	if code != exitOK {
		t.Fatalf("tree --path: code=%d err=%s", code, errOut)
	}
	var page apiclient.Page[apiclient.TreeNode]
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("tree json: %v (%s)", err, out)
	}
	for _, n := range page.Items {
		if n.Path == "top.txt" {
			t.Errorf("tree --path=a unexpectedly included top.txt: %+v", page.Items)
		}
	}

	// --depth 1 from the root sees "a" (folder) but not the deeper file.
	code, out, errOut = e.run("--output", "json", "tree", "site", "--depth", "1")
	if code != exitOK {
		t.Fatalf("tree --depth: code=%d err=%s", code, errOut)
	}
	page = apiclient.Page[apiclient.TreeNode]{}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("tree json: %v (%s)", err, out)
	}
	var sawDeep bool
	for _, n := range page.Items {
		if n.Path == "a/b/deep.txt" {
			sawDeep = true
		}
	}
	if sawDeep {
		t.Errorf("tree --depth=1 unexpectedly included a/b/deep.txt: %+v", page.Items)
	}
}

func TestE2E_Tree(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "site"); code != exitOK {
		t.Fatalf("create collection: code=%d err=%s", code, errOut)
	}
	local := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(local, []byte("<h1>hi</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run("put", "site/docs/index.html", local); code != exitOK {
		t.Fatalf("put: code=%d err=%s", code, errOut)
	}

	code, out, errOut := e.run("--output", "json", "tree", "site")
	if code != exitOK {
		t.Fatalf("tree: code=%d err=%s", code, errOut)
	}
	var page apiclient.Page[apiclient.TreeNode]
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("tree json: %v (%s)", err, out)
	}
	var sawFile bool
	for _, n := range page.Items {
		if n.Path == "docs/index.html" && n.Kind == "file" {
			sawFile = true
		}
	}
	if !sawFile {
		t.Errorf("tree items = %+v, want docs/index.html", page.Items)
	}
}

func TestE2E_TokenLifecycle(t *testing.T) {
	e := newE2E(t)
	code, out, errOut := e.run("--output", "json", "token", "create", "ci", "--scope", "collections:read")
	if code != exitOK {
		t.Fatalf("token create: code=%d err=%s", code, errOut)
	}
	var resp apiclient.CreateTokenResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("token json: %v (%s)", err, out)
	}
	if resp.Secret == "" || resp.Name != "ci" {
		t.Fatalf("token resp = %+v", resp)
	}

	code, out, errOut = e.run("--output", "json", "token", "list")
	if code != exitOK {
		t.Fatalf("token list: code=%d err=%s", code, errOut)
	}
	var page apiclient.Page[apiclient.Token]
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("token list json: %v (%s)", err, out)
	}
	var found bool
	for _, tok := range page.Items {
		if tok.ID == resp.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("token list = %+v, missing id %d", page.Items, resp.ID)
	}

	if code, _, errOut := e.run("token", "revoke", "999999"); code == exitOK {
		t.Errorf("revoke nonexistent token unexpectedly succeeded: %s", errOut)
	}

	// Revoking the token actually created above succeeds; it stays listed
	// (revoke is a soft mark, not a delete) but now carries a revoked_at.
	if code, _, errOut := e.run("token", "revoke", strconv.FormatInt(resp.ID, 10)); code != exitOK {
		t.Fatalf("revoke: code=%d err=%s", code, errOut)
	}
	code, out, errOut = e.run("--output", "json", "token", "list")
	if code != exitOK {
		t.Fatalf("token list after revoke: code=%d err=%s", code, errOut)
	}
	page = apiclient.Page[apiclient.Token]{}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("token list json: %v (%s)", err, out)
	}
	var revoked bool
	for _, tok := range page.Items {
		if tok.ID == resp.ID && tok.RevokedAt != nil {
			revoked = true
		}
	}
	if !revoked {
		t.Errorf("token %d not marked revoked: %+v", resp.ID, page.Items)
	}
}

func TestE2E_TokenCreate_MultipleScopes(t *testing.T) {
	e := newE2E(t)
	code, out, errOut := e.run("--output", "json", "token", "create", "multi",
		"--scope", "collections:read", "--scope", "shares:write")
	if code != exitOK {
		t.Fatalf("token create: code=%d err=%s", code, errOut)
	}
	var resp apiclient.CreateTokenResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("token json: %v (%s)", err, out)
	}
	want := map[string]bool{"collections:read": true, "shares:write": true}
	if len(resp.Scopes) != 2 {
		t.Fatalf("scopes = %v, want 2 entries", resp.Scopes)
	}
	for _, s := range resp.Scopes {
		if !want[s] {
			t.Errorf("unexpected scope %q in %v", s, resp.Scopes)
		}
	}
}

func TestE2E_MaintenanceCleanup(t *testing.T) {
	e := newE2E(t)
	code, out, errOut := e.run("--output", "json", "maintenance", "cleanup")
	if code != exitOK {
		t.Fatalf("cleanup: code=%d err=%s", code, errOut)
	}
	var result struct {
		Cleanup map[string]any `json:"cleanup"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("cleanup json: %v (%s)", err, out)
	}
	if _, ok := result.Cleanup["nodes_deleted"]; !ok {
		t.Errorf("cleanup result = %+v, want a nodes_deleted field", result.Cleanup)
	}
}

func TestE2E_MaintenanceCleanup_TextOutput(t *testing.T) {
	e := newE2E(t)
	code, out, errOut := e.run("maintenance", "cleanup")
	if code != exitOK {
		t.Fatalf("cleanup: code=%d err=%s", code, errOut)
	}
	if !strings.HasPrefix(out, "cleanup: ") {
		t.Errorf("text output = %q, want a \"cleanup: \" prefix", out)
	}
}
