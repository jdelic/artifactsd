package serve_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
)

const sandboxCSP = "sandbox allow-scripts allow-downloads"

func sandboxed(t *testing.T, ts *testserver.Server, path string) bool {
	t.Helper()
	r := call(t, ts, http.MethodGet, path, "", "", nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, r.StatusCode)
	}
	for _, v := range r.Header.Values("Content-Security-Policy") {
		if v == sandboxCSP {
			return true
		}
	}
	return false
}

func mint(t *testing.T, ts *testserver.Server, scopes string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.BaseURL+"/api/v1/tokens",
		strings.NewReader(`{"name":"w","scopes":`+scopes+`}`))
	req.Header.Set("Authorization", "Bearer "+ts.AdminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return v.Token
}

func jsonCall(t *testing.T, ts *testserver.Server, method, path, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, ts.BaseURL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestContentTrust covers the sandbox model (artifactsd-7tl.2):
//   - only admins set content.sandbox, in either direction;
//   - an admin setting sandbox=false on a file, folder, collection or share
//     unsandboxes the content currently under it, without a re-upload;
//   - content a non-admin writes or replaces afterwards is sandboxed until an
//     admin confirms it again with a metadata change.
func TestContentTrust(t *testing.T) {
	ts := testserver.New(t)
	writer := mint(t, ts, `["collections:write"]`)
	sharer := mint(t, ts, `["shares:write"]`)
	html := map[string]string{"Content-Type": "text/html"}
	jsonCall(t, ts, http.MethodPost, "/api/v1/collections", ts.AdminToken, `{"name":"demo"}`)

	// Users can't change the sandbox in either direction, anywhere.
	for _, c := range []struct{ token, method, path, body string }{
		{writer, http.MethodPost, "/api/v1/collections", `{"name":"x","metadata":{"content":{"sandbox":true}}}`},
		{writer, http.MethodPatch, "/api/v1/collections/demo/meta", `{"content":{"sandbox":true}}`},
		{writer, http.MethodPatch, "/api/v1/collections/demo/folders/spa?meta", `{"content":{"sandbox":false}}`},
		{writer, http.MethodPost, "/api/v1/collections/demo/uploads", `{"path":"z.html","metadata":{"content":{"sandbox":true}}}`},
		{sharer, http.MethodPost, "/api/v1/collections/demo/shares", `{"target_path":"","metadata":{"content":{"sandbox":true}}}`},
	} {
		if code, body := jsonCall(t, ts, c.method, c.path, c.token, c.body); code != http.StatusForbidden {
			t.Errorf("non-admin %s %s = %d %s, want 403", c.method, c.path, code, body)
		}
	}
	hdr := metaHdr(`{"content":{"sandbox":true}}`)
	if r := call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/x.html", writer, "x", hdr); r.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin PUT with sandbox=true = %d, want 403", r.StatusCode)
	}

	// A user's file, unsandboxed by an admin without re-uploading it.
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/user.html", writer, "<p>user</p>", html)
	if !sandboxed(t, ts, "/c/demo/user.html") {
		t.Fatal("user content not sandboxed by default")
	}
	if code, _ := jsonCall(t, ts, http.MethodPatch, "/api/v1/collections/demo/files/user.html?meta", ts.AdminToken, `{"content":{"sandbox":false}}`); code != http.StatusOK {
		t.Fatalf("admin patch = %d", code)
	}
	if sandboxed(t, ts, "/c/demo/user.html") {
		t.Error("admin sandbox=false on a user's file did not take effect")
	}
	// An unrelated user patch keeps it unsandboxed.
	jsonCall(t, ts, http.MethodPatch, "/api/v1/collections/demo/files/user.html?meta", writer, `{"crawl":{"indexable":false}}`)
	if sandboxed(t, ts, "/c/demo/user.html") {
		t.Error("unrelated user patch re-sandboxed the file")
	}
	// The user replacing the bytes gets the sandbox back; the admin's setting
	// is kept, even when the user sends a metadata document without it.
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/user.html", writer, "<script>new</script>",
		map[string]string{"Content-Type": "text/html", "Artifactsd-Metadata": metaHdr(`{"crawl":{"indexable":false}}`)["Artifactsd-Metadata"]})
	if !sandboxed(t, ts, "/c/demo/user.html") {
		t.Error("user replacement of unsandboxed content is served without the sandbox")
	}
	if _, own := jsonCall(t, ts, http.MethodGet, "/api/v1/collections/demo/files/user.html?meta", ts.AdminToken, ""); !strings.Contains(own, `"sandbox":false`) {
		t.Errorf("user metadata replacement dropped the admin's sandbox setting: %s", own)
	}
	// The admin re-confirms with a metadata change; no upload needed.
	jsonCall(t, ts, http.MethodPatch, "/api/v1/collections/demo/files/user.html?meta", ts.AdminToken, `{"content":{"sandbox":false}}`)
	if sandboxed(t, ts, "/c/demo/user.html") {
		t.Error("admin re-confirmation did not take effect")
	}

	// Folder level: existing user content is unsandboxed, later uploads are not.
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/spa/index.html", writer, "<p>a</p>", html)
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/spa/deep/b.html", writer, "<p>b</p>", html)
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/spa-other/c.html", writer, "<p>c</p>", html)
	jsonCall(t, ts, http.MethodPatch, "/api/v1/collections/demo/folders/spa?meta", ts.AdminToken, `{"content":{"sandbox":false}}`)
	if sandboxed(t, ts, "/c/demo/spa/index.html") || sandboxed(t, ts, "/c/demo/spa/deep/b.html") {
		t.Error("admin folder sandbox=false did not unsandbox the files below it")
	}
	if !sandboxed(t, ts, "/c/demo/spa-other/c.html") {
		t.Error("a sibling folder with a common name prefix was affected")
	}
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/spa/later.html", writer, "<p>later</p>", html)
	if !sandboxed(t, ts, "/c/demo/spa/later.html") {
		t.Error("user content added after the admin's decision is served without the sandbox")
	}

	// A share override: an admin share with sandbox=false vouches for its target.
	code, body := jsonCall(t, ts, http.MethodPost, "/api/v1/collections/demo/shares", ts.AdminToken,
		`{"target_path":"spa-other","metadata":{"content":{"sandbox":false}}}`)
	var sh struct {
		ShareID string `json:"share_id"`
	}
	_ = json.Unmarshal([]byte(body), &sh)
	if code != http.StatusCreated || sandboxed(t, ts, "/s/"+sh.ShareID+"/c.html") {
		t.Errorf("admin share sandbox=false: %d, content still sandboxed", code)
	}

	// Collection level.
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/root.html", writer, "<p>r</p>", html)
	jsonCall(t, ts, http.MethodPatch, "/api/v1/collections/demo/meta", ts.AdminToken, `{"content":{"sandbox":false}}`)
	if sandboxed(t, ts, "/c/demo/root.html") || sandboxed(t, ts, "/c/demo/spa/later.html") {
		t.Error("admin collection sandbox=false did not unsandbox the collection's files")
	}

	// Large uploads: trust follows the principal that began the session.
	begin := func(token, path string) {
		_, b := jsonCall(t, ts, http.MethodPost, "/api/v1/collections/demo/uploads", token, `{"path":"`+path+`"}`)
		var sess struct {
			UploadURL string `json:"upload_url"`
		}
		_ = json.Unmarshal([]byte(b), &sess)
		put, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, sess.UploadURL, strings.NewReader("<p>big</p>"))
		put.Header.Set("Content-Type", "text/html")
		pr, err := ts.Client().Do(put)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, pr.Body)
		pr.Body.Close()
	}
	begin(writer, "big.html")
	if !sandboxed(t, ts, "/c/demo/big.html") {
		t.Error("user large upload in an unsandboxed collection is served without the sandbox")
	}
	begin(ts.AdminToken, "admin-big.html")
	if sandboxed(t, ts, "/c/demo/admin-big.html") {
		t.Error("admin large upload in an unsandboxed collection is sandboxed")
	}
}
