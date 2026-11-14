package uploadrequest_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
)

// submittedIP uploads one file with the given X-Forwarded-For header and
// returns the client IP recorded on the submission.
func submittedIP(t *testing.T, ts *testserver.Server, xff string) string {
	t.Helper()
	id := createLink(t, ts, `{"collection":"demo","target_folder":"in","use_submission_folders":true}`)
	req := uploadReq(t, ts, id, part{"a.txt", "a"})
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if code, b := do(t, ts, req); code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	ctx := context.Background()
	ur, err := ts.Store.GetUploadRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	subs, err := ts.Store.ListUploadRequestSubmissions(ctx, ur.ID)
	if err != nil || len(subs) != 1 {
		t.Fatalf("submissions: %v %d", err, len(subs))
	}
	if subs[0].ClientIP == nil {
		return ""
	}
	return *subs[0].ClientIP
}

func TestUploadClientIP_NoTrustedProxies(t *testing.T) {
	ts := testserver.New(t)
	createCollection(t, ts, "demo")
	if got := submittedIP(t, ts, "<b>spoofed</b>, 1.2.3.4"); got != "127.0.0.1" {
		t.Errorf("client_ip = %q, want socket address 127.0.0.1", got)
	}
}

func TestUploadClientIP_TrustedProxy(t *testing.T) {
	ts := testserver.NewWithEnv(t, map[string]string{"ARTIFACTSD_TRUSTED_PROXIES": "127.0.0.1"})
	createCollection(t, ts, "demo")
	if got := submittedIP(t, ts, "<b>spoofed</b>"); got != "127.0.0.1" {
		t.Errorf("garbage XFF: client_ip = %q, want socket address 127.0.0.1", got)
	}
	if got := submittedIP(t, ts, "192.0.2.66, 203.0.113.9"); got != "203.0.113.9" {
		t.Errorf("valid XFF: client_ip = %q, want 203.0.113.9", got)
	}
}
