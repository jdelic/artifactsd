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

// TestContentTrust: sandbox=false only ever applies to content an
// admin-scoped principal wrote. Replacing such content, or adding content
// under an unsandboxed folder, with a non-admin token keeps it sandboxed
// (artifactsd-7tl.2).
func TestContentTrust(t *testing.T) {
	ts := testserver.New(t)
	writer := mint(t, ts, `["collections:write"]`)
	jsonHdr := map[string]string{"Content-Type": "application/json"}
	html := map[string]string{"Content-Type": "text/html"}
	call(t, ts, http.MethodPost, "/api/v1/collections", ts.AdminToken, `{"name":"demo"}`, jsonHdr)

	// Admin publishes trusted, unsandboxed content.
	hdr := metaHdr(`{"content":{"sandbox":false}}`)
	hdr["Content-Type"] = "text/html"
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/app.html", ts.AdminToken, "<p>admin</p>", hdr)
	if sandboxed(t, ts, "/c/demo/app.html") {
		t.Fatal("admin content with sandbox=false is sandboxed")
	}

	// A writer's unrelated metadata patch keeps it unsandboxed.
	if r := call(t, ts, http.MethodPatch, "/api/v1/collections/demo/files/app.html?meta", writer,
		`{"crawl":{"indexable":false}}`, jsonHdr); r.StatusCode != http.StatusOK {
		t.Fatalf("writer patch = %d", r.StatusCode)
	}
	if sandboxed(t, ts, "/c/demo/app.html") {
		t.Error("unrelated metadata patch re-sandboxed admin content")
	}

	// A writer replacing the bytes (metadata kept) gets the sandbox back.
	if r := call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/app.html", writer, "<script>evil</script>", html); r.StatusCode != http.StatusOK {
		t.Fatalf("writer replace = %d", r.StatusCode)
	}
	if !sandboxed(t, ts, "/c/demo/app.html") {
		t.Error("non-admin replacement of unsandboxed content is served without the sandbox")
	}
	// The admin re-publishing restores trust.
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/app.html", ts.AdminToken, "<p>admin again</p>", html)
	if sandboxed(t, ts, "/c/demo/app.html") {
		t.Error("admin re-upload did not restore trust")
	}

	// Inherited sandbox=false: new content from a writer is still sandboxed.
	call(t, ts, http.MethodPatch, "/api/v1/collections/demo/folders/spa?meta", ts.AdminToken, `{"content":{"sandbox":false}}`, jsonHdr)
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/spa/index.html", writer, "<p>w</p>", html)
	if !sandboxed(t, ts, "/c/demo/spa/index.html") {
		t.Error("writer content under an unsandboxed folder is served without the sandbox")
	}

	// A share override with sandbox=false (admin-only) still can't unsandbox
	// writer content.
	var sh struct {
		ShareID string `json:"share_id"`
	}
	{
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.BaseURL+"/api/v1/collections/demo/shares",
			strings.NewReader(`{"target_path":"spa","metadata":{"content":{"sandbox":false}}}`))
		req.Header.Set("Authorization", "Bearer "+ts.AdminToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(resp.Body).Decode(&sh)
		resp.Body.Close()
	}
	if sh.ShareID == "" || !sandboxed(t, ts, "/s/"+sh.ShareID+"/index.html") {
		t.Error("share override unsandboxed writer content")
	}

	// Large uploads: trust follows the principal that began the session.
	begin := func(token, path string) {
		r, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			ts.BaseURL+"/api/v1/collections/demo/uploads", strings.NewReader(`{"path":"`+path+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		resp, err := ts.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		var sess struct {
			UploadURL string `json:"upload_url"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&sess)
		resp.Body.Close()
		put, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, sess.UploadURL, strings.NewReader("<p>big</p>"))
		put.Header.Set("Content-Type", "text/html")
		pr, err := ts.Client().Do(put)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, pr.Body)
		pr.Body.Close()
	}
	begin(writer, "spa/big.html")
	if !sandboxed(t, ts, "/c/demo/spa/big.html") {
		t.Error("writer large upload under an unsandboxed folder is served without the sandbox")
	}
	begin(ts.AdminToken, "spa/admin-big.html")
	if sandboxed(t, ts, "/c/demo/spa/admin-big.html") {
		t.Error("admin large upload under an unsandboxed folder is sandboxed")
	}
}
