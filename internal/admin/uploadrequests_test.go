package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// TestCreateUploadRequestAdmin_AllFields covers the admin create form's full
// field set (artifactsd-nml): description, limits, behavior toggles, the
// file-visibility metadata, and the split expires_at/expires_in pair.
func TestCreateUploadRequestAdmin_AllFields(t *testing.T) {
	ha := newHarness(t)
	col := &store.Collection{Name: "inbox", Metadata: []byte(`{}`)}
	if err := ha.st.CreateCollection(context.Background(), col); err != nil {
		t.Fatalf("create collection: %v", err)
	}
	id := &session.Identity{Subject: "u1", Email: "admin@example.com", Expiry: time.Now().Add(time.Hour)}

	form := url.Values{
		"collection":             {"inbox"},
		"target_folder":          {"q2"},
		"label":                  {"Q2 deliverables"},
		"description":            {"PDF or DOCX please"},
		"expires_in":             {"7d"},
		"max_files":              {"50"},
		"max_file_size":          {"10MB"},
		"file_visibility":        {"private"},
		"single_use":             {"on"},
		"use_submission_folders": {"on"},
		"allow_uploader_access":  {"on"},
	}
	r := withIdentity(httptest.NewRequest(http.MethodPost, "/upload-requests",
		strings.NewReader(form.Encode())), id)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ha.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "nc-error") {
		t.Fatalf("create rendered an error: %s", rec.Body.String())
	}

	urs, err := ha.st.ListUploadRequests(context.Background(), &col.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(urs) != 1 {
		t.Fatalf("requests = %d, want 1", len(urs))
	}
	ur := urs[0]
	if ur.Description == nil || *ur.Description != "PDF or DOCX please" {
		t.Errorf("description = %v", ur.Description)
	}
	if ur.MaxFiles == nil || *ur.MaxFiles != 50 {
		t.Errorf("max_files = %v", ur.MaxFiles)
	}
	if ur.MaxFileSize == nil || *ur.MaxFileSize != 10<<20 {
		t.Errorf("max_file_size = %v, want %d (10MB, 1024-based)", ur.MaxFileSize, 10<<20)
	}
	if !ur.SingleUse || !ur.UseSubmissionFolders || !ur.AllowUploaderAccess {
		t.Errorf("toggles = single_use:%v submission_folders:%v uploader_access:%v, want all true",
			ur.SingleUse, ur.UseSubmissionFolders, ur.AllowUploaderAccess)
	}
	if ur.ExpiresAt == nil {
		t.Error("expires_at not set from expires_in duration")
	}
	var meta struct {
		Access struct {
			Visibility string `json:"visibility"`
		} `json:"access"`
	}
	if err := json.Unmarshal(ur.FileMetadata, &meta); err != nil {
		t.Fatalf("file_metadata json: %v (%s)", err, ur.FileMetadata)
	}
	if meta.Access.Visibility != "private" {
		t.Errorf("file_metadata = %s, want visibility private", ur.FileMetadata)
	}
}

// TestCreateUploadRequestAdmin_ExpiryPairExclusive rejects setting both the
// absolute and the relative expiry, mirroring the CLI flag pair.
func TestCreateUploadRequestAdmin_ExpiryPairExclusive(t *testing.T) {
	ha := newHarness(t)
	col := &store.Collection{Name: "inbox", Metadata: []byte(`{}`)}
	if err := ha.st.CreateCollection(context.Background(), col); err != nil {
		t.Fatalf("create collection: %v", err)
	}
	id := &session.Identity{Subject: "u1", Email: "admin@example.com", Expiry: time.Now().Add(time.Hour)}

	form := url.Values{
		"collection": {"inbox"},
		"expires_in": {"7d"},
		"expires_at": {"2039-01-01T00:00:00Z"},
	}
	r := withIdentity(httptest.NewRequest(http.MethodPost, "/upload-requests",
		strings.NewReader(form.Encode())), id)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ha.h.ServeHTTP(rec, r)
	if !strings.Contains(rec.Body.String(), "not both") {
		t.Errorf("want both-expiry error, body: %s", rec.Body.String())
	}
	if urs, _ := ha.st.ListUploadRequests(context.Background(), &col.ID); len(urs) != 0 {
		t.Errorf("requests = %d, want 0 (rejected)", len(urs))
	}
}
