package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type listingJSON struct {
	Collection string `json:"collection"`
	Path       string `json:"path"`
	Entries    []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"entries"`
}

func getListing(t *testing.T, h *harness, target string) listingJSON {
	t.Helper()
	rec := h.get(target+"?format=json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d (%s)", target, rec.Code, rec.Body)
	}
	var v listingJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v (%s)", target, err, rec.Body)
	}
	return v
}

func entryNames(v listingJSON) string {
	names := make([]string, 0, len(v.Entries))
	for _, e := range v.Entries {
		names = append(names, e.Name)
	}
	return strings.Join(names, ",")
}

// An unlisted share with directory listing lists its files: the share's own
// "unlisted" keeps the share out of indexes, it does not hide what is inside
// it. Only a child marked unlisted itself stays out (artifactsd-f0q).
func TestServeShare_UnlistedShareListsContents(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "secretproj", `{}`)
	h.file(t, c, "file.html", "<p>hi</p>", `{}`, "text/html")
	h.file(t, c, "sub/b.txt", "b", `{}`, "text/plain")
	h.file(t, c, "hidden.txt", "x", `{"access":{"visibility":"unlisted"}}`, "text/plain")
	h.share(t, c, "shr_unl", "", "collection", `{"access":{"visibility":"unlisted","directory_listing":true}}`)

	if got := entryNames(getListing(t, h, "/s/shr_unl/")); got != "file.html,sub" {
		t.Errorf("share root entries = %q, want file.html,sub", got)
	}
	if got := entryNames(getListing(t, h, "/s/shr_unl/sub/")); got != "b.txt" {
		t.Errorf("share sub entries = %q, want b.txt", got)
	}
}

// A share listing never names the underlying collection or the shared
// folder's real path: the title is neutral and paths are share-relative
// (artifactsd-f0q).
func TestServeShare_ListingHidesRealNames(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "secretproj", `{"access":{"visibility":"private"}}`)
	h.file(t, c, "internal/q3-plans/deck/a.txt", "a", `{}`, "text/plain")
	h.share(t, c, "shr_coll", "", "collection", `{"access":{"directory_listing":true}}`)
	h.share(t, c, "shr_fold", "internal/q3-plans", "folder", `{"access":{"directory_listing":true}}`)

	for _, tc := range []struct{ url, wantTitle, wantPath, wantEntry string }{
		{"/s/shr_coll/", "Index of shared files /", "", "internal"},
		{"/s/shr_coll/internal/q3-plans/", "Index of shared files /internal/q3-plans/", "internal/q3-plans", "internal/q3-plans/deck"},
		{"/s/shr_fold/", "Index of shared files /", "", "deck"},
		{"/s/shr_fold/deck/", "Index of shared files /deck/", "deck", "deck/a.txt"},
	} {
		page := h.get(tc.url, map[string]string{"Accept": "text/html"}).Body.String()
		if !strings.Contains(page, "<title>"+tc.wantTitle+"</title>") {
			t.Errorf("%s: title missing %q:\n%s", tc.url, tc.wantTitle, page)
		}
		if strings.Contains(page, "secretproj") {
			t.Errorf("%s: HTML listing names the collection:\n%s", tc.url, page)
		}
		v := getListing(t, h, tc.url)
		if v.Collection != "" || v.Path != tc.wantPath || len(v.Entries) != 1 || v.Entries[0].Path != tc.wantEntry {
			t.Errorf("%s: JSON listing = %+v, want path %q and entry %q", tc.url, v, tc.wantPath, tc.wantEntry)
		}
	}
	// Folder shares must not reveal the folder path either.
	for _, u := range []string{"/s/shr_fold/", "/s/shr_fold/deck/"} {
		rec := h.get(u+"?format=json", nil)
		if strings.Contains(rec.Body.String(), "q3-plans") || strings.Contains(rec.Body.String(), "secretproj") {
			t.Errorf("%s leaks the real location: %s", u, rec.Body)
		}
	}
}

// The same rule on /c/: an unlisted folder reached by URL lists its own
// contents when directory listing is on (artifactsd-f0q), while an explicitly
// unlisted child is still omitted and the folder itself stays out of its
// parent's listing.
func TestServeDirectory_UnlistedFolderListsContents(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "pub", `{"access":{"visibility":"public","directory_listing":true}}`)
	h.file(t, c, "u/a.html", "a", `{}`, "text/html")
	h.file(t, c, "u/hidden.txt", "x", `{"access":{"visibility":"unlisted"}}`, "text/plain")
	h.file(t, c, "top.txt", "t", `{}`, "text/plain")
	h.folderMeta(t, c, "u", `{"access":{"visibility":"unlisted"}}`)

	if got := entryNames(getListing(t, h, "/c/pub/u/")); got != "a.html" {
		t.Errorf("unlisted folder entries = %q, want a.html", got)
	}
	if got := entryNames(getListing(t, h, "/c/pub/")); got != "top.txt" {
		t.Errorf("root entries = %q, want top.txt (the unlisted folder stays hidden)", got)
	}

	unl := h.collection(t, "unl", `{"access":{"visibility":"unlisted","directory_listing":true}}`)
	h.file(t, unl, "a.html", "a", `{}`, "text/html")
	if got := entryNames(getListing(t, h, "/c/unl/")); got != "a.html" {
		t.Errorf("unlisted collection entries = %q, want a.html", got)
	}
}
