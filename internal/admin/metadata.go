package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/store"
)

// authEntryRow is a redacted, read-only view of one configured auth entry.
type authEntryRow struct {
	Type   string
	Detail string
}

// metaPanelData backs the "meta_panel" partial: the structured fields form,
// a read-only auth summary, and the raw JSON view (design/09 §3).
type metaPanelData struct {
	AdminPath  string
	Collection string
	Path       string
	Label      string

	Visibility            string
	DirectoryListing      string
	Crawlable             string
	Indexable             string
	ContentType           string
	Headers               string
	ExpiresAt             string
	DeleteAfterCreation   string
	DeleteAfterLastAccess string

	AuthEntries []authEntryRow
	RawJSON     string
	Error       string
	Saved       bool
}

var metaFKeys = []fkey{{"Alt-S", "Save"}, {"Alt-L", "Logout"}}

// editMetadata renders the metadata editor for a collection (path="") or one
// of its nodes (design/09 §3).
func (h *Handler) editMetadata(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	p, ok := h.cleanDir(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	raw, err := h.loadOwnMetadata(r.Context(), c, p)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "path does not exist", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "loading metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := buildMetaPanelData(h.cfg.Admin.Path, name, p, raw, "")
	if err != nil {
		http.Error(w, "loading metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "meta", metaFKeys, "READY", data.Label, data)
}

// saveMetaFields handles the structured-fields form: content type/headers,
// visibility/directory-listing, crawl, and expiry. Existing auth entries and
// robots rules are preserved untouched — they're edited via the raw view
// (design/09 §3 covers them as the "raw view" counterpart to this form).
func (h *Handler) saveMetaFields(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	p, ok := h.cleanDir(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	ctx := r.Context()
	raw, err := h.loadOwnMetadata(ctx, c, p)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "path does not exist", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "loading metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	existing, err := meta.FromJSON(raw)
	if err != nil {
		http.Error(w, "loading metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	headers, err := parseHeaders(r.FormValue("headers"))
	if err != nil {
		h.renderMetaPanelError(w, name, p, raw, "invalid headers: "+err.Error())
		return
	}
	doc := &meta.Document{
		Content: buildContent(r.FormValue("content_type"), headers),
		Access:  buildAccess(r.FormValue("visibility"), r.FormValue("directory_listing"), existingAuth(existing)),
		Crawl:   buildCrawl(r.FormValue("crawlable"), r.FormValue("indexable"), existingRobots(existing)),
		Expiry: buildExpiry(
			r.FormValue("expires_at"), r.FormValue("delete_after_creation"), r.FormValue("delete_after_last_access"),
		),
	}
	if err := doc.Validate(); err != nil {
		h.renderMetaPanelError(w, name, p, raw, err.Error())
		return
	}
	if err := h.saveMetaDoc(ctx, c, p, doc); err != nil {
		h.renderMetaPanelError(w, name, p, raw, "saving metadata: "+err.Error())
		return
	}
	h.renderMetaPanelSaved(w, name, p, doc)
}

// saveMetaRaw handles the raw-JSON form: the document is replaced wholesale,
// so it's the one path that can add/remove/rotate auth entries and robots
// rules. Secrets are hashed before persisting (design/06 §2).
func (h *Handler) saveMetaRaw(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	p, ok := h.cleanDir(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	body := r.FormValue("raw")
	doc, err := meta.FromJSON([]byte(body))
	if err != nil {
		h.renderMetaRawError(w, name, p, body, err.Error())
		return
	}
	if err := doc.Validate(); err != nil {
		h.renderMetaRawError(w, name, p, body, err.Error())
		return
	}
	if err := doc.HashSecrets(); err != nil {
		h.renderMetaRawError(w, name, p, body, "hashing secrets: "+err.Error())
		return
	}
	if err := h.saveMetaDoc(r.Context(), c, p, doc); err != nil {
		h.renderMetaRawError(w, name, p, body, "saving metadata: "+err.Error())
		return
	}
	h.renderMetaPanelSaved(w, name, p, doc)
}

func (h *Handler) saveMetaDoc(ctx context.Context, c *store.Collection, p string, doc *meta.Document) error {
	out, err := doc.JSON()
	if err != nil {
		return err
	}
	return h.saveOwnMetadata(ctx, c, p, out)
}

// loadOwnMetadata returns the raw metadata JSON owned by the collection
// (p == "") or one of its nodes.
func (h *Handler) loadOwnMetadata(ctx context.Context, c *store.Collection, p string) (json.RawMessage, error) {
	if p == "" {
		return c.Metadata, nil
	}
	node, err := h.store.GetNode(ctx, c.ID, p)
	if err != nil {
		return nil, err
	}
	return node.Metadata, nil
}

// saveOwnMetadata persists raw as the collection's (p == "") or a node's own
// metadata, recomputing the denormalized visibility/expiry columns.
func (h *Handler) saveOwnMetadata(ctx context.Context, c *store.Collection, p string, raw json.RawMessage) error {
	if p == "" {
		c.Metadata = raw
		if err := h.store.UpdateCollectionMetadata(ctx, c); err != nil {
			return err
		}
		return h.store.RecomputeCollectionDenormalized(ctx, c.ID, string(h.cfg.Policy.Visibility))
	}
	node, err := h.store.GetNode(ctx, c.ID, p)
	if err != nil {
		return err
	}
	node.Metadata = raw
	if err := h.store.UpsertNode(ctx, node); err != nil {
		return err
	}
	return h.store.RecomputeNodeDenormalized(ctx, node.ID)
}

func (h *Handler) renderMetaPanelError(w http.ResponseWriter, name, p string, raw json.RawMessage, errMsg string) {
	data, err := buildMetaPanelData(h.cfg.Admin.Path, name, p, raw, errMsg)
	if err != nil {
		http.Error(w, "loading metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, "meta_panel", data)
}

func (h *Handler) renderMetaPanelSaved(w http.ResponseWriter, name, p string, doc *meta.Document) {
	raw, err := doc.JSON()
	if err != nil {
		http.Error(w, "encoding metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := buildMetaPanelData(h.cfg.Admin.Path, name, p, raw, "")
	if err != nil {
		http.Error(w, "loading metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	data.Saved = true
	h.renderPartial(w, "meta_panel", data)
}

// renderMetaRawError re-renders the panel with the operator's unsaved raw
// text preserved, instead of reloading the last-saved value, so a typo
// doesn't discard their edit.
func (h *Handler) renderMetaRawError(w http.ResponseWriter, name, p, rawText, errMsg string) {
	data, err := buildMetaPanelData(h.cfg.Admin.Path, name, p, json.RawMessage(`{}`), errMsg)
	if err != nil {
		http.Error(w, "loading metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	data.RawJSON = rawText
	h.renderPartial(w, "meta_panel", data)
}

func buildMetaPanelData(adminPath, collection, p string, raw json.RawMessage, errMsg string) (metaPanelData, error) {
	doc, err := meta.FromJSON(raw)
	if err != nil {
		return metaPanelData{}, err
	}
	pretty, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return metaPanelData{}, err
	}

	label := collection
	if p != "" {
		label += "/" + p
	}
	data := metaPanelData{
		AdminPath:  adminPath,
		Collection: collection,
		Path:       p,
		Label:      label,
		RawJSON:    string(pretty),
		Error:      errMsg,
	}
	if doc.Content != nil {
		if doc.Content.ContentType != nil {
			data.ContentType = *doc.Content.ContentType
		}
		data.Headers = formatHeaders(doc.Content.Headers)
	}
	if doc.Access != nil {
		if doc.Access.Visibility != nil {
			data.Visibility = *doc.Access.Visibility
		}
		data.DirectoryListing = triStateString(doc.Access.DirectoryListing)
		data.AuthEntries = toAuthEntryRows(doc.Access.Auth)
	}
	if doc.Crawl != nil {
		data.Crawlable = triStateString(doc.Crawl.Crawlable)
		data.Indexable = triStateString(doc.Crawl.Indexable)
	}
	if doc.Expiry != nil {
		data.ExpiresAt = strFromPtr(doc.Expiry.ExpiresAt)
		data.DeleteAfterCreation = strFromPtr(doc.Expiry.DeleteAfterCreation)
		data.DeleteAfterLastAccess = strFromPtr(doc.Expiry.DeleteAfterLastAccess)
	}
	return data, nil
}

func toAuthEntryRows(auth *[]meta.AuthEntry) []authEntryRow {
	if auth == nil {
		return nil
	}
	entries := *auth
	rows := make([]authEntryRow, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		detail := ""
		switch e.Type {
		case meta.AuthBearer:
			detail = "token configured"
		case meta.AuthBasic:
			detail = "user: " + e.Username
		case meta.AuthOIDC:
			detail = e.Issuer
		}
		rows = append(rows, authEntryRow{Type: e.Type, Detail: detail})
	}
	return rows
}

func existingAuth(d *meta.Document) *[]meta.AuthEntry {
	if d.Access == nil {
		return nil
	}
	return d.Access.Auth
}

func existingRobots(d *meta.Document) *meta.Robots {
	if d.Crawl == nil {
		return nil
	}
	return d.Crawl.Robots
}

func buildContent(contentType string, headers map[string]string) *meta.Content {
	if contentType == "" && len(headers) == 0 {
		return nil
	}
	c := &meta.Content{Headers: headers}
	if contentType != "" {
		c.ContentType = &contentType
	}
	return c
}

func buildAccess(visibility, directoryListing string, auth *[]meta.AuthEntry) *meta.Access {
	dl := triStatePtr(directoryListing)
	if visibility == "" && dl == nil && auth == nil {
		return nil
	}
	a := &meta.Access{DirectoryListing: dl, Auth: auth}
	if visibility != "" {
		a.Visibility = &visibility
	}
	return a
}

func buildCrawl(crawlable, indexable string, robots *meta.Robots) *meta.Crawl {
	cr := triStatePtr(crawlable)
	idx := triStatePtr(indexable)
	if cr == nil && idx == nil && robots == nil {
		return nil
	}
	return &meta.Crawl{Crawlable: cr, Indexable: idx, Robots: robots}
}

func buildExpiry(expiresAt, deleteAfterCreation, deleteAfterLastAccess string) *meta.Expiry {
	if expiresAt == "" && deleteAfterCreation == "" && deleteAfterLastAccess == "" {
		return nil
	}
	return &meta.Expiry{
		ExpiresAt:             ptrIfSet(expiresAt),
		DeleteAfterCreation:   ptrIfSet(deleteAfterCreation),
		DeleteAfterLastAccess: ptrIfSet(deleteAfterLastAccess),
	}
}

func ptrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strFromPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func triStateString(b *bool) string {
	switch {
	case b == nil:
		return ""
	case *b:
		return "true"
	default:
		return "false"
	}
}

func triStatePtr(s string) *bool {
	switch s {
	case "true":
		b := true
		return &b
	case "false":
		b := false
		return &b
	default:
		return nil
	}
}

// parseHeaders parses the textarea's "Name: value" lines into a map, skipping
// blank lines.
func parseHeaders(text string) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, errors.New("expected \"Name: value\", got " + line)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// formatHeaders renders a header map back to the textarea's "Name: value"
// line form, sorted for stable output.
func formatHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, k := range names {
		lines = append(lines, k+": "+headers[k])
	}
	return strings.Join(lines, "\n")
}
