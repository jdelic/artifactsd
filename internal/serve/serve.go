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
	store *store.Store
	blob  blob.Store
	cfg   *config.Config
	audit *audit.Recorder
	oidc  *auth.OIDCProviderCache
	log   *slog.Logger
}

// New builds a serving handler.
func New(d Deps) *Handler {
	return &Handler{store: d.Store, blob: d.Blob, cfg: d.Config, audit: d.Audit, oidc: d.OIDC, log: d.Logger}
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
	if rawPath == "" || strings.HasSuffix(r.URL.Path, "/") {
		// Directory requests (listing/index) land in artifactsd-c4f.
		http.NotFound(w, r)
		return
	}
	p, err := paths.Clean(rawPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	node, err := h.store.GetNode(r.Context(), coll.ID, p)
	if errors.Is(err, store.ErrNotFound) || (err == nil && node.Kind != store.KindFile) {
		http.NotFound(w, r) // existence non-disclosure
		return
	}
	if err != nil {
		h.serverError(w, "lookup node", err)
		return
	}
	h.serveFile(w, r, coll, node, p)
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, coll *store.Collection, node *store.Node, p string) {
	layers, expiry, ancestorPrivate := h.resolveChain(r.Context(), coll, p)
	eff := meta.Resolve(h.defaults(), layers...)
	expired := expiry != nil && !time.Now().Before(*expiry)

	decision := policy.Decide(policy.Input{
		Effective: eff, Expired: expired, AncestorPrivate: ancestorPrivate,
		IsDirectory: false, Request: r, OIDC: h.oidc,
	})

	switch decision.Outcome {
	case policy.NotFound:
		http.NotFound(w, r)
		return
	case policy.Unauthorized:
		if decision.Challenge != "" {
			w.Header().Set("WWW-Authenticate", decision.Challenge)
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	case policy.Forbidden:
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case policy.RedirectOIDC:
		http.Redirect(w, r, "/auth/oidc/login?return_to="+url.QueryEscape(r.URL.Path), http.StatusFound)
		return
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
