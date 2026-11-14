package uploadrequest_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
)

// putAPI uploads a file through the management API (a token-authenticated
// writer), optionally with a metadata header.
func putAPI(t *testing.T, ts *testserver.Server, p, body, metaJSON string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		ts.BaseURL+"/api/v1/collections/demo/files/"+p, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.AdminToken)
	if metaJSON != "" {
		req.Header.Set("Artifactsd-Metadata", base64.RawURLEncoding.EncodeToString([]byte(metaJSON)))
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s: %d", p, resp.StatusCode)
	}
}

func TestOverwrite_DefaultLinkRefusesExistingFile(t *testing.T) {
	ts := newDemo(t)
	putAPI(t, ts, "report.html", "<h1>official</h1>", `{"access":{"visibility":"private"}}`)
	id := createLink(t, ts, `{"collection":"demo","target_folder":""}`)

	code, b := upload(t, ts, id, part{"report.html", "<h1>defaced</h1>"})
	if code != http.StatusConflict || !strings.Contains(string(b), "report.html already exists") {
		t.Fatalf("upload over API file: %d %s, want 409", code, b)
	}
	if got, _ := fileContent(t, ts, "demo", "report.html"); got != "<h1>official</h1>" {
		t.Errorf("content = %q, want original", got)
	}
	meta := mustAPI(t, ts, http.MethodGet, "/api/v1/collections/demo/files/report.html/meta", "")
	if !strings.Contains(string(meta), `"private"`) {
		t.Errorf("metadata changed: %s", meta)
	}
}

func TestOverwrite_DefaultLinkRefusesItsOwnFileTwice(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":""}`)
	if code, b := upload(t, ts, id, part{"a.txt", "1"}); code != http.StatusOK {
		t.Fatalf("first: %d %s", code, b)
	}
	if code, b := upload(t, ts, id, part{"a.txt", "2"}); code != http.StatusConflict {
		t.Fatalf("second upload of the same name: %d %s, want 409", code, b)
	}
	if got, _ := fileContent(t, ts, "demo", "a.txt"); got != "1" {
		t.Errorf("content = %q, want 1", got)
	}
}

func TestOverwrite_AllowOverwriteOnlyOwnFiles(t *testing.T) {
	ts := newDemo(t)
	putAPI(t, ts, "api.txt", "api", "")
	other := createLink(t, ts, `{"collection":"demo","target_folder":"","allow_overwrite":true}`)
	if code, b := upload(t, ts, other, part{"other.txt", "o"}); code != http.StatusOK {
		t.Fatalf("other link: %d %s", code, b)
	}

	id := createLink(t, ts, `{"collection":"demo","target_folder":"","allow_overwrite":true,"max_files":1}`)
	if code, b := upload(t, ts, id, part{"mine.txt", "v1"}); code != http.StatusOK {
		t.Fatalf("first: %d %s", code, b)
	}
	// Replacing its own file works and does not count toward max_files=1.
	if code, b := upload(t, ts, id, part{"mine.txt", "v2"}); code != http.StatusOK {
		t.Fatalf("overwrite own file: %d %s, want 200", code, b)
	}
	if got, _ := fileContent(t, ts, "demo", "mine.txt"); got != "v2" {
		t.Errorf("mine.txt = %q, want v2", got)
	}
	if code, b := upload(t, ts, id, part{"api.txt", "x"}); code != http.StatusConflict {
		t.Errorf("overwrite API file: %d %s, want 409", code, b)
	}
	if code, b := upload(t, ts, id, part{"other.txt", "x"}); code != http.StatusConflict {
		t.Errorf("overwrite another link's file: %d %s, want 409", code, b)
	}
	for p, want := range map[string]string{"api.txt": "api", "other.txt": "o"} {
		if got, _ := fileContent(t, ts, "demo", p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
}

func TestOverwrite_APIPutClearsProvenance(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"","allow_overwrite":true}`)
	if code, b := upload(t, ts, id, part{"f.txt", "link"}); code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	putAPI(t, ts, "f.txt", "api", "")
	if code, b := upload(t, ts, id, part{"f.txt", "link again"}); code != http.StatusConflict {
		t.Fatalf("link overwriting a file the API replaced: %d %s, want 409", code, b)
	}
	if got, _ := fileContent(t, ts, "demo", "f.txt"); got != "api" {
		t.Errorf("f.txt = %q, want api", got)
	}
}

func TestOverwrite_SubmissionFoldersUnaffected(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"in","use_submission_folders":true}`)
	for range 2 {
		if code, b := upload(t, ts, id, part{"same.txt", "x"}); code != http.StatusOK {
			t.Fatalf("upload: %d %s", code, b)
		}
	}
}
