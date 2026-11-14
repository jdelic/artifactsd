package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/sandbox"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// allSharesRow is one row of the cross-collection shares overview.
type allSharesRow struct {
	Collection string
	ShareID    string
	URL        string
	TargetPath string
	CreatedAt  string
	ExpiresAt  string
	Revoked    bool
}

type allSharesData struct {
	AdminPath string
	Shares    []allSharesRow
}

var allSharesFKeys = []fkey{{"Alt-L", "Logout"}}

// allShares renders the global, cross-collection shares overview (artifactsd-rmn):
// every share in one place.
func (h *Handler) allShares(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadAllShares(r)
	if err != nil {
		http.Error(w, "loading shares: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "allshares", allSharesFKeys, "READY", "Shares", data)
}

// revokeShareGlobal revokes a share from the cross-collection overview and
// re-renders that panel (the per-collection revoke returns a per-collection
// fragment, which would not fit this page).
func (h *Handler) revokeShareGlobal(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("share_id")
	ctx := r.Context()
	sh, err := h.store.GetShare(ctx, shareID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Already gone — fall through and re-render the (now-current) list.
	case err != nil:
		http.Error(w, "looking up share: "+err.Error(), http.StatusInternalServerError)
		return
	default:
		if derr := h.store.DeleteShare(ctx, shareID); derr != nil {
			http.Error(w, "revoking share: "+derr.Error(), http.StatusInternalServerError)
			return
		}
		if h.audit != nil {
			target := shareID
			if col, cerr := h.store.GetCollection(ctx, sh.CollectionID); cerr == nil {
				target = col.Name + " " + shareID
			}
			id := session.FromContext(ctx)
			_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionShareRevoke, Actor: actorOf(id), Target: target})
		}
	}
	data, err := h.loadAllShares(r)
	if err != nil {
		http.Error(w, "loading shares: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, "allshares", data)
}

func (h *Handler) loadAllShares(r *http.Request) (allSharesData, error) {
	shares, err := h.store.ListAllShares(r.Context())
	if err != nil {
		return allSharesData{}, err
	}
	rows := make([]allSharesRow, 0, len(shares))
	for _, sh := range shares {
		row := allSharesRow{
			Collection: sh.CollectionName,
			ShareID:    sh.ShareID,
			URL:        h.shareURL(sh.ShareID),
			TargetPath: sh.TargetPath,
			CreatedAt:  sh.CreatedAt.UTC().Format("2006-01-02 15:04"),
			Revoked:    sh.RevokedAt != nil,
		}
		if sh.ExpiresAt != nil {
			row.ExpiresAt = sh.ExpiresAt.UTC().Format("2006-01-02 15:04")
		}
		rows = append(rows, row)
	}
	return allSharesData{AdminPath: h.cfg.Admin.Path, Shares: rows}, nil
}

// allFilesRow is one row of the cross-collection file browser.
type allFilesRow struct {
	Collection  string
	Path        string
	Size        int64
	SizeLabel   string
	ContentType string
	Created     string
	Expires     string
	// ExpiresSource says where the effective expiry comes from (artifactsd-3wy).
	ExpiresSource string
	ViewURL       string
	MetaURL       string
	// Sandbox is the file's sandbox state (artifactsd-7tl.9).
	Sandbox string
}

type allFilesData struct {
	AdminPath string
	Sort      string
	Files     []allFilesRow
}

var allFilesFKeys = []fkey{{"Alt-L", "Logout"}}

// allFilesLimit caps the cross-collection file listing so a very large
// deployment can't render an unbounded page in one shot.
const allFilesLimit = 1000

// allFiles renders the cross-collection file browser (artifactsd-0ab) with
// sorting (default: largest first).
func (h *Handler) allFiles(w http.ResponseWriter, r *http.Request) {
	sortBy := store.FileSortSize
	switch r.URL.Query().Get("sort") {
	case "name":
		sortBy = store.FileSortName
	case "created":
		sortBy = store.FileSortCreated
	case "expiry":
		sortBy = store.FileSortExpiry
	}
	files, err := h.store.ListAllFiles(r.Context(), sortBy, allFilesLimit)
	if err != nil {
		http.Error(w, "loading files: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]allFilesRow, 0, len(files))
	base := strings.TrimRight(h.cfg.Server.BaseURL, "/")
	// Sandbox states are computed once per listed collection.
	states := map[string]map[string]sandbox.State{}
	for _, f := range files {
		if _, ok := states[f.CollectionName]; ok {
			continue
		}
		states[f.CollectionName] = map[string]sandbox.State{}
		c, err := h.store.GetCollectionByName(r.Context(), f.CollectionName)
		if err != nil {
			continue
		}
		nodes, err := h.store.ListCollectionNodes(r.Context(), c.ID)
		if err != nil {
			continue
		}
		states[f.CollectionName] = sandbox.ForNodes(h.cfg.Policy.Sandbox, c.Metadata, nodes)
	}
	for _, f := range files {
		row := allFilesRow{
			Collection:  f.CollectionName,
			Path:        f.Path,
			Size:        f.Size,
			SizeLabel:   formatBytes(f.Size),
			ContentType: f.ContentType,
			Created:     f.CreatedAt.UTC().Format("2006-01-02 15:04"),
			ViewURL:     base + "/c/" + f.CollectionName + "/" + paths.EscapeURL(f.Path),
			MetaURL:     h.cfg.Admin.Path + "/collections/" + f.CollectionName + "/meta?path=" + url.QueryEscape(f.Path),
			Sandbox:     string(states[f.CollectionName][f.Path]),
		}
		row.Expires, row.ExpiresSource = expiryCell(f.Effective)
		rows = append(rows, row)
	}
	h.render(w, r, "allfiles", allFilesFKeys, "READY", "Files", allFilesData{
		AdminPath: h.cfg.Admin.Path, Sort: string(sortBy), Files: rows,
	})
}
