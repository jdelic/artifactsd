package uploadrequest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
)

// part is one file of a multipart upload.
type part struct {
	name    string
	content string
}

// apiDo performs an authenticated JSON API call and returns status and body.
func apiDo(t *testing.T, ts *testserver.Server, method, path, body string) (int, []byte) {
	t.Helper()
	var rdr io.Reader = http.NoBody
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, ts.BaseURL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.AdminToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// mustAPI is apiDo that fails the test unless the status is 2xx.
func mustAPI(t *testing.T, ts *testserver.Server, method, path, body string) []byte {
	t.Helper()
	code, b := apiDo(t, ts, method, path, body)
	if code < 200 || code > 299 {
		t.Fatalf("%s %s: status %d: %s", method, path, code, b)
	}
	return b
}

// createCollection creates a collection with the given name.
func createCollection(t *testing.T, ts *testserver.Server, name string) {
	t.Helper()
	mustAPI(t, ts, http.MethodPost, "/api/v1/collections", `{"name":"`+name+`"}`)
}

// createLink creates an upload request from a JSON body and returns its id.
func createLink(t *testing.T, ts *testserver.Server, body string) string {
	t.Helper()
	b := mustAPI(t, ts, http.MethodPost, "/api/v1/upload-requests", body)
	var v struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(b, &v); err != nil || v.RequestID == "" {
		t.Fatalf("create upload request: %s", b)
	}
	return v.RequestID
}

// uploadReq builds a multipart POST to /r/{id}.
func uploadReq(t *testing.T, ts *testserver.Server, id string, parts ...part) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		fw, err := mw.CreateFormFile("file", p.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte(p.content))
	}
	_ = mw.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.BaseURL+"/r/"+id, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// do sends req and returns status and body.
func do(t *testing.T, ts *testserver.Server, req *http.Request) (int, []byte) {
	t.Helper()
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// upload POSTs parts to /r/{id} and returns status and body.
func upload(t *testing.T, ts *testserver.Server, id string, parts ...part) (int, []byte) {
	t.Helper()
	return do(t, ts, uploadReq(t, ts, id, parts...))
}

// fileContent fetches a file through the management API (bypassing serving
// policy); ok is false when the file does not exist.
func fileContent(t *testing.T, ts *testserver.Server, collection, path string) (content string, ok bool) {
	t.Helper()
	code, b := apiDo(t, ts, http.MethodGet, "/api/v1/collections/"+collection+"/files/"+path, "")
	switch code {
	case http.StatusOK:
		return string(b), true
	case http.StatusNotFound:
		return "", false
	default:
		t.Fatalf("GET file %s/%s: status %d: %s", collection, path, code, b)
		return "", false
	}
}

// assertAbsent fails the test for every path that exists in collection.
func assertAbsent(t *testing.T, ts *testserver.Server, collection string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, ok := fileContent(t, ts, collection, p); ok {
			t.Errorf("%s/%s exists, want absent", collection, p)
		}
	}
}
