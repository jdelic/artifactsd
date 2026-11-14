package uploadrequest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/sandbox"
	"github.com/jdelic/artifactsd/internal/store"
)

// ErrUploaderAccessNeedsVisibility is returned by PrepareFileMetadata when a
// link asks to show file URLs to uploaders but its files would not be public
// or unlisted.
var ErrUploaderAccessNeedsVisibility = errors.New(
	"allow_uploader_access requires file_metadata.access.visibility public or unlisted")

// PrepareFileMetadata applies the upload-link defaults to an already validated
// file_metadata document (artifactsd-m9e.24): when it sets no
// access.visibility, files uploaded through the link are private. With
// allowUploaderAccess the visibility must be public or unlisted. It returns
// the document to store.
func PrepareFileMetadata(raw json.RawMessage, allowUploaderAccess bool) (json.RawMessage, error) {
	doc, err := meta.FromJSON(raw)
	if err != nil {
		return nil, err
	}
	vis := applyDefaultVisibility(doc)
	if allowUploaderAccess && vis != meta.VisibilityPublic && vis != meta.VisibilityUnlisted {
		return nil, ErrUploaderAccessNeedsVisibility
	}
	return doc.JSON()
}

// applyDefaultVisibility sets access.visibility to private when doc leaves it
// unset, and returns the resulting visibility.
func applyDefaultVisibility(doc *meta.Document) string {
	if doc.Access == nil {
		doc.Access = &meta.Access{}
	}
	if doc.Access.Visibility == nil {
		v := meta.VisibilityPrivate
		doc.Access.Visibility = &v
	}
	return *doc.Access.Visibility
}

// resolveFile resolves a committed file's effective metadata (server defaults
// → collection → ancestor folders → file) and whether the collection or an
// ancestor folder is private. Lookup errors are returned.
func (h *Handler) resolveFile(ctx context.Context, c *store.Collection, nodePath string, fileDoc *meta.Document) (*meta.Effective, bool, error) {
	private := c.Visibility == meta.VisibilityPrivate
	layers := []*meta.Document{docOrNil(c.Metadata)}
	segs := strings.Split(nodePath, "/")
	prefix := ""
	for _, seg := range segs[:len(segs)-1] {
		if prefix == "" {
			prefix = seg
		} else {
			prefix += "/" + seg
		}
		n, err := h.store.GetNode(ctx, c.ID, prefix)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if n.Visibility != nil && *n.Visibility == meta.VisibilityPrivate {
			private = true
		}
		layers = append(layers, docOrNil(n.Metadata))
	}
	layers = append(layers, fileDoc)
	eff := meta.Resolve(meta.Defaults{
		Visibility:       string(h.cfg.Policy.Visibility),
		DirectoryListing: h.cfg.Policy.DirectoryListing,
		Crawlable:        h.cfg.Policy.Crawlable,
		Indexable:        h.cfg.Policy.Indexable,
		Sandbox:          h.cfg.Policy.Sandbox,
	}, layers...)
	return eff, private, nil
}

// publicURLAllowed reports whether a committed file is reachable anonymously
// by URL: its effective visibility is public or unlisted and neither the
// collection nor any ancestor folder is private. Lookup errors fail closed.
func (h *Handler) publicURLAllowed(ctx context.Context, c *store.Collection, nodePath string, fileDoc *meta.Document) bool {
	eff, private, err := h.resolveFile(ctx, c, nodePath, fileDoc)
	if err != nil || private {
		return false
	}
	return eff.Visibility == meta.VisibilityPublic || eff.Visibility == meta.VisibilityUnlisted
}

// sandboxWarning explains, for a file uploaded through a link (always
// untrusted content), that it is served sandboxed although the sandbox is
// turned off where it lives (artifactsd-7tl.9). Empty when not applicable.
func (h *Handler) sandboxWarning(ctx context.Context, c *store.Collection, nodePath string, fileDoc *meta.Document) string {
	eff, _, err := h.resolveFile(ctx, c, nodePath, fileDoc)
	if err != nil || sandbox.For(eff.Sandbox, true, false) != sandbox.AwaitingApproval {
		return ""
	}
	return "served sandboxed until an admin re-approves it: " + sandbox.AwaitingApprovalNote
}

func docOrNil(raw []byte) *meta.Document {
	d, err := meta.FromJSON(raw)
	if err != nil {
		return nil
	}
	return d
}
