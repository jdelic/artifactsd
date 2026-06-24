package serve

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestRobotsRoot_AllowDisallowByIndexableAndVisibility(t *testing.T) {
	h := newHarness(t)
	h.collection(t, "pub", `{"crawl":{"indexable":true}}`)
	h.collection(t, "hidden", `{}`) // indexable defaults false
	h.collection(t, "secret", `{"access":{"visibility":"private"},"crawl":{"indexable":true}}`)
	h.collection(t, "quiet", `{"access":{"visibility":"unlisted"},"crawl":{"indexable":true}}`)

	rec := h.get("/robots.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("robots.txt = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want text/plain...", ct)
	}
	body := rec.Body.String()

	if !strings.Contains(body, "Disallow: /api/\n") {
		t.Errorf("missing Disallow: /api/ in:\n%s", body)
	}
	if !strings.Contains(body, "Disallow: /admin/\n") {
		t.Errorf("missing Disallow: /admin/ in:\n%s", body)
	}
	if !strings.Contains(body, "Allow: /c/pub/\n") {
		t.Errorf("pub should be allowed (indexable, public):\n%s", body)
	}
	if !strings.Contains(body, "Disallow: /c/hidden/\n") {
		t.Errorf("hidden should be disallowed (not indexable):\n%s", body)
	}
	if !strings.Contains(body, "Disallow: /c/secret/\n") {
		t.Errorf("secret should be disallowed (private, despite indexable):\n%s", body)
	}
	if !strings.Contains(body, "Disallow: /c/quiet/\n") {
		t.Errorf("quiet should be disallowed (unlisted, despite indexable):\n%s", body)
	}
	if !strings.Contains(body, "Sitemap: https://x.example.com/sitemap.xml\n") {
		t.Errorf("missing Sitemap: line (pub is allowed):\n%s", body)
	}
}

func TestRobotsRoot_NoSitemapLineWhenNothingIndexable(t *testing.T) {
	h := newHarness(t)
	h.collection(t, "demo", `{}`) // indexable defaults false

	rec := h.get("/robots.txt", nil)
	if strings.Contains(rec.Body.String(), "Sitemap:") {
		t.Errorf("unexpected Sitemap: line when nothing indexable:\n%s", rec.Body.String())
	}
}

// A "robots.txt" path under a collection is just an ordinary file lookup —
// there is no per-collection robots.txt endpoint (RFC 9309 only recognizes
// the file at the domain root; see SPEC §10.3).
func TestServe_CollectionRobotsTxtPathIsAnOrdinaryFile(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "robots.txt", "User-agent: nope\n", `{}`, "text/plain")

	rec := h.get("/c/demo/robots.txt", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "User-agent: nope\n" {
		t.Errorf("serve = %d %q, want the uploaded file's own content", rec.Code, rec.Body)
	}
}

func TestRobotsRoot_CachedWithinTTL(t *testing.T) {
	h := newHarness(t)
	h.collection(t, "demo", `{"crawl":{"indexable":true}}`)

	first := h.get("/robots.txt", nil).Body.String()

	// Mutate the collection's metadata directly (bypassing any invalidation
	// hook); within the TTL window the cached body must still be served.
	c, err := h.st.GetCollectionByName(context.Background(), "demo")
	if err != nil {
		t.Fatalf("GetCollectionByName: %v", err)
	}
	c.Metadata = []byte(`{}`) // would now resolve as non-indexable
	if err := h.st.UpdateCollectionMetadata(context.Background(), c); err != nil {
		t.Fatalf("UpdateCollectionMetadata: %v", err)
	}

	second := h.get("/robots.txt", nil).Body.String()
	if second != first {
		t.Errorf("robots.txt changed within the cache TTL:\nfirst:  %q\nsecond: %q", first, second)
	}
}
