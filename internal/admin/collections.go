package admin

import (
	"errors"
	"net/http"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// collectionRow is one row of the collections table.
type collectionRow struct {
	Name       string
	Visibility string
	CreatedAt  string
}

// collectionsPanelData backs the "collections_panel" partial, shared by the
// full-page GET and the htmx-swapped create/delete responses.
type collectionsPanelData struct {
	AdminPath   string
	Collections []collectionRow
	Error       string
}

var collectionsFKeys = []fkey{{"F5", "Create"}, {"F8", "Delete"}, {"F10", "Logout"}}

// collectionsList renders the collections screen (design/09 §3): list,
// create, delete.
func (h *Handler) collectionsList(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadCollectionsPanel(r, "")
	if err != nil {
		http.Error(w, "loading collections: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "collections", collectionsFKeys, "READY", "Collections", data)
}

// createCollection handles the htmx create form, re-rendering the panel with
// either the refreshed list or an inline error.
func (h *Handler) createCollection(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := paths.ValidateCollectionName(name); err != nil {
		h.renderCollectionsPanel(w, r, err.Error())
		return
	}
	ctx := r.Context()
	if _, err := h.store.GetCollectionByName(ctx, name); err == nil {
		h.renderCollectionsPanel(w, r, "collection already exists")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "looking up collection: "+err.Error(), http.StatusInternalServerError)
		return
	}

	c := &store.Collection{Name: name}
	if err := h.store.CreateCollection(ctx, c); err != nil {
		h.renderCollectionsPanel(w, r, "creating collection: "+err.Error())
		return
	}
	if err := h.store.RecomputeCollectionDenormalized(ctx, c.ID, string(h.cfg.Policy.Visibility)); err != nil {
		h.renderCollectionsPanel(w, r, "creating collection: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionCollectionCreate, Actor: actorOf(id), Target: name})
	}
	h.renderCollectionsPanel(w, r, "")
}

// deleteCollection handles the htmx delete form. The confirmation dialog
// (hx-confirm) is the operator's explicit go-ahead, so this always deletes
// the collection and its contents (the API's force=true semantics) rather
// than re-gating on emptiness.
func (h *Handler) deleteCollection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ctx := r.Context()
	c, err := h.store.GetCollectionByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		h.renderCollectionsPanel(w, r, "collection does not exist")
		return
	}
	if err != nil {
		http.Error(w, "looking up collection: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.store.DeleteCollection(ctx, c.ID); err != nil {
		h.renderCollectionsPanel(w, r, "deleting collection: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionCollectionDelete, Actor: actorOf(id), Target: name})
	}
	h.renderCollectionsPanel(w, r, "")
}

// renderCollectionsPanel re-loads the collection list and renders the
// "collections_panel" partial for an htmx swap.
func (h *Handler) renderCollectionsPanel(w http.ResponseWriter, r *http.Request, errMsg string) {
	data, err := h.loadCollectionsPanel(r, errMsg)
	if err != nil {
		http.Error(w, "loading collections: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, "collections_panel", data)
}

func (h *Handler) loadCollectionsPanel(r *http.Request, errMsg string) (collectionsPanelData, error) {
	cols, err := h.store.ListCollections(r.Context())
	if err != nil {
		return collectionsPanelData{}, err
	}
	rows := make([]collectionRow, 0, len(cols))
	for _, c := range cols {
		rows = append(rows, collectionRow{
			Name:       c.Name,
			Visibility: c.Visibility,
			CreatedAt:  c.CreatedAt.UTC().Format("2006-01-02 15:04"),
		})
	}
	return collectionsPanelData{AdminPath: h.cfg.Admin.Path, Collections: rows, Error: errMsg}, nil
}
