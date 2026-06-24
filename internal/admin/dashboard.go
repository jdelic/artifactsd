package admin

import (
	"net/http"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// dashboardData backs templates/dashboard.html.
type dashboardData struct {
	AdminPath string
	Stats     *statsView
}

// statsView mirrors store.Stats with a human-formatted size string, since
// html/template can't call arbitrary methods with arguments inline as
// cleanly as a precomputed field.
type statsView struct {
	Collections     int64
	Files           int64
	Folders         int64
	Shares          int64
	ActiveTokens    int64
	Blobs           int64
	StorageBytes    int64
	StorageReadable string
}

var dashboardFKeys = []fkey{{"F9", "Cleanup"}, {"F10", "Logout"}}

// dashboard renders the admin landing page: stats tiles, janitor status, and
// the "Run cleanup" control (design/09 §3).
func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	st, err := h.store.Stats(r.Context())
	if err != nil {
		http.Error(w, "loading stats: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "dashboard", dashboardFKeys, "READY", "Dashboard",
		dashboardData{AdminPath: h.cfg.Admin.Path, Stats: toStatsView(st)})
}

// cleanupResultData backs templates/cleanup_result.html, the htmx-swapped
// partial returned by maintenanceCleanup.
type cleanupResultData struct {
	Result any
	Error  string
}

// maintenanceCleanup triggers one on-demand janitor pass and returns a
// partial with the resulting summary, audited as an admin action (design/09
// §5: "all admin actions audited").
func (h *Handler) maintenanceCleanup(w http.ResponseWriter, r *http.Request) {
	if h.cleaner == nil {
		h.renderPartial(w, "cleanup_result", cleanupResultData{Error: "cleanup is not configured"})
		return
	}
	result, err := h.cleaner.RunOnce(r.Context())
	if err != nil {
		h.renderPartial(w, "cleanup_result", cleanupResultData{Error: err.Error()})
		return
	}
	if h.audit != nil {
		id := session.FromContext(r.Context())
		_ = h.audit.Record(r.Context(), &audit.Entry{Action: audit.ActionMaintenanceRun, Actor: actorOf(id)})
	}
	h.renderPartial(w, "cleanup_result", cleanupResultData{Result: result})
}

func toStatsView(st *store.Stats) *statsView {
	return &statsView{
		Collections:     st.Collections,
		Files:           st.Files,
		Folders:         st.Folders,
		Shares:          st.Shares,
		ActiveTokens:    st.ActiveTokens,
		Blobs:           st.Blobs,
		StorageBytes:    st.StorageBytes,
		StorageReadable: formatBytes(st.StorageBytes),
	}
}
