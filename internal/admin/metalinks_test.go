package admin

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/store"
)

// metaLinkRe matches every admin metadata-editor URL in a page, whether in an
// href or an htmx hx-post attribute.
var metaLinkRe = regexp.MustCompile(`(?:href|hx-post)="(/admin/collections/[^"]*/meta[^"]*\?path=[^"]*)"`)

// Every metadata link (Files screen, tree, and the editor's own save forms)
// must carry the stored name query-escaped, so a file literally named with
// "%20", "&", "#", "+" or "?" opens and saves its own metadata instead of
// 404ing or hitting another path (artifactsd-ikl, same class as
// artifactsd-vxu).
func TestMetaLinks_EscapeFileNames(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")
	names := []string{"Hacking%20Tutorial.mov", "a&b #1+?.txt"}
	for _, n := range names {
		seedFile(t, ha, "demo-run", n, "hi")
	}

	// linksFor returns the metadata URLs on a page that point at name.
	linksFor := func(page, name string) []string {
		t.Helper()
		var out []string
		for _, m := range metaLinkRe.FindAllStringSubmatch(page, -1) {
			raw := html.UnescapeString(m[1])
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("unparsable link %q: %v", raw, err)
			}
			if u.Query().Get("path") == name {
				out = append(out, strings.TrimPrefix(raw, "/admin"))
			}
		}
		return out
	}

	files := doReq(t, ha, http.MethodGet, "/files", http.NoBody, "").Body.String()
	tree := doReq(t, ha, http.MethodGet, "/collections/demo-run/tree", http.NoBody, "").Body.String()
	for _, n := range names {
		for screen, page := range map[string]string{"files": files, "tree": tree} {
			links := linksFor(page, n)
			if len(links) == 0 {
				t.Errorf("%s: no metadata link resolving to %q:\n%s", screen, n, page)
			}
			for _, l := range links {
				rec := doReq(t, ha, http.MethodGet, l, http.NoBody, "")
				if rec.Code != http.StatusOK {
					t.Errorf("%s: GET %s = %d: %s", screen, l, rec.Code, rec.Body)
				}
			}
		}

		// The editor's own save forms post back to the same file.
		editor := doReq(t, ha, http.MethodGet, "/collections/demo-run/meta?path="+url.QueryEscape(n), http.NoBody, "").Body.String()
		saves := linksFor(editor, n)
		if len(saves) != 2 {
			t.Fatalf("editor for %q: save forms resolving to it = %v, want 2:\n%s", n, saves, editor)
		}
		for _, l := range saves {
			if !strings.Contains(l, "/meta/fields?") {
				continue
			}
			rec := doReq(t, ha, http.MethodPost, l, strings.NewReader(url.Values{"content_type": {"text/x-test"}}.Encode()),
				"application/x-www-form-urlencoded")
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "saved") {
				t.Errorf("POST %s = %d: %s", l, rec.Code, rec.Body)
			}
		}
		node := nodeNamed(t, ha, "demo-run", n)
		if !strings.Contains(string(node.Metadata), "text/x-test") {
			t.Errorf("metadata of %q not saved: %s", n, node.Metadata)
		}
	}
}

func nodeNamed(t *testing.T, ha *harness, collection, p string) *store.Node {
	t.Helper()
	c, err := ha.st.GetCollectionByName(t.Context(), collection)
	if err != nil {
		t.Fatal(err)
	}
	n, err := ha.st.GetNode(t.Context(), c.ID, p)
	if err != nil {
		t.Fatalf("node %s/%s: %v", collection, p, err)
	}
	return n
}
