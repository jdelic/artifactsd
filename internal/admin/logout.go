package admin

import (
	"net/http"
	"strings"
)

// logout clears the admin session cookie and sends the browser back to the
// admin root, which — now carrying no session — bounces to the OIDC login.
// It is a POST guarded by csrfGate like every other state-changing admin
// action, and is reachable from the top-bar Logout control (Alt+L). The basic
// fallback admin carries no session cookie, so for it logout is effectively a
// no-op redirect; the browser must drop its basic credentials separately.
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.session.Clear(w)
	dest := strings.TrimSuffix(h.cfg.Admin.Path, "/") + "/"
	// htmx issues this POST via fetch; HX-Redirect navigates the top window
	// (a body redirect would only swap into the triggering element).
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", dest)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
