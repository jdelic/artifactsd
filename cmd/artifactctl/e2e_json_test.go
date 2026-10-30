package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// decodeOneJSON fails unless out is exactly one JSON object (whitespace
// aside), returning it decoded.
func decodeOneJSON(t *testing.T, what, out string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s: stdout is not a JSON object: %v\n%s", what, err, out)
	}
	if dec.More() {
		t.Fatalf("%s: stdout has data after the JSON object:\n%s", what, out)
	}
	return v
}

// TestE2E_JSONOutput_OnlyJSON: with --output json every command writes one
// JSON document to stdout — a generated bearer token travels inside it, and
// commands whose API call has no body still emit an object.
func TestE2E_JSONOutput_OnlyJSON(t *testing.T) {
	e := newE2E(t)
	dir := t.TempDir()
	local := filepath.Join(dir, "a.txt")
	mustWrite(t, local, "hello")
	tree := filepath.Join(dir, "tree")
	mustWrite(t, filepath.Join(tree, "x.txt"), "x")
	mustWrite(t, filepath.Join(tree, "sub", "y.txt"), "y")

	jsonRun := func(args ...string) map[string]any {
		t.Helper()
		code, out, errOut := e.run(append([]string{"--output", "json"}, args...)...)
		if code != exitOK {
			t.Fatalf("%v: code=%d err=%s", args, code, errOut)
		}
		return decodeOneJSON(t, args[0], out)
	}
	wantToken := func(what string, v map[string]any) {
		t.Helper()
		if tok, _ := v["generated_bearer_token"].(string); tok == "" {
			t.Fatalf("%s: no generated_bearer_token in %v", what, v)
		}
	}

	v := jsonRun("collection", "create", "demo", "--protected", "--generate-bearer-token")
	wantToken("collection create", v)
	if v["name"] != "demo" {
		t.Fatalf("collection create: name = %v", v["name"])
	}
	wantToken("put", jsonRun("put", "demo/a.txt", local, "--protected", "--generate-bearer-token"))
	v = jsonRun("-v", "put-tree", "demo", tree, "--protected", "--generate-bearer-token")
	wantToken("put-tree", v)
	if v["uploaded"] != float64(2) {
		t.Fatalf("put-tree: uploaded = %v", v["uploaded"])
	}
	wantToken("meta set file", jsonRun("meta", "set", "demo/a.txt", "--generate-bearer-token"))
	wantToken("meta set folder", jsonRun("meta", "set", "demo/sub/", "--generate-bearer-token"))
	wantToken("meta set collection", jsonRun("meta", "set", "demo", "--generate-bearer-token"))
	if _, ok := jsonRun("meta", "set", "demo/a.txt", "--public")["generated_bearer_token"]; ok {
		t.Fatal("meta set without --generate-bearer-token reported a token")
	}

	outFile := filepath.Join(dir, "got.txt")
	if v := jsonRun("get", "demo/a.txt", "-o", outFile); v["wrote"] != outFile || v["bytes"] != float64(5) {
		t.Fatalf("get -o: %v", v)
	}
	if b, err := os.ReadFile(outFile); err != nil || string(b) != "hello" {
		t.Fatalf("get -o wrote %q, %v", b, err)
	}

	sh := jsonRun("share", "create", "demo/a.txt")
	if v := jsonRun("share", "revoke", sh["share_id"].(string)); v["revoked"] != sh["share_id"] {
		t.Fatalf("share revoke: %v", v)
	}

	ur := jsonRun("upload-request", "create", "demo")
	id := ur["request_id"].(string)
	if v := jsonRun("upload-request", "close", id); v["status"] != "closed" {
		t.Fatalf("upload-request close: %v", v)
	}
	if v := jsonRun("upload-request", "revoke", id); v["revoked"] != id {
		t.Fatalf("upload-request revoke: %v", v)
	}

	tok := jsonRun("token", "create", "ci", "--scope", "collections:read")
	tokID := strconv.FormatFloat(tok["id"].(float64), 'f', 0, 64)
	if v := jsonRun("token", "revoke", tokID); v["revoked"] != tok["id"] {
		t.Fatalf("token revoke: %v", v)
	}

	if v := jsonRun("rm", "demo/a.txt"); v["deleted"] != "demo/a.txt" || v["recursive"] != false {
		t.Fatalf("rm: %v", v)
	}
	if v := jsonRun("rm", "-r", "demo/sub"); v["deleted"] != "demo/sub" || v["recursive"] != true {
		t.Fatalf("rm -r: %v", v)
	}
	if v := jsonRun("collection", "delete", "demo", "--force"); v["deleted"] != "demo" {
		t.Fatalf("collection delete: %v", v)
	}
}

// TestRun_JSONOutput_LocalCommands: the commands that need no server also
// emit a single JSON object under --output json.
func TestRun_JSONOutput_LocalCommands(t *testing.T) {
	code, out, errOut := runCLI(t, "--output", "json", "version")
	if code != exitOK {
		t.Fatalf("version: code=%d err=%s", code, errOut)
	}
	if v := decodeOneJSON(t, "version", out); v["name"] != "artifactctl" || v["version"] == "" {
		t.Fatalf("version: %v", v)
	}

	chdir(t, t.TempDir())
	home := t.TempDir()
	code, out, errOut = runCLIHome(t, home, "--output", "json", "skill", "install", "hermes")
	if code != exitOK {
		t.Fatalf("skill install: code=%d err=%s", code, errOut)
	}
	v := decodeOneJSON(t, "skill install", out)
	want := filepath.Join(home, ".hermes", "skills", "artifactsd", "SKILL.md")
	if v["agent"] != "hermes" || v["mode"] != "copy" || v["path"] != want || v["user_wide_fallback"] != true {
		t.Fatalf("skill install: %v", v)
	}
}
