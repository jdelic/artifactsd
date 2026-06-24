package serve

import (
	"context"
	"encoding/xml"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/store"
)

// sitemapNS is the sitemaps.org namespace shared by <urlset> and
// <sitemapindex> documents.
const sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

// sitemapMaxURLs is the sitemaps.org per-file cap (50,000 URLs). Beyond it the
// no-page response becomes a <sitemapindex> paginating the URL set via
// ?page=N, and each page returns its own 50k-URL <urlset> (design/05 §4).
const sitemapMaxURLs = 50000

// sitemapCacheTTL bounds how stale a generated sitemap may be after a metadata
// change, mirroring robotsCacheTTL (design/05 §4/§6). Pagination/rendering is
// cheap; only the policy-filtered URL collection is memoized.
const sitemapCacheTTL = 30 * time.Second

// sitemapURL is one eligible public+indexable file (design/05 §4).
type sitemapURL struct {
	loc     string
	lastmod time.Time
}

// sitemapCache memoizes the collected URL set keyed by request variant ("root"
// or "col:{name}"). Bounded by the collection count and overwritten on refresh
// rather than evicted.
type sitemapCache struct {
	mu      sync.Mutex
	entries map[string]sitemapCacheEntry
}

type sitemapCacheEntry struct {
	urls    []sitemapURL
	expires time.Time
}

func newSitemapCache() *sitemapCache {
	return &sitemapCache{entries: map[string]sitemapCacheEntry{}}
}

func (sc *sitemapCache) get(key string, now time.Time, compute func() []sitemapURL) []sitemapURL {
	sc.mu.Lock()
	if e, ok := sc.entries[key]; ok && now.Before(e.expires) {
		urls := e.urls
		sc.mu.Unlock()
		return urls
	}
	sc.mu.Unlock()

	urls := compute()

	sc.mu.Lock()
	sc.entries[key] = sitemapCacheEntry{urls: urls, expires: now.Add(sitemapCacheTTL)}
	sc.mu.Unlock()
	return urls
}

// SitemapRoot handles GET /sitemap.xml: every public+indexable, non-expired
// file across all collections (design/05 §4, SPEC §10.4).
func (h *Handler) SitemapRoot(w http.ResponseWriter, r *http.Request) {
	h.serveSitemap(w, r, "", "/sitemap.xml")
}

// SitemapCollection handles the collection-root sitemap, scoped to one
// collection. It is dispatched from ServeCollection (see serve.go) rather than
// registered as its own route, so the generated sitemap shadows any uploaded
// file literally named "sitemap.xml" at the collection root — unlike robots.txt,
// design/05 §4 makes the per-collection sitemap a generated endpoint.
func (h *Handler) SitemapCollection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("collection")
	h.serveSitemap(w, r, name, "/c/"+name+"/sitemap.xml")
}

// serveSitemap renders the sitemap for collection (empty = all collections),
// paginating at sitemapMaxURLs: the no-?page response is a <sitemapindex> once
// the set exceeds the cap, and ?page=N returns the Nth 50k-URL <urlset>.
func (h *Handler) serveSitemap(w http.ResponseWriter, r *http.Request, collection, selfPath string) {
	rawPage := r.URL.Query().Get("page")
	page := 1
	if rawPage != "" {
		n, err := strconv.Atoi(rawPage)
		if err != nil || n < 1 {
			http.NotFound(w, r)
			return
		}
		page = n
	}

	if collection != "" {
		if _, err := h.store.GetCollectionByName(r.Context(), collection); err != nil {
			// Unknown (or unreadable) collection: nothing to enumerate.
			http.NotFound(w, r)
			return
		}
	}

	key := "root"
	if collection != "" {
		key = "col:" + collection
	}
	urls := h.sitemapCache.get(key, time.Now(), func() []sitemapURL {
		return h.collectSitemapURLs(r.Context(), collection)
	})

	body, ok := renderSitemap(urls, h.sitemapLoc(selfPath), rawPage != "", page)
	if !ok {
		http.NotFound(w, r) // page out of range
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(body)
}

// renderSitemap turns a URL set into either a single <urlset>, a paginating
// <sitemapindex>, or one page's <urlset>. self is the absolute URL of this
// sitemap endpoint (used to build per-page index links). ok is false when an
// explicit ?page is out of range.
func renderSitemap(urls []sitemapURL, self string, explicitPage bool, page int) ([]byte, bool) {
	total := len(urls)
	if total <= sitemapMaxURLs {
		if explicitPage && page != 1 {
			return nil, false
		}
		return renderURLSet(urls), true
	}

	pages := (total + sitemapMaxURLs - 1) / sitemapMaxURLs
	if !explicitPage {
		return renderSitemapIndex(urls, self, pages), true
	}
	if page > pages {
		return nil, false
	}
	start := (page - 1) * sitemapMaxURLs
	end := start + sitemapMaxURLs
	if end > total {
		end = total
	}
	return renderURLSet(urls[start:end]), true
}

type urlsetXML struct {
	XMLName xml.Name `xml:"urlset"`
	NS      string   `xml:"xmlns,attr"`
	URLs    []urlXML `xml:"url"`
}

type urlXML struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type sitemapIndexXML struct {
	XMLName  xml.Name        `xml:"sitemapindex"`
	NS       string          `xml:"xmlns,attr"`
	Sitemaps []sitemapRefXML `xml:"sitemap"`
}

type sitemapRefXML struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

func renderURLSet(urls []sitemapURL) []byte {
	doc := urlsetXML{NS: sitemapNS, URLs: make([]urlXML, 0, len(urls))}
	for _, u := range urls {
		doc.URLs = append(doc.URLs, urlXML{Loc: u.loc, LastMod: lastmodStr(u.lastmod)})
	}
	return marshalSitemap(doc)
}

// renderSitemapIndex emits one <sitemap> per page; each <lastmod> is the most
// recent file lastmod within that page so crawlers can skip unchanged pages.
func renderSitemapIndex(urls []sitemapURL, self string, pages int) []byte {
	doc := sitemapIndexXML{NS: sitemapNS, Sitemaps: make([]sitemapRefXML, 0, pages)}
	for p := 1; p <= pages; p++ {
		start := (p - 1) * sitemapMaxURLs
		end := start + sitemapMaxURLs
		if end > len(urls) {
			end = len(urls)
		}
		doc.Sitemaps = append(doc.Sitemaps, sitemapRefXML{
			Loc:     self + "?page=" + strconv.Itoa(p),
			LastMod: lastmodStr(latestLastmod(urls[start:end])),
		})
	}
	return marshalSitemap(doc)
}

func marshalSitemap(doc any) []byte {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		// The document types are fixed and always marshalable; fall back to an
		// empty (but well-formed) urlset rather than emitting a partial body.
		body = []byte(`<urlset xmlns="` + sitemapNS + `"></urlset>`)
	}
	return append([]byte(xml.Header), append(body, '\n')...)
}

func lastmodStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func latestLastmod(urls []sitemapURL) time.Time {
	var latest time.Time
	for _, u := range urls {
		if u.lastmod.After(latest) {
			latest = u.lastmod
		}
	}
	return latest
}

// collectSitemapURLs enumerates every public+indexable, non-expired file under
// collection (empty = all collections), resolving each node's effective
// metadata down the chain. Private subtrees are pruned (non-disclosure, same as
// the serve pipeline) and unlisted/private/non-indexable files are omitted.
// Output is ordered by collection then path for stable, diffable sitemaps.
func (h *Handler) collectSitemapURLs(ctx context.Context, collection string) []sitemapURL {
	var cols []*store.Collection
	if collection == "" {
		all, err := h.store.ListCollections(ctx)
		if err != nil {
			if h.log != nil {
				h.log.Error("serve: list collections for sitemap", "error", err)
			}
			return nil
		}
		cols = all
	} else {
		c, err := h.store.GetCollectionByName(ctx, collection)
		if err != nil {
			return nil
		}
		cols = []*store.Collection{c}
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })

	now := time.Now()
	var urls []sitemapURL
	for _, c := range cols {
		layers := []*meta.Document{docOf(c.Metadata)}
		eff := meta.Resolve(h.defaults(), layers...)
		expired := c.ExpiresAt != nil && !now.Before(*c.ExpiresAt)
		// A private or expired collection can serve no public file; skip it
		// wholesale. An unlisted collection is still walked because a descendant
		// may set its own visibility back to public.
		if eff.Visibility == meta.VisibilityPrivate || expired {
			continue
		}
		h.walkSitemap(ctx, c, nil, layers, c.ExpiresAt, now, &urls)
	}
	return urls
}

// walkSitemap recurses parentID's subtree (collection root when nil), appending
// eligible files and pruning private/expired folders.
func (h *Handler) walkSitemap(ctx context.Context, c *store.Collection, parentID *int64, parentLayers []*meta.Document, parentExpiry *time.Time, now time.Time, out *[]sitemapURL) {
	children, err := h.store.ListChildren(ctx, c.ID, parentID)
	if err != nil {
		if h.log != nil {
			h.log.Error("serve: list children for sitemap", "collection", c.Name, "error", err)
		}
		return
	}
	for _, ch := range children {
		layers := append(append([]*meta.Document{}, parentLayers...), docOf(ch.Metadata))
		eff := meta.Resolve(h.defaults(), layers...)
		expiry := meta.Earliest(parentExpiry, ch.ExpiresAt)
		expired := expiry != nil && !now.Before(*expiry)

		if ch.Kind == store.KindFile {
			if !expired && eff.Visibility == meta.VisibilityPublic && eff.Indexable {
				*out = append(*out, sitemapURL{loc: h.fileLoc(c.Name, ch.Path), lastmod: ch.UpdatedAt})
			}
			continue
		}
		// Folder: an expired or private subtree yields no public file, so prune
		// it (a private folder also non-discloses everything beneath it, matching
		// the serve pipeline's ancestor-private guard).
		if expired || eff.Visibility == meta.VisibilityPrivate {
			continue
		}
		h.walkSitemap(ctx, c, &ch.ID, layers, expiry, now, out)
	}
}

// fileLoc builds the absolute public URL for a file, mirroring the management
// API's fileURL so sitemap <loc> values match the crawl API's URLs.
func (h *Handler) fileLoc(collection, p string) string {
	return strings.TrimRight(h.cfg.Server.BaseURL, "/") + "/c/" + collection + "/" + p
}

// sitemapLoc builds the absolute URL of a sitemap endpoint itself, used for the
// per-page links inside a <sitemapindex>.
func (h *Handler) sitemapLoc(selfPath string) string {
	return strings.TrimRight(h.cfg.Server.BaseURL, "/") + selfPath
}
