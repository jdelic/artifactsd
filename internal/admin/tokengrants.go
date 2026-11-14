package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// tokenGrantRow is one row of a restricted token's grants table.
type tokenGrantRow struct {
	Collection string
	Scopes     string
	CreatedAt  string
}

// tokenGrantsData backs the token grants subpage (design/16 §7).
type tokenGrantsData struct {
	AdminPath   string
	TokenID     int64
	TokenName   string
	Prefix      string
	Revoked     bool
	Grants      []tokenGrantRow
	Collections []string
	GrantScopes []string
	Error       string
}

var tokenGrantsFKeys = []fkey{{"Alt-G", "Grant"}, {"Alt-L", "Logout"}}

// errNotRestricted marks a grants route addressed at an unrestricted token.
var errNotRestricted = errors.New("this token is not restricted: it reaches every collection its scopes allow, so it has no grants")

// tokenGrants renders a restricted token's grants subpage: a table of its
// per-collection grants and a form to add or replace one.
func (h *Handler) tokenGrants(w http.ResponseWriter, r *http.Request) {
	tok, err := h.restrictedToken(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	data, err := h.loadTokenGrants(r, tok, "")
	if err != nil {
		http.Error(w, "loading grants: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "tokengrants", tokenGrantsFKeys, "READY", "Grants: "+tok.Name, data)
}

// grantToken creates or replaces a restricted token's grant on a collection
// (design/16 §5, §7).
func (h *Handler) grantToken(w http.ResponseWriter, r *http.Request) {
	tok, err := h.restrictedToken(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderTokenGrants(w, r, tok, "invalid form: "+err.Error())
		return
	}
	if tok.RevokedAt != nil {
		h.renderTokenGrants(w, r, tok, "token is revoked")
		return
	}
	scopes := r.Form["scopes"]
	if err := auth.ValidateGrantScopes(scopes); err != nil {
		h.renderTokenGrants(w, r, tok, err.Error())
		return
	}
	ctx := r.Context()
	c, err := h.store.GetCollectionByName(ctx, r.FormValue("collection"))
	if err != nil {
		h.renderTokenGrants(w, r, tok, "unknown collection: "+r.FormValue("collection"))
		return
	}
	scopesJSON, err := scopesJSON(scopes)
	if err != nil {
		http.Error(w, "encoding scopes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.store.SetTokenGrant(ctx, &store.TokenGrant{TokenID: tok.ID, CollectionID: c.ID, Scopes: scopesJSON}); err != nil {
		h.renderTokenGrants(w, r, tok, "granting: "+err.Error())
		return
	}
	h.auditGrant(r, audit.ActionTokenGrant, tok, c.Name, scopes)
	h.renderTokenGrants(w, r, tok, "")
}

// ungrantToken removes a restricted token's grant on a collection.
func (h *Handler) ungrantToken(w http.ResponseWriter, r *http.Request) {
	tok, err := h.restrictedToken(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	ctx := r.Context()
	c, err := h.store.GetCollectionByName(ctx, r.PathValue("collection"))
	if err != nil {
		h.renderTokenGrants(w, r, tok, "unknown collection: "+r.PathValue("collection"))
		return
	}
	if err := h.store.DeleteTokenGrant(ctx, tok.ID, c.ID); err != nil {
		h.renderTokenGrants(w, r, tok, "removing grant: "+err.Error())
		return
	}
	h.auditGrant(r, audit.ActionTokenUngrant, tok, c.Name, nil)
	h.renderTokenGrants(w, r, tok, "")
}

// restrictedToken resolves {id} for the grant routes; grants only exist on
// restricted tokens.
func (h *Handler) restrictedToken(r *http.Request) (*store.APIToken, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, errors.New("invalid token id")
	}
	tok, err := h.store.GetToken(r.Context(), id)
	if err != nil {
		return nil, errors.New("token does not exist")
	}
	if !tok.Restricted {
		return nil, errNotRestricted
	}
	return tok, nil
}

func (h *Handler) renderTokenGrants(w http.ResponseWriter, r *http.Request, tok *store.APIToken, errMsg string) {
	data, err := h.loadTokenGrants(r, tok, errMsg)
	if err != nil {
		http.Error(w, "loading grants: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, "tokengrants_panel", data)
}

func (h *Handler) loadTokenGrants(r *http.Request, tok *store.APIToken, errMsg string) (tokenGrantsData, error) {
	ctx := r.Context()
	grants, err := h.store.TokenGrants(ctx, tok.ID)
	if err != nil {
		return tokenGrantsData{}, err
	}
	cols, err := h.store.ListCollections(ctx)
	if err != nil {
		return tokenGrantsData{}, err
	}
	rows := make([]tokenGrantRow, 0, len(grants))
	for _, g := range grants {
		rows = append(rows, tokenGrantRow{
			Collection: g.CollectionName, Scopes: joinScopes(g.Scopes),
			CreatedAt: g.CreatedAt.UTC().Format("2006-01-02 15:04"),
		})
	}
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
	}
	grantable := make([]string, 0, len(auth.KnownScopes))
	for _, s := range auth.KnownScopes {
		if auth.IsGrantScope(s) {
			grantable = append(grantable, string(s))
		}
	}
	return tokenGrantsData{
		AdminPath: h.cfg.Admin.Path, TokenID: tok.ID, TokenName: tok.Name, Prefix: tok.TokenPrefix,
		Revoked: tok.RevokedAt != nil, Grants: rows, Collections: names, GrantScopes: grantable, Error: errMsg,
	}, nil
}

// auditGrant records a grant change made in the admin UI (design/16 §6).
func (h *Handler) auditGrant(r *http.Request, action string, tok *store.APIToken, collection string, scopes []string) {
	if h.audit == nil {
		return
	}
	details := map[string]any{"token_id": tok.ID, "collection": collection}
	if scopes != nil {
		details["scopes"] = scopes
	}
	raw, _ := json.Marshal(details)
	ctx := r.Context()
	_ = h.audit.Record(ctx, &audit.Entry{
		Action: action, Actor: actorOf(session.FromContext(ctx)), Target: tok.Name + " " + collection, Details: raw,
	})
}
