package uploadrequest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/store"
)

func anonGet(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestVisibility_DefaultPrivate(t *testing.T) {
	ts := newDemo(t) // collection inherits the server default: public
	id := createLink(t, ts, `{"collection":"demo","target_folder":"in"}`)
	ur := mustAPI(t, ts, http.MethodGet, "/api/v1/upload-requests/"+id, "")
	if !strings.Contains(string(ur), `"visibility":"private"`) {
		t.Errorf("stored file_metadata lacks private visibility: %s", ur)
	}
	code, b := upload(t, ts, id, part{"a.txt", "a"})
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	if strings.Contains(string(b), `"url"`) {
		t.Errorf("response has a url for a private file: %s", b)
	}
	if c := anonGet(t, ts.BaseURL+"/c/demo/in/a.txt"); c != http.StatusNotFound {
		t.Errorf("anonymous GET = %d, want 404", c)
	}
	meta := mustAPI(t, ts, http.MethodGet, "/api/v1/collections/demo/files/in/a.txt?meta", "")
	if !strings.Contains(string(meta), `"private"`) {
		t.Errorf("own metadata: %s", meta)
	}
}

func TestVisibility_UnlistedWithUploaderAccess(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"in","allow_uploader_access":true,
		"file_metadata":{"access":{"visibility":"unlisted"}}}`)
	code, b := upload(t, ts, id, part{"a.txt", "a"})
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	var res struct {
		Files []struct {
			URL *string `json:"url"`
		} `json:"files"`
	}
	if err := json.Unmarshal(b, &res); err != nil || len(res.Files) != 1 || res.Files[0].URL == nil {
		t.Fatalf("expected a url: %s (%v)", b, err)
	}
	if c := anonGet(t, *res.Files[0].URL); c != http.StatusOK {
		t.Errorf("GET returned url = %d, want 200", c)
	}
}

func TestVisibility_NoURLUnderPrivateAncestor(t *testing.T) {
	ts := newDemo(t)
	mustAPI(t, ts, http.MethodPatch, "/api/v1/collections/demo/folders/in?meta", `{"access":{"visibility":"private"}}`)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"in","allow_uploader_access":true,
		"file_metadata":{"access":{"visibility":"public"}}}`)
	code, b := upload(t, ts, id, part{"a.txt", "a"})
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	if strings.Contains(string(b), `"url"`) {
		t.Errorf("url returned for a file under a private folder: %s", b)
	}
}

func TestVisibility_UploaderAccessRequiresPublicOrUnlisted(t *testing.T) {
	ts := newDemo(t)
	for _, body := range []string{
		`{"collection":"demo","allow_uploader_access":true}`,
		`{"collection":"demo","allow_uploader_access":true,"file_metadata":{"access":{"visibility":"protected"}}}`,
	} {
		code, b := apiDo(t, ts, http.MethodPost, "/api/v1/upload-requests", body)
		if code != http.StatusBadRequest || !strings.Contains(string(b), "allow_uploader_access requires") {
			t.Errorf("create %s: %d %s, want 400", body, code, b)
		}
	}
}

func TestVisibility_LegacyLinkWithoutVisibility(t *testing.T) {
	ts := newDemo(t)
	ctx := context.Background()
	c, err := ts.Store.GetCollectionByName(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	// A link created before this change: stored file_metadata has no visibility.
	ur := &store.UploadRequest{
		RequestID: "urq_legacy0123456789ab", CollectionID: c.ID, TargetFolder: "old",
		FileMetadata: []byte(`{}`), AllowUploaderAccess: true,
	}
	if err := ts.Store.CreateUploadRequest(ctx, ur); err != nil {
		t.Fatal(err)
	}
	code, b := upload(t, ts, ur.RequestID, part{"a.txt", "a"})
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	if strings.Contains(string(b), `"url"`) {
		t.Errorf("legacy link returned a url: %s", b)
	}
	if c := anonGet(t, ts.BaseURL+"/c/demo/old/a.txt"); c != http.StatusNotFound {
		t.Errorf("anonymous GET = %d, want 404", c)
	}
}

// TestUpload_SandboxWarning: a file uploaded into an admin-unsandboxed folder
// is reported as served sandboxed until approved (artifactsd-7tl.9).
func TestUpload_SandboxWarning(t *testing.T) {
	ts := newDemo(t)
	mustAPI(t, ts, http.MethodPatch, "/api/v1/collections/demo/folders/in?meta", `{"content":{"sandbox":false}}`)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"in"}`)
	code, b := upload(t, ts, id, part{"a.html", "<p>a</p>"})
	if code != http.StatusOK || !strings.Contains(string(b), `"warning":"served sandboxed until an admin re-approves it`) {
		t.Errorf("upload into unsandboxed folder: %d %s, want a warning", code, b)
	}
	other := createLink(t, ts, `{"collection":"demo","target_folder":"plain"}`)
	if _, b := upload(t, ts, other, part{"b.html", "<p>b</p>"}); strings.Contains(string(b), "warning") {
		t.Errorf("plain upload has a warning: %s", b)
	}
}
