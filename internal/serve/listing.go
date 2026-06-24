package serve

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/policy"
	"github.com/jdelic/artifactsd/internal/store"
)

// listingEntry is one child in a directory listing (design/05 §2).
type listingEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"` // "file" | "folder"
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

// listingView is the JSON listing body and the html/template data.
type listingView struct {
	Collection string         `json:"collection"`
	Path       string         `json:"path"`
	Entries    []listingEntry `json:"entries"`
}

var listingTemplate = template.Must(template.New("listing").Parse(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>Index of /{{.Collection}}/{{.Path}}</title></head>
<body>
<h1>Index of /{{.Collection}}/{{.Path}}</h1>
<ul>
{{- range .Entries}}
<li><a href="{{.Name}}{{if eq .Type "folder"}}/{{end}}">{{.Name}}{{if eq .Type "folder"}}/{{end}}</a></li>
{{- else}}
<li><em>(empty)</em></li>
{{- end}}
</ul>
</body>
</html>
`))

// serveDirectory handles a directory request (trailing slash or folder
// target, design/05 §1 step 5): listing if directory_listing is enabled and
// access passes, else an index.html fallback, else 404 (design/05 §2). sh is
// non-nil for a share-rooted request and layers its metadata on top at the
// highest precedence, bypassing the private non-disclosure guard exactly like
// serveShareFile (design/14 §1/§5) — see that function's comment for why.
func (h *Handler) serveDirectory(w http.ResponseWriter, r *http.Request, coll *store.Collection, dirPath string, dirNode *store.Node, sh *store.Share) {
	layers, expiry, ancestorPrivate := h.resolveChain(r.Context(), coll, dirPath)
	if sh != nil {
		layers = append(layers, docOf(sh.Metadata))
		expiry = meta.Earliest(expiry, sh.ExpiresAt)
		ancestorPrivate = false
	}
	eff := meta.Resolve(h.defaults(), layers...)
	if sh != nil && eff.Visibility == meta.VisibilityPrivate {
		eff.Visibility = meta.VisibilityUnlisted
	}
	expired := expiry != nil && !time.Now().Before(*expiry)

	decision := policy.Decide(policy.Input{
		Effective: eff, Expired: expired, AncestorPrivate: ancestorPrivate,
		IsDirectory: true, Request: r, OIDC: h.oidc,
	})

	switch decision.Outcome {
	case policy.Unauthorized:
		if decision.Challenge != "" {
			w.Header().Set("WWW-Authenticate", decision.Challenge)
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case policy.Forbidden:
		http.Error(w, "forbidden", http.StatusForbidden)
	case policy.RedirectOIDC:
		http.Redirect(w, r, "/auth/oidc/login?return_to="+url.QueryEscape(r.URL.Path), http.StatusFound)
	case policy.Serve:
		h.renderListing(w, r, coll, dirPath, dirNode, layers, eff, sh != nil)
	case policy.NotFound:
		// Decide returns NotFound here for one of: private/under-private-
		// ancestor, expired, or (only reachable once those are ruled out)
		// directory_listing == false (design/03 §5.5). The first two must stay
		// a hard 404 (non-disclosure); the last falls back to an index
		// document instead of giving up (design/05 §1 step 5).
		if ancestorPrivate || eff.Visibility == meta.VisibilityPrivate || expired {
			http.NotFound(w, r)
			return
		}
		h.serveIndexOrNotFound(w, r, coll, dirPath, sh)
	}
}

// resolveDirNode resolves a trailing-slash (or bare) directory path to its
// folder node, or nil for the collection root. 404s on a missing or
// non-folder node (existence non-disclosure).
func (h *Handler) resolveDirNode(w http.ResponseWriter, r *http.Request, coll *store.Collection, rawPath string) (string, *store.Node, bool) {
	if rawPath == "" {
		return "", nil, true
	}
	p, err := paths.Clean(rawPath)
	if err != nil {
		http.NotFound(w, r)
		return "", nil, false
	}
	node, err := h.store.GetNode(r.Context(), coll.ID, p)
	if errors.Is(err, store.ErrNotFound) || (err == nil && node.Kind != store.KindFolder) {
		http.NotFound(w, r)
		return "", nil, false
	}
	if err != nil {
		h.serverError(w, "lookup node", err)
		return "", nil, false
	}
	return p, node, true
}

// serveIndexOrNotFound serves dirPath/index.html as an ordinary file (so it
// gets its own full access check) if present, else 404.
func (h *Handler) serveIndexOrNotFound(w http.ResponseWriter, r *http.Request, coll *store.Collection, dirPath string, sh *store.Share) {
	indexPath := "index.html"
	if dirPath != "" {
		indexPath = dirPath + "/index.html"
	}
	node, err := h.store.GetNode(r.Context(), coll.ID, indexPath)
	if errors.Is(err, store.ErrNotFound) || (err == nil && node.Kind != store.KindFile) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.serverError(w, "lookup index", err)
		return
	}
	if sh != nil {
		h.serveShareFile(w, r, sh, coll, node, indexPath)
		return
	}
	h.serveFile(w, r, coll, node, indexPath)
}

// renderListing renders a folder's immediate children as HTML or JSON
// (design/05 §2). layers is the directory's own resolved metadata chain
// (already including a share's overrides, if any); each child's effective
// visibility/content-type is computed by extending it with the child's own
// metadata, avoiding a re-walk of the ancestor chain per child. sharePrivateBypass
// mirrors serveDirectory's own private→unlisted bypass for a share-rooted
// request: a child that resolves "private" only because it inherits the
// share's otherwise-private collection/ancestor chain is still listed (the
// share already grants access to the whole subtree); a child explicitly
// marked "unlisted" is always hidden regardless (SPEC §8.1's "don't list me"
// is independent of the share/private non-disclosure mechanism).
func (h *Handler) renderListing(w http.ResponseWriter, r *http.Request, coll *store.Collection, dirPath string, dirNode *store.Node, layers []*meta.Document, dirEff *meta.Effective, sharePrivateBypass bool) {
	var parentID *int64
	if dirNode != nil {
		id := dirNode.ID
		parentID = &id
	}
	children, err := h.store.ListChildren(r.Context(), coll.ID, parentID)
	if err != nil {
		h.serverError(w, "list children", err)
		return
	}

	view := listingView{Collection: coll.Name, Path: dirPath, Entries: []listingEntry{}}
	for _, c := range children {
		childLayers := append(append([]*meta.Document{}, layers...), docOf(c.Metadata))
		childEff := meta.Resolve(h.defaults(), childLayers...)
		hidePrivate := childEff.Visibility == meta.VisibilityPrivate && !sharePrivateBypass
		if hidePrivate || childEff.Visibility == meta.VisibilityUnlisted {
			continue
		}
		entry := listingEntry{Name: c.Name, Path: c.Path, UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339)}
		if c.Kind == store.KindFolder {
			entry.Type = "folder"
		} else {
			entry.Type = "file"
			if c.Size != nil {
				entry.Size = *c.Size
			}
			entry.ContentType = detectContentType(childEff, c)
		}
		view.Entries = append(view.Entries, entry)
	}

	if tag := robotsTag(dirEff); tag != "" {
		w.Header().Set("X-Robots-Tag", tag)
	}

	if wantsJSONListing(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(view)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = listingTemplate.Execute(w, view)
}

// wantsJSONListing picks JSON over the default HTML listing (design/05 §1
// step 5): an explicit ?format=json always wins; otherwise an Accept header
// preferring application/json over text/html does. Agents are steered toward
// the dedicated crawl API instead (design/05 §5); this is a convenience.
func wantsJSONListing(r *http.Request) bool {
	if r.URL.Query().Get("format") == "json" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}
