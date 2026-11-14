package uploadrequest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
	"github.com/jdelic/artifactsd/internal/uploadrequest"
)

func newDemo(t *testing.T) *testserver.Server {
	t.Helper()
	ts := testserver.New(t)
	createCollection(t, ts, "demo")
	return ts
}

func TestUpload_MaxFilesRejectsWholeRequest(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"","max_files":1}`)
	code, b := upload(t, ts, id, part{"f1.txt", "1"}, part{"f2.txt", "2"}, part{"f3.txt", "3"})
	if code != http.StatusConflict {
		t.Fatalf("3 files on max_files=1: status %d (%s), want 409", code, b)
	}
	assertAbsent(t, ts, "demo", "f1.txt", "f2.txt", "f3.txt")
}

func TestUpload_MaxFilesCountsEarlierSubmissions(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"","max_files":2}`)
	if code, b := upload(t, ts, id, part{"a.txt", "a"}); code != http.StatusOK {
		t.Fatalf("first upload: %d %s", code, b)
	}
	if code, b := upload(t, ts, id, part{"b.txt", "b"}, part{"c.txt", "c"}); code != http.StatusConflict {
		t.Fatalf("2 more files with 1 left: status %d (%s), want 409", code, b)
	}
	assertAbsent(t, ts, "demo", "b.txt", "c.txt")
	if _, ok := fileContent(t, ts, "demo", "a.txt"); !ok {
		t.Error("a.txt from the accepted submission is missing")
	}
}

func TestUpload_SingleUseConcurrent(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"","single_use":true}`)
	const n = 10
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = upload(t, ts, id, part{fmt.Sprintf("r%d.txt", i), "x"})
		}()
	}
	wg.Wait()
	winners := 0
	for i, c := range codes {
		switch c {
		case http.StatusOK:
			winners++
			if _, ok := fileContent(t, ts, "demo", fmt.Sprintf("r%d.txt", i)); !ok {
				t.Errorf("winner r%d.txt missing", i)
			}
		case http.StatusGone, http.StatusConflict:
			assertAbsent(t, ts, "demo", fmt.Sprintf("r%d.txt", i))
		default:
			t.Errorf("request %d: unexpected status %d", i, c)
		}
	}
	if winners != 1 {
		t.Errorf("%d requests succeeded on a single-use link, want exactly 1 (codes %v)", winners, codes)
	}
}

func TestUpload_RevokedBeforeCommit(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":""}`)
	restore := uploadrequest.SetBeforeCommitHook(func() {
		mustAPI(t, ts, http.MethodDelete, "/api/v1/upload-requests/"+id, "")
	})
	defer restore()
	if code, b := upload(t, ts, id, part{"late.txt", "x"}); code != http.StatusGone {
		t.Fatalf("upload to link revoked mid-request: status %d (%s), want 410", code, b)
	}
	assertAbsent(t, ts, "demo", "late.txt")
}

func TestUpload_OversizeSecondFileDropsFirst(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"","max_file_size":10}`)
	code, b := upload(t, ts, id, part{"small.txt", "ok"}, part{"big.txt", strings.Repeat("x", 100)})
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize file: status %d (%s), want 413", code, b)
	}
	assertAbsent(t, ts, "demo", "small.txt", "big.txt")
}

func TestUpload_DuplicateTargetPathRejected(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":""}`)
	code, b := upload(t, ts, id, part{"same.txt", "1"}, part{"dir/same.txt", "2"})
	if code != http.StatusBadRequest {
		t.Fatalf("duplicate target path: status %d (%s), want 400", code, b)
	}
	assertAbsent(t, ts, "demo", "same.txt")
}

func TestUpload_HappyPathMultiAndRaw(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","target_folder":"inbox/x"}`)
	code, b := upload(t, ts, id, part{"a.txt", "aaa"}, part{"b.txt", "bb"})
	if code != http.StatusOK {
		t.Fatalf("multi upload: %d %s", code, b)
	}
	var res struct {
		Files []struct {
			Filename, Path string
			Size           int64
		}
	}
	if err := json.Unmarshal(b, &res); err != nil || len(res.Files) != 2 || res.Files[1].Path != "inbox/x/b.txt" {
		t.Fatalf("response %s (%v)", b, err)
	}
	if got, _ := fileContent(t, ts, "demo", "inbox/x/a.txt"); got != "aaa" {
		t.Errorf("a.txt = %q", got)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.BaseURL+"/r/"+id, strings.NewReader("raw!"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Disposition", `attachment; filename="raw.bin"`)
	if code, b := do(t, ts, req); code != http.StatusOK {
		t.Fatalf("raw upload: %d %s", code, b)
	}
	if got, _ := fileContent(t, ts, "demo", "inbox/x/raw.bin"); got != "raw!" {
		t.Errorf("raw.bin = %q", got)
	}

	ctx := context.Background()
	ur, err := ts.Store.GetUploadRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if ur.FilesUploaded != 3 {
		t.Errorf("files_uploaded = %d, want 3", ur.FilesUploaded)
	}
	subs, err := ts.Store.ListUploadRequestSubmissions(ctx, ur.ID)
	if err != nil || len(subs) != 2 {
		t.Fatalf("submissions: %v (%d)", err, len(subs))
	}
	var files, bytes int64
	for _, s := range subs {
		files += s.FileCount
		bytes += s.TotalBytes
	}
	if files != 3 || bytes != 9 {
		t.Errorf("submission totals: %d files, %d bytes; want 3, 9", files, bytes)
	}
}
