// Package serve implements the public serving pipeline for /c (and later /s):
// resolve target → effective metadata → policy decision → stream blob with
// content-type, conditional/range support, and crawler headers (design/05 §1).
package serve

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/policy"
	"github.com/jdelic/artifactsd/internal/store"
)

// Deps are the serving handler's dependencies.
type Deps struct {
	Store  *store.Store
	Blob   blob.Store
	Config *config.Config
	Audit  *audit.Recorder
	OIDC   *auth.OIDCProviderCache
	Logger *slog.Logger
}

// Handler serves public content.
type Handler struct {
	store        *store.Store
	blob         blob.Store
	cfg          *config.Config
	audit        *audit.Recorder
	oidc         *auth.OIDCProviderCache
	log          *slog.Logger
	lastAccess   *lastAccessUpdater
	robotsCache  *robotsCache
	sitemapCache *sitemapCache
}

// New builds a serving handler.
func New(d Deps) *Handler {
	return &Handler{
		store: d.Store, blob: d.Blob, cfg: d.Config, audit: d.Audit, oidc: d.OIDC, log: d.Logger,
		lastAccess:   newLastAccessUpdater(d.Store, d.Config.Limits.AccessUpdateThrottle, d.Logger),
		robotsCache:  newRobotsCache(),
		sitemapCache: newSitemapCache(),
	}
}

// WithClock overrides the last-access throttle's clock (for tests).
func (h *Handler) WithClock(now func() time.Time) *Handler {
	h.lastAccess.withClock(now)
	return h
}

// ServeCollection handles GET|HEAD /c/{collection}/{path...} (design/05 §1).
func (h *Handler) ServeCollection(w http.ResponseWriter, r *http.Request) {
	coll, err := h.store.GetCollectionByName(r.Context(), r.PathValue("collection"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.serverError(w, "lookup collection", err)
		return
	}

	rawPath := r.PathValue("path")
	// The collection-root sitemap is a generated endpoint (design/05 §4),
	// dispatched here rather than as its own route to avoid a net/http
	// pattern-precedence collision with the /c file wildcard; it shadows any
	// file literally named sitemap.xml at the collection root.
	if rawPath == "sitemap.xml" {
		h.SitemapCollection(w, r)
		return
	}
	if rawPath == "" || strings.HasSuffix(r.URL.Path, "/") {
		dirPath, dirNode, ok := h.resolveDirNode(w, r, coll, rawPath)
		if !ok {
			return
		}
		h.serveDirectory(w, r, coll, dirPath, dirNode, nil)
		return
	}
	p, err := paths.Clean(rawPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	node, err := h.store.GetNode(r.Context(), coll.ID, p)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.serverError(w, "lookup node", err)
		return
	}
	if node.Kind != store.KindFile {
		h.serveDirectory(w, r, coll, p, node, nil)
		return
	}
	h.serveFile(w, r, coll, node, p)
}

// ServeShare handles GET|HEAD /s/{share_id}/{path...}: resolve share_id to its
// (collection, base path), append+revalidate the optional tail stays within
// that subtree, then run the same pipeline as /c with the share's metadata
// layered on top at the highest precedence (design/05 §1 step 1/3, design/04
// §6).
func (h *Handler) ServeShare(w http.ResponseWriter, r *http.Request) {
	sh, coll, ok := h.lookupActiveShare(w, r)
	if !ok {
		return
	}

	p, ok := h.resolveSharePath(w, r, sh)
	if !ok {
		return
	}
	if p == "" {
		// Directory request at the share's own root (a whole-collection share
		// with no tail).
		h.serveDirectory(w, r, coll, "", nil, sh)
		return
	}

	node, err := h.store.GetNode(r.Context(), coll.ID, p)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r) // existence non-disclosure
		return
	}
	if err != nil {
		h.serverError(w, "lookup node", err)
		return
	}
	if node.Kind != store.KindFile {
		h.serveDirectory(w, r, coll, p, node, sh)
		return
	}
	h.serveShareFile(w, r, sh, coll, node, p)
}

// lookupActiveShare resolves {share_id} and its collection, rejecting a
// revoked or expired share as 404 (existence non-disclosure, SPEC §17).
func (h *Handler) lookupActiveShare(w http.ResponseWriter, r *http.Request) (*store.Share, *store.Collection, bool) {
	sh, err := h.store.GetShare(r.Context(), r.PathValue("share_id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return nil, nil, false
	}
	if err != nil {
		h.serverError(w, "lookup share", err)
		return nil, nil, false
	}
	if sh.RevokedAt != nil || (sh.ExpiresAt != nil && !time.Now().Before(*sh.ExpiresAt)) {
		http.NotFound(w, r)
		return nil, nil, false
	}

	coll, err := h.store.GetCollection(r.Context(), sh.CollectionID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return nil, nil, false
	}
	if err != nil {
		h.serverError(w, "lookup share collection", err)
		return nil, nil, false
	}
	return sh, coll, true
}

// resolveSharePath resolves the optional tail path under a share's target,
// re-validating it stays within the share's subtree (design/05 §1 step 1): the
// tail is cleaned on its own — paths.Clean rejects any ".." segment outright,
// it never resolves one upward — before being concatenated onto the share's
// fixed TargetPath, so a cleaned tail can never escape it. Returns "" for a
// directory request at the share's own root (only reachable for a
// whole-collection share with no tail).
func (h *Handler) resolveSharePath(w http.ResponseWriter, r *http.Request, sh *store.Share) (string, bool) {
	tail := r.PathValue("path")
	if tail == "" {
		return sh.TargetPath, true
	}
	if sh.TargetKind == "file" {
		// A file-target share has nothing "under" it to address.
		http.NotFound(w, r)
		return "", false
	}
	cleaned, err := paths.Clean(tail)
	if err != nil {
		http.NotFound(w, r)
		return "", false
	}
	if sh.TargetPath == "" {
		return cleaned, true
	}
	return sh.TargetPath + "/" + cleaned, true
}

// serveShareFile resolves effective metadata through the normal collection→
// ancestor→file chain, then layers the share's own metadata on top at the
// highest precedence (design/05 §1 step 3). It also bypasses the
// ancestor/own-private non-disclosure guard: a share is itself the access
// grant (design/14 §1/§5 — capability sharing needs no additional auth), so an
// object that would otherwise resolve "private" is served as if "unlisted"
// instead, unless the share's own metadata configures additional auth
// requirements (still enforced normally via eff.RequiresAuth()).
func (h *Handler) serveShareFile(w http.ResponseWriter, r *http.Request, sh *store.Share, coll *store.Collection, node *store.Node, p string) {
	layers, chainExpiry, _ := h.resolveChain(r.Context(), coll, p)
	layers = append(layers, docOf(sh.Metadata))
	expiry := meta.Earliest(chainExpiry, sh.ExpiresAt)
	expired := expiry != nil && !time.Now().Before(*expiry)

	eff := meta.Resolve(h.defaults(), layers...)
	if eff.Visibility == meta.VisibilityPrivate {
		eff.Visibility = meta.VisibilityUnlisted
	}

	decision := policy.Decide(policy.Input{
		Effective: eff, Expired: expired, AncestorPrivate: false,
		IsDirectory: false, Request: r, OIDC: h.oidc,
	})
	h.dispatch(w, r, coll, node, eff, decision)
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, coll *store.Collection, node *store.Node, p string) {
	layers, expiry, ancestorPrivate := h.resolveChain(r.Context(), coll, p)
	eff := meta.Resolve(h.defaults(), layers...)
	expired := expiry != nil && !time.Now().Before(*expiry)

	decision := policy.Decide(policy.Input{
		Effective: eff, Expired: expired, AncestorPrivate: ancestorPrivate,
		IsDirectory: false, Request: r, OIDC: h.oidc,
	})
	h.dispatch(w, r, coll, node, eff, decision)
}

// dispatch acts on a policy decision: 404/401/403/redirect, or stream the
// file. Shared by the /c and /s pipelines (design/05 §1 steps 4-6).
func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request, coll *store.Collection, node *store.Node, eff *meta.Effective, decision policy.Decision) {
	switch decision.Outcome {
	case policy.NotFound:
		http.NotFound(w, r)
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
		h.stream(w, r, coll, node, eff, &decision)
	}
}

func (h *Handler) stream(w http.ResponseWriter, r *http.Request, coll *store.Collection, node *store.Node, eff *meta.Effective, decision *policy.Decision) {
	if node.BlobID == nil {
		http.NotFound(w, r)
		return
	}
	rc, err := h.blob.Open(r.Context(), blob.Ref(*node.BlobID))
	if err != nil {
		h.serverError(w, "open blob", err)
		return
	}
	defer func() { _ = rc.Close() }()

	for k, v := range meta.FilterHeaders(eff.Headers) {
		w.Header().Set(k, v)
	}
	// Sandbox artifact-controlled content into an opaque origin so its JavaScript
	// cannot drive or read the same-origin admin UI (design/14 §7, artifactsd-c63).
	// Added (not Set) as our own CSP header so any author-supplied CSP is layered
	// on top — sandbox policies are intersected by the browser, so an author CSP
	// can only tighten, never loosen, this. Harmless on non-document responses
	// (the directive only constrains browsing contexts), so applied broadly to
	// also cover content the browser would otherwise sniff as HTML. Trusted
	// content opts out via content.sandbox=false (then it keeps its real origin).
	if eff.Sandbox {
		w.Header().Add("Content-Security-Policy", "sandbox allow-scripts allow-downloads")
	}
	if ct := detectContentType(eff, node); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if tag := robotsTag(eff); tag != "" {
		w.Header().Set("X-Robots-Tag", tag)
	}
	if node.Checksum != nil {
		w.Header().Set("ETag", `"`+*node.Checksum+`"`)
	}
	w.Header().Set("Accept-Ranges", "bytes")

	// Audit successful authenticated access to a protected object (decision N6).
	if decision.Subject != "" && h.audit != nil {
		_ = h.audit.RecordAccess(r.Context(), decision.Subject, coll.Name+"/"+node.Path)
	}
	// Throttled last_access_at update, feeding delete_after_last_access
	// (design/05 §1 step 7, design/08 §3) — recorded before streaming starts so
	// it isn't gated on the client reading the full response.
	h.lastAccess.touch(r.Context(), node.ID)

	http.ServeContent(w, r, node.Name, node.UpdatedAt, rc)
}

// resolveChain loads the metadata layers (collection → ancestor folders → file)
// and computes the effective absolute expiry (earliest in the chain via the
// denormalized columns) and whether any ancestor is private.
func (h *Handler) resolveChain(ctx context.Context, coll *store.Collection, p string) (layers []*meta.Document, expiry *time.Time, ancestorPrivate bool) {
	layers = append(layers, docOf(coll.Metadata))
	expiry = coll.ExpiresAt
	if coll.Visibility == meta.VisibilityPrivate {
		ancestorPrivate = true
	}

	segs := strings.Split(p, "/")
	prefix := ""
	for i, seg := range segs {
		if prefix == "" {
			prefix = seg
		} else {
			prefix += "/" + seg
		}
		n, err := h.store.GetNode(ctx, coll.ID, prefix)
		if err != nil {
			continue
		}
		layers = append(layers, docOf(n.Metadata))
		expiry = meta.Earliest(expiry, n.ExpiresAt)
		// Ancestors only (not the file itself) drive the ancestor guard.
		if i < len(segs)-1 && n.Visibility != nil && *n.Visibility == meta.VisibilityPrivate {
			ancestorPrivate = true
		}
	}
	return layers, expiry, ancestorPrivate
}

func (h *Handler) defaults() meta.Defaults {
	return meta.Defaults{
		Visibility:       string(h.cfg.Policy.Visibility),
		DirectoryListing: h.cfg.Policy.DirectoryListing,
		Crawlable:        h.cfg.Policy.Crawlable,
		Indexable:        h.cfg.Policy.Indexable,
		Sandbox:          h.cfg.Policy.Sandbox,
	}
}

func (h *Handler) serverError(w http.ResponseWriter, what string, err error) {
	if h.log != nil {
		h.log.Error("serve: "+what, "error", err)
	}
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func docOf(raw []byte) *meta.Document {
	d, err := meta.FromJSON(raw)
	if err != nil {
		return nil
	}
	return d
}

// detectContentType resolves the content type (design/05 §1): explicit override
// → stored type → extension map. Empty lets http.ServeContent sniff.
func detectContentType(eff *meta.Effective, node *store.Node) string {
	if eff.ContentType != "" {
		return eff.ContentType
	}
	if node.ContentType != nil && *node.ContentType != "" {
		return *node.ContentType
	}
	if ext := path.Ext(node.Name); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	return ""
}

// robotsTag composes the X-Robots-Tag value: "noindex" when not indexable, plus
// any configured tags.
func robotsTag(eff *meta.Effective) string {
	tags := make([]string, 0, len(eff.XRobotsTag)+1)
	if !eff.Indexable {
		tags = append(tags, "noindex")
	}
	tags = append(tags, eff.XRobotsTag...)
	return strings.Join(tags, ", ")
}
