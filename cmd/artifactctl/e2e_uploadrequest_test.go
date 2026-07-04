package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// TestE2E_UploadRequestLifecycle drives create (with file-metadata flags,
// artifactsd-nml), list, show, close, and revoke end-to-end.
func TestE2E_UploadRequestLifecycle(t *testing.T) {
	e := newE2E(t)
	if code, _, errOut := e.run("collection", "create", "inbox"); code != exitOK {
		t.Fatalf("create collection: code=%d err=%s", code, errOut)
	}

	code, out, errOut := e.run("--output", "json", "upload-request", "create", "inbox",
		"--target-folder", "q2", "--label", "Q2 deliverables",
		"--max-files", "50", "--max-file-size", "10MB",
		"--private", "--no-index", "--expires-in", "7d",
		"--use-submission-folders")
	if code != exitOK {
		t.Fatalf("create: code=%d err=%s", code, errOut)
	}
	var ur apiclient.UploadRequest
	if err := json.Unmarshal([]byte(out), &ur); err != nil {
		t.Fatalf("create json: %v (%s)", err, out)
	}
	if ur.Status != "active" || ur.TargetFolder != "q2" || !ur.UseSubmissionFolders {
		t.Errorf("created = %+v", ur)
	}
	if ur.MaxFileSize == nil || *ur.MaxFileSize != 10<<20 {
		t.Errorf("max_file_size = %v, want %d (1024-based 10MB)", ur.MaxFileSize, 10<<20)
	}
	meta := string(ur.FileMetadata)
	if !strings.Contains(meta, `"private"`) || !strings.Contains(meta, `"indexable"`) {
		t.Errorf("file_metadata = %s, want visibility private + indexable false", meta)
	}

	// show is the primary verb; get stays as an alias (artifactsd-9mp).
	for _, verb := range []string{"show", "get"} {
		if code, out, errOut := e.run("upload-request", verb, ur.RequestID); code != exitOK || !strings.Contains(out, ur.RequestID) {
			t.Fatalf("%s: code=%d out=%q err=%s", verb, code, out, errOut)
		}
	}

	if code, out, errOut := e.run("upload-request", "list"); code != exitOK || !strings.Contains(out, ur.RequestID) {
		t.Fatalf("list: code=%d out=%q err=%s", code, out, errOut)
	}
	if code, _, errOut := e.run("upload-request", "close", ur.RequestID); code != exitOK {
		t.Fatalf("close: code=%d err=%s", code, errOut)
	}
	if code, _, errOut := e.run("upload-request", "revoke", ur.RequestID); code != exitOK {
		t.Fatalf("revoke: code=%d err=%s", code, errOut)
	}
	code, out, errOut = e.run("--output", "json", "upload-request", "show", ur.RequestID)
	if code != exitOK {
		t.Fatalf("show after revoke: code=%d err=%s", code, errOut)
	}
	if err := json.Unmarshal([]byte(out), &ur); err != nil {
		t.Fatalf("show json: %v", err)
	}
	if ur.Status != "revoked" {
		t.Errorf("status = %q, want revoked", ur.Status)
	}
}
