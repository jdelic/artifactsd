package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/durations"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// shareRow is one row of the shares table.
type shareRow struct {
	ShareID    string
	TargetPath string
	URL        string
	CreatedAt  string
	ExpiresAt  string
}

// sharesPanelData backs the "shares_panel" partial.
type sharesPanelData struct {
	AdminPath  string
	Collection string
	Shares     []shareRow
	NewURL     string
	Error      string
}

var sharesFKeys = []fkey{{"F5", "Create"}, {"F8", "Revoke"}, {"F10", "Logout"}}

// listShares renders the shares screen for a collection: list, create,
// revoke (design/09 §3).
func (h *Handler) listShares(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	data, err := h.loadSharesPanel(r, c, "", "")
	if err != nil {
		http.Error(w, "loading shares: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "shares", sharesFKeys, "READY", "Shares: "+name, data)
}

// createShare mints a share for the collection (target_path empty) or one of
// its nodes, optionally with a relative expiry ("7d", "48h" — design/04 §6).
func (h *Handler) createShare(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	ctx := r.Context()
	targetPath, targetKind, err := h.resolveShareTarget(ctx, c, r.FormValue("target_path"))
	if err != nil {
		h.renderSharesPanel(w, r, c, "", err.Error())
		return
	}

	metaJSON := []byte(`{}`)
	if expiresIn := strings.TrimSpace(r.FormValue("expires_in")); expiresIn != "" {
		d, err := durations.Parse(expiresIn)
		if err != nil {
			h.renderSharesPanel(w, r, c, "", "invalid expires_in: "+err.Error())
			return
		}
		expiresAt := time.Now().UTC().Add(d).Format(time.RFC3339)
		metaJSON = []byte(`{"expiry":{"expires_at":"` + expiresAt + `"}}`)
	}

	shareID, err := auth.GenerateShareID()
	if err != nil {
		http.Error(w, "generating share id: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sh := &store.Share{ShareID: shareID, CollectionID: c.ID, TargetPath: targetPath, TargetKind: targetKind, Metadata: metaJSON}
	if err := h.store.CreateShare(ctx, sh); err != nil {
		h.renderSharesPanel(w, r, c, "", "creating share: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionShareCreate, Actor: actorOf(id), Target: c.Name + " " + shareID})
	}
	h.renderSharesPanel(w, r, c, h.shareURL(shareID), "")
}

// revokeShare deletes a share (design/04 §6).
func (h *Handler) revokeShare(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	shareID := r.PathValue("share_id")
	ctx := r.Context()
	if _, err := h.store.GetShare(ctx, shareID); errors.Is(err, store.ErrNotFound) {
		h.renderSharesPanel(w, r, c, "", "share does not exist")
		return
	} else if err != nil {
		http.Error(w, "looking up share: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.store.DeleteShare(ctx, shareID); err != nil {
		h.renderSharesPanel(w, r, c, "", "revoking share: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionShareRevoke, Actor: actorOf(id), Target: c.Name + " " + shareID})
	}
	h.renderSharesPanel(w, r, c, "", "")
}

func (h *Handler) renderSharesPanel(w http.ResponseWriter, r *http.Request, c *store.Collection, newURL, errMsg string) {
	data, err := h.loadSharesPanel(r, c, newURL, errMsg)
	if err != nil {
		http.Error(w, "loading shares: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, "shares_panel", data)
}

func (h *Handler) loadSharesPanel(r *http.Request, c *store.Collection, newURL, errMsg string) (sharesPanelData, error) {
	shares, err := h.store.ListSharesByCollection(r.Context(), c.ID)
	if err != nil {
		return sharesPanelData{}, err
	}
	rows := make([]shareRow, 0, len(shares))
	for _, sh := range shares {
		row := shareRow{
			ShareID: sh.ShareID, TargetPath: sh.TargetPath, URL: h.shareURL(sh.ShareID),
			CreatedAt: sh.CreatedAt.UTC().Format("2006-01-02 15:04"),
		}
		if sh.ExpiresAt != nil {
			row.ExpiresAt = sh.ExpiresAt.UTC().Format("2006-01-02 15:04")
		}
		rows = append(rows, row)
	}
	return sharesPanelData{
		AdminPath: h.cfg.Admin.Path, Collection: c.Name, Shares: rows, NewURL: newURL, Error: errMsg,
	}, nil
}

// resolveShareTarget cleans the requested target path and delegates to the
// store's shared resolution logic. An empty path shares the whole collection;
// otherwise the target node must exist.
func (h *Handler) resolveShareTarget(ctx context.Context, c *store.Collection, raw string) (path, kind string, err error) {
	var p string
	if strings.Trim(raw, "/") != "" {
		p, err = paths.Clean(raw)
		if err != nil {
			return "", "", err
		}
	}
	k, err := h.store.ResolveShareTarget(ctx, c.ID, p)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", errors.New("share target does not exist")
	}
	if err != nil {
		return "", "", err
	}
	return p, k, nil
}

func (h *Handler) shareURL(shareID string) string {
	return strings.TrimRight(h.cfg.Server.BaseURL, "/") + "/s/" + shareID + "/"
}
