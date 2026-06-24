package admin

import (
	"net/http"
	"time"

	"github.com/jdelic/artifactsd/internal/store"
)

// auditRow is one row of the audit log table.
type auditRow struct {
	Timestamp string
	Actor     string
	Action    string
	Target    string
}

// auditData backs the "audit" page: filterable by action and since-date
// (design/09 §3, design/04 §8).
type auditData struct {
	AdminPath string
	Entries   []auditRow
	Action    string
	Since     string
	Error     string
}

var auditFKeys = []fkey{{"F5", "Filter"}, {"F10", "Logout"}}

// auditLog renders the audit log screen, the admin UI's record of admin
// actions, deletions, and authenticated access to protected objects
// (design/09 §3, decision N6).
func (h *Handler) auditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.AuditFilter{Action: q.Get("action"), Limit: 200}
	errMsg := ""
	since := q.Get("since")
	if since != "" {
		if t, err := time.Parse("2006-01-02", since); err == nil {
			filter.Since = &t
		} else if t, err := time.Parse(time.RFC3339, since); err == nil {
			filter.Since = &t
		} else {
			errMsg = "invalid since date: use YYYY-MM-DD or RFC3339"
		}
	}

	entries, err := h.store.QueryAudit(r.Context(), filter)
	if err != nil {
		http.Error(w, "loading audit log: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]auditRow, 0, len(entries))
	for _, e := range entries {
		row := auditRow{Timestamp: e.Timestamp.UTC().Format("2006-01-02 15:04:05"), Action: e.Action}
		if e.Actor != nil {
			row.Actor = *e.Actor
		}
		if e.Target != nil {
			row.Target = *e.Target
		}
		rows = append(rows, row)
	}
	data := auditData{
		AdminPath: h.cfg.Admin.Path, Entries: rows, Action: filter.Action, Since: since, Error: errMsg,
	}
	h.render(w, r, "audit", auditFKeys, "READY", "Audit Log", data)
}
