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
		"file_visibility":        {"unlisted"},
		"single_use":             {"on"},
		"use_submission_folders": {"on"},
		"allow_uploader_access":  {"on"},
		"allow_overwrite":        {"on"},
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
	if !ur.SingleUse || !ur.UseSubmissionFolders || !ur.AllowUploaderAccess || !ur.AllowOverwrite {
		t.Errorf("toggles = single_use:%v submission_folders:%v uploader_access:%v overwrite:%v, want all true",
			ur.SingleUse, ur.UseSubmissionFolders, ur.AllowUploaderAccess, ur.AllowOverwrite)
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
	if meta.Access.Visibility != "unlisted" {
		t.Errorf("file_metadata = %s, want visibility unlisted", ur.FileMetadata)
	}
}

// TestCreateUploadRequestAdmin_DefaultsPrivate: without a visibility the link's
// files are private, and uploader access then cannot be enabled.
func TestCreateUploadRequestAdmin_DefaultsPrivate(t *testing.T) {
	ha := newHarness(t)
	col := &store.Collection{Name: "inbox", Metadata: []byte(`{}`)}
	if err := ha.st.CreateCollection(context.Background(), col); err != nil {
		t.Fatalf("create collection: %v", err)
	}
	id := &session.Identity{Subject: "u1", Email: "admin@example.com", Expiry: time.Now().Add(time.Hour)}
	post := func(form url.Values) string {
		r := withIdentity(httptest.NewRequest(http.MethodPost, "/upload-requests",
			strings.NewReader(form.Encode())), id)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		ha.h.ServeHTTP(rec, r)
		return rec.Body.String()
	}
	post(url.Values{"collection": {"inbox"}})
	urs, _ := ha.st.ListUploadRequests(context.Background(), &col.ID)
	if len(urs) != 1 || !strings.Contains(string(urs[0].FileMetadata), `"visibility":"private"`) {
		t.Fatalf("default link: %d links, metadata %s", len(urs), urs[0].FileMetadata)
	}
	body := post(url.Values{"collection": {"inbox"}, "file_visibility": {"private"}, "allow_uploader_access": {"on"}})
	if !strings.Contains(body, "requires public or unlisted") {
		t.Errorf("private + uploader access: want an error, body: %s", body)
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
