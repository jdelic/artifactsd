package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"

	"github.com/jdelic/artifactsd/internal/session"
)

//go:embed templates/*.html
var templateFS embed.FS

var funcMap = template.FuncMap{
	"formatBytes": formatBytes,
	"sharecell":   newShareCell,
}

// newShareCell builds the data for the inline Share/Unshare control so a row
// template can render it with one call (artifactsd-jaj/unshare).
func newShareCell(adminPath, collection, target string, shared bool, shareURL string) shareCellData {
	return shareCellData{
		AdminPath: adminPath, Collection: collection, TargetPath: target,
		Shared: shared, ShareURL: shareURL,
	}
}

// parseTemplates parses every embedded template once at startup; a malformed
// template is a programming error, not a runtime condition, so this panics
// the same way template.Must would.
func parseTemplates() *template.Template {
	return template.Must(template.New("admin").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"))
}

// pageData is the data available to the layout template (top nav, shortcut
// footer, CSRF token for forms/htmx) plus the pre-rendered page content.
// html/template's "template" action requires a constant name, so render
// executes the page template into Content first rather than dispatching on a
// dynamic name from within layout.html.
type pageData struct {
	AdminPath string
	Identity  *session.Identity
	CSRFToken string
	// FKeys lists the bottom-bar keyboard-shortcut hints for this screen, e.g.
	// {"Alt-C", "Create"}, {"Alt-L", "Logout"}. Each one is wired by
	// static/shortcuts.js to the element carrying the matching data-shortcut
	// attribute; the displayed Alt+<letter> combos are browser-safe (unlike the
	// DOS-style F-keys this footer used to advertise, which browsers reserve).
	FKeys   []fkey
	Status  string // right-aligned status word in the shortcut bar, e.g. "READY"
	Title   string // breadcrumb/title shown in the top bar
	Active  string // top-nav section to highlight, e.g. "dashboard", "collections"
	Content template.HTML
}

// fkey is one footer keyboard-shortcut hint: Key is the displayed combo (e.g.
// "Alt-C") and Label the action it performs (e.g. "Create"). The lowercased
// letter is the data-shortcut value static/shortcuts.js dispatches on when Alt
// is held. (Named fkey for historical reasons — the footer once showed F-keys.)
type fkey struct {
	Key   string
	Label string
}

// render executes the named page template, then wraps its output in the
// shared layout chrome. active names the top-nav section to highlight (e.g.
// "dashboard", "collections").
func (h *Handler) render(w http.ResponseWriter, r *http.Request, page string, fkeys []fkey, status, title string, data any) {
	var content bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&content, page, data); err != nil {
		if h.log != nil {
			h.log.Error("admin: render page failed", "page", page, "error", err)
		}
		http.Error(w, "rendering page", http.StatusInternalServerError)
		return
	}

	id := session.FromContext(r.Context())
	pd := pageData{
		AdminPath: h.cfg.Admin.Path,
		Identity:  id,
		FKeys:     fkeys,
		Status:    status,
		Title:     title,
		Active:    navSection(page),
		Content:   template.HTML(content.String()), //nolint:gosec // page templates are our own, html/template-escaped
	}
	if id != nil {
		pd.CSRFToken = h.session.CSRFToken(id)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "layout", pd); err != nil && h.log != nil {
		h.log.Error("admin: render layout failed", "page", page, "error", err)
	}
}

// renderPartial executes a single named template without the layout chrome,
// for htmx fragment swaps.
func (h *Handler) renderPartial(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil && h.log != nil {
		h.log.Error("admin: render partial failed", "name", name, "error", err)
	}
}

// navSection maps a page template name to the top-nav section it belongs
// under (e.g. the collection tree browser, metadata editor, and shares
// screens all highlight "Collections" too).
func navSection(page string) string {
	switch page {
	case "tree", "meta", "shares":
		return "collections"
	case "tokens", "audit":
		return "tools"
	case "allfiles":
		return "files"
	case "allshares":
		return "shares"
	case "uploadrequests":
		return "uploadrequests"
	default:
		return page
	}
}

// actorOf returns the audit actor string for an identity: email if present,
// else the OIDC subject, else "unknown" (should not happen behind adminGate).
func actorOf(id *session.Identity) string {
	switch {
	case id == nil:
		return "unknown"
	case id.Email != "":
		return id.Email
	default:
		return id.Subject
	}
}

// formatBytes renders n as a human-readable size (1.2 MiB, 340 KiB, ...).
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for next := n / unit; next >= unit; next /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
