package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestAllSharesPage covers artifactsd-rmn: the global shares overview lists
// shares from every collection and can revoke them.
func TestAllSharesPage(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")

	rec := doReq(t, ha, http.MethodGet, "/shares", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No shares in any collection yet") {
		t.Fatalf("empty all-shares = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/shares", strings.NewReader(url.Values{"target_path": {""}}.Encode()), "application/x-www-form-urlencoded")
	shareID := extractShareID(rec.Body.String())
	if shareID == "" {
		t.Fatalf("no share id: %s", rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodGet, "/shares", http.NoBody, "")
	body := rec.Body.String()
	if !strings.Contains(body, "All shares") || !strings.Contains(body, "demo-run") || !strings.Contains(body, shareID) {
		t.Fatalf("all-shares missing the share: %s", body)
	}

	rec = doReq(t, ha, http.MethodPost, "/shares/"+shareID+"/revoke", http.NoBody, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), shareID) {
		t.Fatalf("global revoke = %d, still lists %s: %s", rec.Code, shareID, rec.Body.String())
	}
}

// TestAllFilesPage covers artifactsd-0ab: the cross-collection file browser
// lists files and sorts largest-first by default.
func TestAllFilesPage(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")
	seedFile(t, ha, "demo-run", "small.txt", "hi")
	seedFile(t, ha, "demo-run", "big.txt", strings.Repeat("x", 500))

	rec := doReq(t, ha, http.MethodGet, "/files", http.NoBody, "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "All files") {
		t.Fatalf("all-files = %d: %s", rec.Code, body)
	}
	bigIdx, smallIdx := strings.Index(body, "big.txt"), strings.Index(body, "small.txt")
	if bigIdx < 0 || smallIdx < 0 {
		t.Fatalf("files missing from listing: %s", body)
	}
	if bigIdx > smallIdx {
		t.Errorf("default sort should be largest-first; big.txt came after small.txt")
	}

	// A by-name sort is accepted (whitelisted; no injection).
	rec = doReq(t, ha, http.MethodGet, "/files?sort=name", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "big.txt") {
		t.Fatalf("sort=name = %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAllFilesPage_ExpiryColumnAndSort covers artifactsd-7h3: the file list
// shows an expiry column, and sort=expiry surfaces expiring files before
// never-expiring ones.
func TestAllFilesPage_ExpiryColumnAndSort(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")
	seedFile(t, ha, "demo-run", "keep.txt", "k")
	seedFile(t, ha, "demo-run", "temp.txt", "t")

	// Give temp.txt an expiry; keep.txt never expires.
	exp := url.Values{"delete_after_creation_enabled": {"on"}, "delete_after_creation": {"30d"}}
	doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/fields?path=temp.txt", strings.NewReader(exp.Encode()), "application/x-www-form-urlencoded")

	rec := doReq(t, ha, http.MethodGet, "/files?sort=expiry", http.NoBody, "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "<th>Expires</th>") {
		t.Fatalf("expiry column missing: %d: %s", rec.Code, body)
	}
	tempIdx, keepIdx := strings.Index(body, "temp.txt"), strings.Index(body, "keep.txt")
	if tempIdx < 0 || keepIdx < 0 {
		t.Fatalf("files missing: %s", body)
	}
	if tempIdx > keepIdx {
		t.Errorf("sort=expiry should list the expiring file (temp.txt) before the never-expiring one (keep.txt)")
	}
}

// TestNav_FilesSharesAreLinks covers artifactsd-rmn/0ab: the top-nav Files and
// Shares items are real links, not decorative spans.
func TestNav_FilesSharesAreLinks(t *testing.T) {
	ha := newFullHarness(t)
	rec := doReq(t, ha, http.MethodGet, "/", http.NoBody, "")
	body := rec.Body.String()
	if !strings.Contains(body, `href="/admin/files"`) || !strings.Contains(body, `href="/admin/shares"`) {
		t.Errorf("Files/Shares nav items are not links: %s", body)
	}
}
