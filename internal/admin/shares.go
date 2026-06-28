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

var sharesFKeys = []fkey{{"Alt-C", "Create"}, {"Alt-L", "Logout"}}

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

// shareCellData backs the reusable "share_cell" fragment: the inline Share /
// Unshare control on file, folder, and collection rows. When Shared it shows the
// /s/<id>/ link plus an Unshare button; otherwise a Share button. Both buttons
// htmx-swap the cell with the handler's fresh render (artifactsd-jaj).
type shareCellData struct {
	AdminPath  string
	Collection string
	TargetPath string
	Shared     bool
	ShareURL   string
	Error      string
}

func (h *Handler) renderShareCell(w http.ResponseWriter, c *store.Collection, target string, shared bool, shareURL, errMsg string) {
	h.renderPartial(w, "share_cell", shareCellData{
		AdminPath: h.cfg.Admin.Path, Collection: c.Name, TargetPath: target,
		Shared: shared, ShareURL: shareURL, Error: errMsg,
	})
}

// quickShare mints a no-expiry share for a target (a file/folder path, or the
// whole collection when target_path is empty) and returns the share cell now
// showing the /s/<id>/ link and an Unshare button (artifactsd-jaj). It powers
// the one-click Share buttons; the full Shares screen remains for
// expiry-controlled shares.
func (h *Handler) quickShare(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	ctx := r.Context()
	rawTarget := r.FormValue("target_path")
	targetPath, targetKind, err := h.resolveShareTarget(ctx, c, rawTarget)
	if err != nil {
		h.renderShareCell(w, c, strings.Trim(rawTarget, "/"), false, "", err.Error())
		return
	}
	shareID, err := auth.GenerateShareID()
	if err != nil {
		h.renderShareCell(w, c, targetPath, false, "", "generating share id")
		return
	}
	sh := &store.Share{ShareID: shareID, CollectionID: c.ID, TargetPath: targetPath, TargetKind: targetKind, Metadata: []byte(`{}`)}
	if err := h.store.CreateShare(ctx, sh); err != nil {
		h.renderShareCell(w, c, targetPath, false, "", "creating share: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionShareCreate, Actor: actorOf(id), Target: c.Name + " " + shareID})
	}
	h.renderShareCell(w, c, targetPath, true, h.shareURL(shareID), "")
}

// unshare revokes every share whose target is the given path (a file/folder, or
// the whole collection when empty) and returns the share cell to its unshared
// state. "Unshare this" means it is no longer shared, so all shares for that
// exact target are removed; the Shares screen remains for per-share control.
func (h *Handler) unshare(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	ctx := r.Context()
	target, err := cleanShareTargetPath(r.FormValue("target_path"))
	if err != nil {
		h.renderShareCell(w, c, "", true, "", err.Error())
		return
	}
	shares, err := h.store.ListSharesByCollection(ctx, c.ID)
	if err != nil {
		h.renderShareCell(w, c, target, true, "", "loading shares: "+err.Error())
		return
	}
	id := session.FromContext(ctx)
	for _, sh := range shares {
		if sh.TargetPath != target {
			continue
		}
		if derr := h.store.DeleteShare(ctx, sh.ShareID); derr == nil && h.audit != nil {
			_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionShareRevoke, Actor: actorOf(id), Target: c.Name + " " + sh.ShareID})
		}
	}
	h.renderShareCell(w, c, target, false, "", "")
}

// cleanShareTargetPath cleans a share target; empty means the whole collection.
func cleanShareTargetPath(raw string) (string, error) {
	if strings.Trim(raw, "/") == "" {
		return "", nil
	}
	return paths.Clean(raw)
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
