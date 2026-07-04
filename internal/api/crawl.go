package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/store"
)

// crawlState carries the resolved metadata chain down a crawl walk, so each
// node is resolved by extending its parent's chain rather than re-walking from
// the collection root (design/05 §5; same approach as internal/serve/listing.go).
// ancestorPrivate mirrors internal/serve's own-vs-ancestor split: it reflects
// only ancestors, not the node these layers ultimately resolve to.
type crawlState struct {
	layers          []*meta.Document
	ancestorPrivate bool
	expiry          *time.Time
}

// crawlEntry is one file/folder in a crawl response (SPEC §10.5). Folder
// entries carry only path/type/crawlable/indexable — agents drill into a
// folder via ?path= rather than receiving its contents inline.
type crawlEntry struct {
	Path        string  `json:"path"`
	Type        string  `json:"type"`
	URL         string  `json:"url,omitempty"`
	ContentType string  `json:"content_type,omitempty"`
	Size        *int64  `json:"size,omitempty"`
	UpdatedAt   string  `json:"updated_at,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
	Crawlable   bool    `json:"crawlable"`
	Indexable   bool    `json:"indexable"`
}

// crawlResponse is the per-collection crawl response (SPEC §10.5).
type crawlResponse struct {
	Collection string       `json:"collection"`
	BaseURL    string       `json:"base_url"`
	Crawlable  bool         `json:"crawlable"`
	Indexable  bool         `json:"indexable"`
	Entries    []crawlEntry `json:"entries"`
}

// crawlCollectionSummary is one collection in the root crawl listing
// (design/04 §7: "all crawlable collections").
type crawlCollectionSummary struct {
	Collection string `json:"collection"`
	BaseURL    string `json:"base_url"`
	Crawlable  bool   `json:"crawlable"`
	Indexable  bool   `json:"indexable"`
}

// crawlRoot handles GET /api/v1/crawl: the policy-filtered list of
// collections an agent may crawl. Distinct from GET /api/v1/collections
// (unfiltered, admin) — private/expired collections are never listed here,
// and non-crawlable collections are omitted unless the caller has admin scope
// (design/04 §7, design/05 §5).
func (h *Handler) crawlRoot(w http.ResponseWriter, r *http.Request) {
	cols, err := h.store.ListCollections(r.Context())
	if err != nil {
		h.internalServerError(w, "list collections", err)
		return
	}
	defs := h.policyDefaults()
	admin := isAdmin(r)

	items := make([]crawlCollectionSummary, 0, len(cols))
	for _, c := range cols {
		eff := meta.Resolve(defs, docOrNil(c.Metadata))
		expired := c.ExpiresAt != nil && !time.Now().Before(*c.ExpiresAt)
		if eff.Visibility == meta.VisibilityPrivate || expired {
			continue // never listed, regardless of scope
		}
		if !eff.Crawlable && !admin {
			continue
		}
		items = append(items, crawlCollectionSummary{
			Collection: c.Name, BaseURL: h.dirURL(c.Name, ""),
			Crawlable: eff.Crawlable, Indexable: eff.Indexable,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": items})
}

// crawlCollection handles GET /api/v1/collections/{c}/crawl?path=&depth=: a
// policy-filtered, depth-limited tree under path (default the collection
// root), so agents can enumerate structure without scraping HTML directory
// listings (design/05 §5).
func (h *Handler) crawlCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := h.lookupCollection(w, r)
	if !ok {
		return
	}
	basePath := strings.Trim(r.URL.Query().Get("path"), "/")
	if basePath != "" {
		cleaned, ok := h.cleanPathStr(w, basePath)
		if !ok {
			return
		}
		basePath = cleaned
	}
	depth, err := parseDepthParam(r.URL.Query().Get("depth"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
		return
	}
	if depth == 0 {
		depth = -1 // crawlEntries' internal unlimited sentinel
	}
	defs := h.policyDefaults()
	admin := isAdmin(r)

	state, baseNode, err := h.resolveCrawlBase(r.Context(), c, basePath, defs)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeNotFound, "path does not exist")
		return
	}
	if err != nil {
		h.internalServerError(w, "resolve crawl base", err)
		return
	}
	baseEff := meta.Resolve(defs, state.layers...)
	expired := state.expiry != nil && !time.Now().Before(*state.expiry)
	if state.ancestorPrivate || baseEff.Visibility == meta.VisibilityPrivate || expired {
		// Never disclose a private/expired base, same as the public serve path.
		writeError(w, http.StatusNotFound, CodeNotFound, "path does not exist")
		return
	}
	if baseNode != nil && baseNode.Kind != store.KindFolder {
		writeError(w, http.StatusConflict, CodeConflict, "path is a file, not a folder")
		return
	}

	var parentID *int64
	if baseNode != nil {
		parentID = &baseNode.ID
	}
	entries, err := h.crawlEntries(r.Context(), c, parentID, state, depth, admin, defs)
	if err != nil {
		h.internalServerError(w, "list crawl entries", err)
		return
	}
	writeJSON(w, http.StatusOK, crawlResponse{
		Collection: c.Name, BaseURL: h.dirURL(c.Name, basePath),
		Crawlable: baseEff.Crawlable, Indexable: baseEff.Indexable,
		Entries: entries,
	})
}

// resolveCrawlBase resolves basePath's metadata chain (collection → each
// ancestor folder) and whether any of those ancestors is private, mirroring
// internal/serve.Handler.resolveChain. Returns ErrNotFound if any segment is
// missing. baseNode is nil for the collection root.
func (h *Handler) resolveCrawlBase(ctx context.Context, c *store.Collection, basePath string, defs meta.Defaults) (crawlState, *store.Node, error) {
	state := crawlState{layers: []*meta.Document{docOrNil(c.Metadata)}, expiry: c.ExpiresAt}
	if basePath == "" {
		return state, nil, nil
	}

	segs := strings.Split(basePath, "/")
	prefix := ""
	var node *store.Node
	for i, seg := range segs {
		if prefix == "" {
			prefix = seg
		} else {
			prefix += "/" + seg
		}
		n, err := h.store.GetNode(ctx, c.ID, prefix)
		if err != nil {
			return state, nil, err
		}
		state.layers = append(state.layers, docOrNil(n.Metadata))
		state.expiry = meta.Earliest(state.expiry, n.ExpiresAt)
		if i < len(segs)-1 {
			// An intermediate ancestor: its own private flag becomes the
			// ancestor guard for everything below it.
			if meta.Resolve(defs, state.layers...).Visibility == meta.VisibilityPrivate {
				state.ancestorPrivate = true
			}
		}
		node = n
	}
	return state, node, nil
}

// crawlEntries lists parentID's children (collection root when nil),
// filtering private/expired/non-crawlable entries and recursing into folders
// up to depth levels (negative = unlimited, design/05 §5).
func (h *Handler) crawlEntries(ctx context.Context, c *store.Collection, parentID *int64, state crawlState, depth int, admin bool, defs meta.Defaults) ([]crawlEntry, error) {
	children, err := h.store.ListChildren(ctx, c.ID, parentID)
	if err != nil {
		return nil, err
	}

	entries := make([]crawlEntry, 0, len(children))
	for _, ch := range children {
		childLayers := append(append([]*meta.Document{}, state.layers...), docOrNil(ch.Metadata))
		eff := meta.Resolve(defs, childLayers...)
		childExpiry := meta.Earliest(state.expiry, ch.ExpiresAt)
		expired := childExpiry != nil && !time.Now().Before(*childExpiry)
		if state.ancestorPrivate || eff.Visibility == meta.VisibilityPrivate || expired {
			continue // never listed, regardless of scope
		}
		if !eff.Crawlable && !admin {
			continue
		}
		entries = append(entries, h.toCrawlEntry(c.Name, ch, eff))

		recurse := ch.Kind == store.KindFolder && (depth < 0 || depth > 1)
		if recurse {
			nextDepth := depth
			if depth > 0 {
				nextDepth--
			}
			childState := crawlState{layers: childLayers, ancestorPrivate: state.ancestorPrivate, expiry: childExpiry}
			sub, err := h.crawlEntries(ctx, c, &ch.ID, childState, nextDepth, admin, defs)
			if err != nil {
				return nil, err
			}
			entries = append(entries, sub...)
		}
	}
	return entries, nil
}

func (h *Handler) toCrawlEntry(collection string, n *store.Node, eff *meta.Effective) crawlEntry {
	if n.Kind == store.KindFolder {
		return crawlEntry{Path: n.Path + "/", Type: "folder", Crawlable: eff.Crawlable, Indexable: eff.Indexable}
	}
	e := crawlEntry{
		Path: n.Path, Type: "file", URL: h.fileURL(collection, n.Path),
		Size: n.Size, UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339),
		Crawlable: eff.Crawlable, Indexable: eff.Indexable,
	}
	if ct := crawlContentType(eff, n); ct != "" {
		e.ContentType = ct
	}
	if n.ExpiresAt != nil {
		s := n.ExpiresAt.UTC().Format(time.RFC3339)
		e.ExpiresAt = &s
	}
	return e
}

// crawlContentType mirrors internal/serve's detectContentType (explicit
// override → stored type → extension map), kept as a small local copy rather
// than a cross-package dependency on the serving pipeline.
func crawlContentType(eff *meta.Effective, n *store.Node) string {
	if eff.ContentType != "" {
		return eff.ContentType
	}
	if n.ContentType != nil && *n.ContentType != "" {
		return *n.ContentType
	}
	if ext := path.Ext(n.Name); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	return ""
}

// dirURL builds a directory URL under /c/{collection}/, with a trailing slash
// (SPEC §10.5 example: "https://.../sales-demo-2026/").
func (h *Handler) dirURL(collection, p string) string {
	u := strings.TrimRight(h.baseURL, "/") + "/c/" + collection + "/"
	if p != "" {
		u += p + "/"
	}
	return u
}

// policyDefaults is the server-defaults base layer for metadata resolution
// (design/12 §3), shared by metaView and the crawl API.
func (h *Handler) policyDefaults() meta.Defaults {
	return meta.Defaults{
		Visibility:       string(h.cfg.Policy.Visibility),
		DirectoryListing: h.cfg.Policy.DirectoryListing,
		Crawlable:        h.cfg.Policy.Crawlable,
		Indexable:        h.cfg.Policy.Indexable,
	}
}

// isAdmin reports whether the request's authenticated principal has admin
// scope — non-crawlable objects are visible to admin-scoped crawl callers only
// (SPEC §10.1).
func isAdmin(r *http.Request) bool {
	p := auth.PrincipalFromContext(r.Context())
	return p != nil && p.Scopes.Has(auth.ScopeAdmin)
}
