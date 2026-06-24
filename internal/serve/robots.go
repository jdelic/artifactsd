package serve

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jdelic/artifactsd/internal/meta"
)

// robotsCacheTTL bounds how stale a generated robots.txt may be after a
// metadata change (design/05 §3/§6). Not exposed as config — a short fixed
// window is enough headroom for this cheap, rarely-fetched endpoint.
const robotsCacheTTL = 30 * time.Second

// robotsCache holds the generated root robots.txt body.
type robotsCache struct {
	mu      sync.Mutex
	body    []byte
	expires time.Time
}

func newRobotsCache() *robotsCache {
	return &robotsCache{}
}

func (rc *robotsCache) get(now time.Time, compute func() []byte) []byte {
	rc.mu.Lock()
	if rc.body != nil && now.Before(rc.expires) {
		body := rc.body
		rc.mu.Unlock()
		return body
	}
	rc.mu.Unlock()

	body := compute()

	rc.mu.Lock()
	rc.body, rc.expires = body, now.Add(robotsCacheTTL)
	rc.mu.Unlock()
	return body
}

// RobotsRoot handles GET /robots.txt (design/05 §3, SPEC §10.3): always
// disallows /api/ and the admin path, then Allow/Disallow per collection by
// its own effective indexable flag — private/unlisted collections are always
// disallowed regardless — and a Sitemap: line when any collection is allowed.
// Deliberately root-only: the robots.txt convention (RFC 9309) only
// recognizes the file at the domain root, so there is no equivalent
// per-collection endpoint — per-collection policy is folded into this
// response's Allow/Disallow lines instead.
func (h *Handler) RobotsRoot(w http.ResponseWriter, r *http.Request) {
	body := h.robotsCache.get(time.Now(), func() []byte {
		return h.buildRootRobots(r.Context())
	})
	writeRobots(w, body)
}

func (h *Handler) buildRootRobots(ctx context.Context) []byte {
	cols, err := h.store.ListCollections(ctx)
	if err != nil {
		if h.log != nil {
			h.log.Error("serve: list collections for robots.txt", "error", err)
		}
		cols = nil
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })

	var b strings.Builder
	b.WriteString("User-agent: *\n")
	b.WriteString("Disallow: /api/\n")
	b.WriteString("Disallow: " + strings.TrimRight(h.cfg.Admin.Path, "/") + "/\n")

	anyAllowed := false
	for _, c := range cols {
		eff := meta.Resolve(h.defaults(), docOf(c.Metadata))
		hidden := c.Visibility == meta.VisibilityPrivate || c.Visibility == meta.VisibilityUnlisted
		if !hidden && eff.Indexable {
			b.WriteString("Allow: /c/" + c.Name + "/\n")
			anyAllowed = true
		} else {
			b.WriteString("Disallow: /c/" + c.Name + "/\n")
		}
	}
	if anyAllowed {
		b.WriteString("Sitemap: " + strings.TrimRight(h.cfg.Server.BaseURL, "/") + "/sitemap.xml\n")
	}
	return []byte(b.String())
}

func writeRobots(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(body)
}
