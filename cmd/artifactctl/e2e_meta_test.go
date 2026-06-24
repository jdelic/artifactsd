package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
