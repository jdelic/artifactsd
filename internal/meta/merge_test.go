package meta

import "testing"

// TestMerge_NilPatch ensures a nil patch leaves the receiver untouched.
func TestMerge_NilPatch(t *testing.T) {
	d := &Document{Access: &Access{Visibility: strp(VisibilityPrivate)}}
	d.Merge(nil)
	if d.Access == nil || d.Access.Visibility == nil || *d.Access.Visibility != VisibilityPrivate {
		t.Fatalf("nil patch mutated document: %+v", d)
	}
}

// TestMerge_ScalarOverride covers content_type, visibility, and directory_listing
// scalar overrides, and confirms sections absent from the patch are untouched.
func TestMerge_ScalarOverride(t *testing.T) {
	base := &Document{
		Content: &Content{ContentType: strp("text/plain")},
		Access:  &Access{Visibility: strp(VisibilityPublic), DirectoryListing: boolp(false)},
		Crawl:   &Crawl{Crawlable: boolp(true)},
	}
	patch := &Document{
		Content: &Content{ContentType: strp("text/html")},
		Access:  &Access{Visibility: strp(VisibilityProtected)},
	}
	base.Merge(patch)

	if *base.Content.ContentType != "text/html" {
		t.Errorf("content_type = %q, want text/html", *base.Content.ContentType)
	}
	if *base.Access.Visibility != VisibilityProtected {
		t.Errorf("visibility = %q, want protected", *base.Access.Visibility)
	}
	// directory_listing was not in the patch → preserved.
	if base.Access.DirectoryListing == nil || *base.Access.DirectoryListing {
		t.Errorf("directory_listing should be preserved as false, got %v", base.Access.DirectoryListing)
	}
	// crawl section absent from patch → untouched.
	if base.Crawl == nil || base.Crawl.Crawlable == nil || !*base.Crawl.Crawlable {
		t.Errorf("crawl section should be untouched, got %+v", base.Crawl)
	}
}

// TestMerge_HeadersMapMergeAndRemove covers header map-merge with empty-value
// removal (design/03 §2).
func TestMerge_HeadersMapMergeAndRemove(t *testing.T) {
	base := &Document{Content: &Content{Headers: map[string]string{
		"Cache-Control": "max-age=60",
		"X-Keep":        "stays",
		"X-Drop":        "old",
	}}}
	patch := &Document{Content: &Content{Headers: map[string]string{
		"Cache-Control": "no-store", // override
		"X-Drop":        "",         // remove
		"X-New":         "added",    // add
	}}}
	base.Merge(patch)

	want := map[string]string{
		"Cache-Control": "no-store",
		"X-Keep":        "stays",
		"X-New":         "added",
	}
	got := base.Content.Headers
	if len(got) != len(want) {
		t.Fatalf("headers = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("header %q = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["X-Drop"]; ok {
		t.Error("X-Drop should have been removed by empty value")
	}
}

// TestMerge_HeadersCaseInsensitive confirms that override and removal match the
// existing header regardless of key casing (artifactsd-lw4.1): the base holds a
// canonical key, the patch uses a different case, and the result has exactly one
// canonical entry.
func TestMerge_HeadersCaseInsensitive(t *testing.T) {
	base := &Document{Content: &Content{Headers: map[string]string{
		"Content-Type":  "text/plain",
		"Cache-Control": "max-age=60",
	}}}
	patch := &Document{Content: &Content{Headers: map[string]string{
		"content-type":  "text/html", // override despite lower-case key
		"cache-control": "",          // remove despite lower-case key
	}}}
	base.Merge(patch)

	got := base.Content.Headers
	if len(got) != 1 {
		t.Fatalf("headers = %v, want exactly one canonical entry", got)
	}
	if got["Content-Type"] != "text/html" {
		t.Errorf("Content-Type = %q, want text/html (case-insensitive override)", got["Content-Type"])
	}
	if _, ok := got["Cache-Control"]; ok {
		t.Error("Cache-Control should have been removed by a lower-case empty-value patch")
	}
	if _, ok := got["cache-control"]; ok {
		t.Error("no non-canonical key should remain after merge")
	}
}

// TestMerge_HeadersIntoNilBase ensures headers can be added when the base has no
// content section at all.
func TestMerge_HeadersIntoNilBase(t *testing.T) {
	base := &Document{}
	patch := &Document{Content: &Content{Headers: map[string]string{"X-A": "1"}}}
	base.Merge(patch)
	if base.Content == nil || base.Content.Headers["X-A"] != "1" {
		t.Fatalf("header not added into empty base: %+v", base.Content)
	}
}

// TestMerge_AuthReplacedWholesale confirms access.auth is replaced, not merged,
// and that an explicit empty list ("no auth") replaces an inherited list.
func TestMerge_AuthReplacedWholesale(t *testing.T) {
	base := &Document{Access: &Access{Auth: &[]AuthEntry{
		{Type: AuthBearer, TokenHash: "h1"},
		{Type: AuthBasic, Username: "u", PasswordHash: "h2"},
	}}}
	patch := &Document{Access: &Access{Auth: &[]AuthEntry{
		{Type: AuthBearer, TokenHash: "h3"},
	}}}
	base.Merge(patch)
	if got := *base.Access.Auth; len(got) != 1 || got[0].TokenHash != "h3" {
		t.Fatalf("auth not replaced wholesale: %+v", got)
	}

	// Explicit empty list replaces (means "no auth").
	empty := &Document{Access: &Access{Auth: &[]AuthEntry{}}}
	base.Merge(empty)
	if base.Access.Auth == nil || len(*base.Access.Auth) != 0 {
		t.Fatalf("explicit empty auth list should replace, got %+v", base.Access.Auth)
	}
}

// TestMerge_AuthNilPatchPreserves confirms a patch that does not set auth leaves
// the inherited auth in place (unset != empty).
func TestMerge_AuthNilPatchPreserves(t *testing.T) {
	base := &Document{Access: &Access{Auth: &[]AuthEntry{{Type: AuthBearer, TokenHash: "h1"}}}}
	patch := &Document{Access: &Access{Visibility: strp(VisibilityProtected)}}
	base.Merge(patch)
	if base.Access.Auth == nil || len(*base.Access.Auth) != 1 {
		t.Fatalf("auth should be preserved when patch omits it, got %+v", base.Access.Auth)
	}
}

// TestMerge_CrawlAndExpiry covers the crawl and expiry section overrides, into
// both populated and nil base sections.
func TestMerge_CrawlAndExpiry(t *testing.T) {
	base := &Document{}
	patch := &Document{
		Crawl: &Crawl{
			Crawlable: boolp(false),
			Indexable: boolp(true),
			Robots:    &Robots{XRobotsTag: []string{"noindex"}},
		},
		Expiry: &Expiry{
			ExpiresAt:           strp("2030-01-01T00:00:00Z"),
			DeleteAfterCreation: strp("24h"),
		},
	}
	base.Merge(patch)

	if base.Crawl == nil || *base.Crawl.Crawlable || !*base.Crawl.Indexable {
		t.Fatalf("crawl scalars not applied into nil base: %+v", base.Crawl)
	}
	if base.Crawl.Robots == nil || len(base.Crawl.Robots.XRobotsTag) != 1 {
		t.Errorf("robots not applied: %+v", base.Crawl.Robots)
	}
	if base.Expiry == nil || base.Expiry.ExpiresAt == nil || *base.Expiry.ExpiresAt != "2030-01-01T00:00:00Z" {
		t.Errorf("expires_at not applied: %+v", base.Expiry)
	}
	if base.Expiry.DeleteAfterCreation == nil || *base.Expiry.DeleteAfterCreation != "24h" {
		t.Errorf("delete_after_creation not applied: %+v", base.Expiry)
	}
	// delete_after_last_access not in patch → stays nil.
	if base.Expiry.DeleteAfterLastAccess != nil {
		t.Errorf("delete_after_last_access should be nil, got %v", *base.Expiry.DeleteAfterLastAccess)
	}
}
