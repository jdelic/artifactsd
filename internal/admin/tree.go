package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// crumb is one segment of the tree breadcrumb.
type crumb struct {
	Name string
	Path string
}

// nodeRow is one row of the folder/file table.
type nodeRow struct {
	Name        string
	Path        string
	Size        string
	ContentType string
	ViewURL     string
}

// treePanelData backs the "tree_panel" partial: breadcrumb, folder/file
// tables, and the upload forms for the current directory.
type treePanelData struct {
	AdminPath  string
	Collection string
	Dir        string
	Breadcrumb []crumb
	Folders    []nodeRow
	Files      []nodeRow
	Error      string
}

var treeFKeys = []fkey{{"Alt-U", "Upload"}, {"Alt-L", "Logout"}}

// collectionTree renders the tree-browse screen for a collection at ?path=
// (design/09 §3, mirroring the management API's GET .../tree).
func (h *Handler) collectionTree(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	dir, ok := h.cleanDir(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	data, err := h.loadTreePanel(r.Context(), c, dir, "")
	if err != nil {
		h.treeError(w, r, c, err)
		return
	}
	h.render(w, r, "tree", treeFKeys, "READY", "Collection: "+name, data)
}

// renderTreePanel re-loads the directory listing and renders the
// "tree_panel" partial for an htmx swap.
func (h *Handler) renderTreePanel(w http.ResponseWriter, r *http.Request, c *store.Collection, dir, errMsg string) {
	data, err := h.loadTreePanel(r.Context(), c, dir, errMsg)
	if err != nil {
		http.Error(w, "loading tree: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, "tree_panel", data)
}

// treeError renders the full tree page at the collection root with an inline
// error instead of a bare 500/400, so a bad request still leaves the
// operator oriented.
func (h *Handler) treeError(w http.ResponseWriter, r *http.Request, c *store.Collection, err error) {
	data, lerr := h.loadTreePanel(r.Context(), c, "", err.Error())
	if lerr != nil {
		http.Error(w, "loading tree: "+lerr.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "tree", treeFKeys, "READY", "Collection: "+c.Name, data)
}

func (h *Handler) loadTreePanel(ctx context.Context, c *store.Collection, dir, errMsg string) (treePanelData, error) {
	var parentID *int64
	if dir != "" {
		node, err := h.store.GetNode(ctx, c.ID, dir)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return treePanelData{}, errors.New("folder does not exist")
		case err != nil:
			return treePanelData{}, err
		case node.Kind != store.KindFolder:
			return treePanelData{}, errors.New("path is a file, not a folder")
		}
		parentID = &node.ID
	}
	children, err := h.store.ListChildren(ctx, c.ID, parentID)
	if err != nil {
		return treePanelData{}, err
	}

	data := treePanelData{
		AdminPath:  h.cfg.Admin.Path,
		Collection: c.Name,
		Dir:        dir,
		Breadcrumb: toBreadcrumb(c.Name, dir),
		Error:      errMsg,
	}
	for _, n := range children {
		row := nodeRow{Name: n.Name, Path: n.Path}
		if n.Kind == store.KindFolder {
			data.Folders = append(data.Folders, row)
			continue
		}
		if n.Size != nil {
			row.Size = formatBytes(*n.Size)
		}
		if n.ContentType != nil {
			row.ContentType = *n.ContentType
		}
		row.ViewURL = strings.TrimRight(h.cfg.Server.BaseURL, "/") + "/c/" + c.Name + "/" + n.Path
		data.Files = append(data.Files, row)
	}
	return data, nil
}

// toBreadcrumb splits dir into linkable segments, with the collection root
// always first.
func toBreadcrumb(collection, dir string) []crumb {
	out := []crumb{{Name: collection, Path: ""}}
	if dir == "" {
		return out
	}
	prefix := ""
	for _, seg := range strings.Split(dir, "/") {
		if prefix == "" {
			prefix = seg
		} else {
			prefix += "/" + seg
		}
		out = append(out, crumb{Name: seg, Path: prefix})
	}
	return out
}

// deleteFile removes a file node, then re-renders the tree panel for its
// parent directory ("dir", supplied by the row's hidden form field).
func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	dir := r.FormValue("dir")
	p, ok := h.cleanDir(w, r.PathValue("path"))
	if !ok {
		return
	}
	node, err := h.store.GetNode(r.Context(), c.ID, p)
	if errors.Is(err, store.ErrNotFound) {
		h.renderTreePanel(w, r, c, dir, "file does not exist")
		return
	}
	if err != nil {
		http.Error(w, "looking up file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if node.Kind != store.KindFile {
		h.renderTreePanel(w, r, c, dir, "path is a folder, not a file")
		return
	}
	if err := h.store.DeleteNode(r.Context(), node.ID); err != nil {
		h.renderTreePanel(w, r, c, dir, "deleting file: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(r.Context())
		_ = h.audit.Record(r.Context(), &audit.Entry{Action: audit.ActionFileDelete, Actor: actorOf(id), Target: c.Name + "/" + p})
	}
	h.renderTreePanel(w, r, c, dir, "")
}

// deleteFolder removes a folder node and its subtree (DB cascade), then
// re-renders the tree panel for its parent directory.
func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := h.lookupCollectionByName(w, r, name)
	if !ok {
		return
	}
	dir := r.FormValue("dir")
	p, ok := h.cleanDir(w, r.PathValue("path"))
	if !ok {
		return
	}
	node, err := h.store.GetNode(r.Context(), c.ID, p)
	if errors.Is(err, store.ErrNotFound) {
		h.renderTreePanel(w, r, c, dir, "folder does not exist")
		return
	}
	if err != nil {
		http.Error(w, "looking up folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if node.Kind != store.KindFolder {
		h.renderTreePanel(w, r, c, dir, "path is a file, not a folder")
		return
	}
	if err := h.store.DeleteNode(r.Context(), node.ID); err != nil {
		h.renderTreePanel(w, r, c, dir, "deleting folder: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(r.Context())
		_ = h.audit.Record(r.Context(), &audit.Entry{Action: audit.ActionFileDelete, Actor: actorOf(id), Target: c.Name + "/" + p})
	}
	h.renderTreePanel(w, r, c, dir, "")
}

// lookupCollectionByName resolves a collection by name, writing a 404 page
// and returning false if it does not exist.
func (h *Handler) lookupCollectionByName(w http.ResponseWriter, r *http.Request, name string) (*store.Collection, bool) {
	c, err := h.store.GetCollectionByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "collection does not exist", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		http.Error(w, "looking up collection: "+err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	return c, true
}

// cleanDir normalizes an (often empty, meaning "root") directory path query
// parameter or path segment, writing a 400 page and returning false on an
// invalid non-empty value.
func (h *Handler) cleanDir(w http.ResponseWriter, raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	p, err := paths.Clean(raw)
	if err != nil {
		http.Error(w, "invalid path: "+err.Error(), http.StatusBadRequest)
		return "", false
	}
	return p, true
}
