package serve

import (
	"context"
	"encoding/xml"
	"net/http"
	"path"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/store"
)

// fileWithFolderMeta materializes a single folder node carrying folderMeta,
// then a file beneath it — exercising the metadata-chain walk where an ancestor
// folder's own visibility/expiry must gate the descendant file.
func (h *harness) fileWithFolderMeta(t *testing.T, c *store.Collection, folderPath, folderMeta, filePath, content, fileMeta string) {
	t.Helper()
	folder := &store.Node{
		CollectionID: c.ID, Path: folderPath, Name: path.Base(folderPath),
		Kind: store.KindFolder, Metadata: []byte(folderMeta),
	}
	if err := h.st.UpsertNode(context.Background(), folder); err != nil {
		t.Fatalf("UpsertNode folder: %v", err)
	}
	if err := h.st.RecomputeNodeDenormalized(context.Background(), folder.ID); err != nil {
		t.Fatalf("recompute folder: %v", err)
	}
	h.file(t, c, filePath, content, fileMeta, "text/html")
}

// parseURLSet unmarshals a sitemap <urlset> body and returns loc→lastmod.
func parseURLSet(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var doc urlsetXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal urlset: %v\nbody:\n%s", err, body)
	}
	out := map[string]string{}
	for _, u := range doc.URLs {
		out[u.Loc] = u.LastMod
	}
	return out
}

func TestSitemapRoot_OnlyPublicIndexableFiles(t *testing.T) {
	h := newHarness(t)

	pub := h.collection(t, "pub", `{"access":{"visibility":"public"},"crawl":{"indexable":true}}`)
	// Indexable+public file → included; inherits collection defaults.
	h.file(t, pub, "index.html", "<h1>hi</h1>", `{}`, "text/html")
	// Public but not indexable (file overrides indexable off) → excluded.
	h.file(t, pub, "draft.html", "draft", `{"crawl":{"indexable":false}}`, "text/html")
	// Unlisted file (directly fetchable but not advertised) → excluded.
	h.file(t, pub, "hidden.html", "hidden", `{"access":{"visibility":"unlisted"}}`, "text/html")
	// Private file → excluded.
	h.file(t, pub, "secret.html", "secret", `{"access":{"visibility":"private"}}`, "text/html")

	// A wholly non-indexable collection contributes nothing.
	plain := h.collection(t, "plain", `{"access":{"visibility":"public"}}`)
	h.file(t, plain, "page.html", "x", `{}`, "text/html")

	rec := h.get("/sitemap.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sitemap = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("content-type = %q, want application/xml...", ct)
	}
	locs := parseURLSet(t, rec.Body.Bytes())

	want := "https://x.example.com/c/pub/index.html"
	if _, ok := locs[want]; !ok {
		t.Errorf("missing %q in sitemap; got %v", want, locs)
	}
	if locs[want] == "" {
		t.Errorf("expected a <lastmod> for %q", want)
	}
	for loc := range locs {
		if loc != want {
			t.Errorf("unexpected loc in sitemap: %q", loc)
		}
	}
}

func TestSitemapRoot_PrivateAndExpiredSubtreesPruned(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "pub", `{"access":{"visibility":"public"},"crawl":{"indexable":true}}`)

	// File under a private folder: served path would 404 (ancestor-private
	// non-disclosure), so it must not appear even if the file itself is public.
	h.fileWithFolderMeta(t, c, "vault", `{"access":{"visibility":"private"}}`, "vault/leak.html", "x", `{"access":{"visibility":"public"},"crawl":{"indexable":true}}`)
	// File under an expired folder → excluded.
	h.fileWithFolderMeta(t, c, "old", `{"expiry":{"expires_at":"2000-01-01T00:00:00Z"}}`, "old/page.html", "x", `{}`)
	// A normal nested file → included.
	h.file(t, c, "docs/guide.html", "g", `{}`, "text/html")

	rec := h.get("/sitemap.xml", nil)
	locs := parseURLSet(t, rec.Body.Bytes())

	if _, ok := locs["https://x.example.com/c/pub/docs/guide.html"]; !ok {
		t.Errorf("nested public file missing; got %v", locs)
	}
	for loc := range locs {
		if strings.Contains(loc, "leak.html") || strings.Contains(loc, "old/page.html") {
			t.Errorf("pruned subtree leaked into sitemap: %q", loc)
		}
	}
}

func TestSitemapCollection_ScopedToOneCollection(t *testing.T) {
	h := newHarness(t)
	a := h.collection(t, "alpha", `{"access":{"visibility":"public"},"crawl":{"indexable":true}}`)
	b := h.collection(t, "beta", `{"access":{"visibility":"public"},"crawl":{"indexable":true}}`)
	h.file(t, a, "a.html", "a", `{}`, "text/html")
	h.file(t, b, "b.html", "b", `{}`, "text/html")

	rec := h.get("/c/alpha/sitemap.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("collection sitemap = %d", rec.Code)
	}
	locs := parseURLSet(t, rec.Body.Bytes())
	if _, ok := locs["https://x.example.com/c/alpha/a.html"]; !ok {
		t.Errorf("alpha file missing; got %v", locs)
	}
	if _, ok := locs["https://x.example.com/c/beta/b.html"]; ok {
		t.Errorf("beta file leaked into alpha sitemap; got %v", locs)
	}
}

func TestSitemapCollection_UnknownIs404(t *testing.T) {
	h := newHarness(t)
	if rec := h.get("/c/nope/sitemap.xml", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown collection sitemap = %d, want 404", rec.Code)
	}
}

func TestSitemapCollection_ShadowsUploadedFile(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "pub", `{"access":{"visibility":"public"},"crawl":{"indexable":true}}`)
	// A real file literally named sitemap.xml at the collection root is shadowed
	// by the generated endpoint (design/05 §4).
	h.file(t, c, "sitemap.xml", "<not-a-sitemap/>", `{}`, "application/xml")

	rec := h.get("/c/pub/sitemap.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sitemap = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "urlset") {
		t.Errorf("expected generated sitemap, got uploaded file:\n%s", rec.Body.String())
	}
	// A sitemap.xml in a subfolder remains an ordinary file.
	h.file(t, c, "sub/sitemap.xml", "<custom/>", `{}`, "application/xml")
	if rec := h.get("/c/pub/sub/sitemap.xml", nil); rec.Body.String() != "<custom/>" {
		t.Errorf("nested sitemap.xml should be the uploaded file, got %q", rec.Body.String())
	}
}

func TestSitemapRoot_EmptyWhenNothingEligible(t *testing.T) {
	h := newHarness(t)
	h.collection(t, "plain", `{"access":{"visibility":"public"}}`) // not indexable

	rec := h.get("/sitemap.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sitemap = %d", rec.Code)
	}
	locs := parseURLSet(t, rec.Body.Bytes())
	if len(locs) != 0 {
		t.Errorf("expected empty urlset, got %v", locs)
	}
}

func TestRenderSitemap_PaginatesPastCap(t *testing.T) {
	// Drive the index/pagination logic directly: building 50k+ real nodes in a
	// test DB would be needlessly slow.
	urls := make([]sitemapURL, sitemapMaxURLs+5)
	for i := range urls {
		urls[i] = sitemapURL{loc: "https://x.example.com/c/pub/f" + itoa(i) + ".html"}
	}
	self := "https://x.example.com/sitemap.xml"

	// No ?page → a <sitemapindex> with two pages.
	body, ok := renderSitemap(urls, self, false, 1)
	if !ok {
		t.Fatal("index render not ok")
	}
	var idx sitemapIndexXML
	if err := xml.Unmarshal(body, &idx); err != nil {
		t.Fatalf("unmarshal index: %v\n%s", err, body)
	}
	if len(idx.Sitemaps) != 2 {
		t.Fatalf("index pages = %d, want 2", len(idx.Sitemaps))
	}
	if idx.Sitemaps[0].Loc != self+"?page=1" || idx.Sitemaps[1].Loc != self+"?page=2" {
		t.Errorf("unexpected page locs: %q, %q", idx.Sitemaps[0].Loc, idx.Sitemaps[1].Loc)
	}

	// ?page=1 → first 50k URLs.
	body, ok = renderSitemap(urls, self, true, 1)
	if !ok {
		t.Fatal("page 1 render not ok")
	}
	var p1 urlsetXML
	if err := xml.Unmarshal(body, &p1); err != nil {
		t.Fatalf("unmarshal page 1: %v", err)
	}
	if len(p1.URLs) != sitemapMaxURLs {
		t.Errorf("page 1 URLs = %d, want %d", len(p1.URLs), sitemapMaxURLs)
	}

	// ?page=2 → the remaining 5.
	body, _ = renderSitemap(urls, self, true, 2)
	var p2 urlsetXML
	if err := xml.Unmarshal(body, &p2); err != nil {
		t.Fatalf("unmarshal page 2: %v", err)
	}
	if len(p2.URLs) != 5 {
		t.Errorf("page 2 URLs = %d, want 5", len(p2.URLs))
	}

	// Out-of-range page → not ok (404 at the HTTP layer).
	if _, ok := renderSitemap(urls, self, true, 3); ok {
		t.Error("page 3 should be out of range")
	}
}

func TestRenderSitemap_SmallSetRejectsExtraPages(t *testing.T) {
	urls := []sitemapURL{{loc: "https://x.example.com/c/pub/a.html"}}
	if _, ok := renderSitemap(urls, "https://x.example.com/sitemap.xml", true, 2); ok {
		t.Error("page 2 of a single-page sitemap should 404")
	}
	if _, ok := renderSitemap(urls, "https://x.example.com/sitemap.xml", false, 1); !ok {
		t.Error("no-page request should render the single urlset")
	}
}

func TestSitemap_BadPageParam(t *testing.T) {
	h := newHarness(t)
	h.collection(t, "pub", `{"access":{"visibility":"public"},"crawl":{"indexable":true}}`)
	for _, p := range []string{"0", "-1", "abc"} {
		if rec := h.get("/sitemap.xml?page="+p, nil); rec.Code != http.StatusNotFound {
			t.Errorf("page=%q = %d, want 404", p, rec.Code)
		}
	}
}

// itoa is a tiny strconv.Itoa stand-in to keep the test imports minimal.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
