package admin

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// tokenRow is one row of the tokens table.
type tokenRow struct {
	ID        int64
	Name      string
	Prefix    string
	Scopes    string
	CreatedAt string
	Revoked   bool
	// Restricted tokens reach only their granted collections (design/16);
	// the grants are managed on the token's grants subpage.
	Restricted bool
	GrantCount int
}

// tokensPanelData backs the "tokens_panel" partial.
type tokensPanelData struct {
	AdminPath   string
	Tokens      []tokenRow
	KnownScopes []string
	NewToken    string
	NewTokenFor string
	// NewTokenGrantsID is set when the token just created is restricted, to
	// point the admin at its (still empty) grants page.
	NewTokenGrantsID int64
	Error            string
}

var tokensFKeys = []fkey{{"Alt-C", "Create"}, {"Alt-L", "Logout"}}

// tokensList renders the API tokens screen: list, create, revoke; a freshly
// minted secret is shown once (design/09 §3/§5).
func (h *Handler) tokensList(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadTokensPanel(r, "", "", "")
	if err != nil {
		http.Error(w, "loading tokens: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "tokens", tokensFKeys, "READY", "API Tokens", data)
}

// createToken mints a new API token, returning the cleartext secret exactly
// once (design/09 §5: "token secret ... shown once with a copy button").
func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	tokenName := r.FormValue("name")
	if tokenName == "" {
		h.renderTokensPanel(w, r, "", "", "name is required")
		return
	}
	scopes := r.Form["scopes"]
	restricted := r.FormValue("restricted") != ""
	if msg := validateTokenForm(restricted, scopes); msg != "" {
		h.renderTokensPanel(w, r, "", "", msg)
		return
	}

	secret, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		http.Error(w, "generating token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	scopesJSON, err := scopesJSON(scopes)
	if err != nil {
		http.Error(w, "encoding scopes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tok := &store.APIToken{Name: tokenName, TokenHash: hash, TokenPrefix: prefix, Scopes: scopesJSON, Restricted: restricted}
	ctx := r.Context()
	if err := h.store.CreateToken(ctx, tok); err != nil {
		h.renderTokensPanel(w, r, "", "", "creating token: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{Action: audit.ActionTokenCreate, Actor: actorOf(id), Target: tok.Name})
	}
	data, err := h.loadTokensPanel(r, secret, tok.Name, "")
	if err != nil {
		http.Error(w, "loading tokens: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if tok.Restricted {
		data.NewTokenGrantsID = tok.ID
	}
	h.renderPartial(w, "tokens_panel", data)
}

// validateTokenForm checks the create form (design/16 §2): unrestricted
// tokens need known scopes; restricted tokens take none (their scopes live on
// grants, added afterwards) and can never be admin. Returns "" when valid.
func validateTokenForm(restricted bool, scopes []string) string {
	if restricted {
		for _, s := range scopes {
			if auth.Scope(s) == auth.ScopeAdmin {
				return "admin tokens cannot be restricted"
			}
		}
		if len(scopes) > 0 {
			return "restricted tokens carry scopes on their grants: leave the scopes unchecked and add grants on the token's grants page"
		}
		return ""
	}
	if len(scopes) == 0 {
		return "at least one scope is required"
	}
	for _, s := range scopes {
		if !auth.IsKnownScope(auth.Scope(s)) {
			return "unknown scope: " + s
		}
	}
	return ""
}

// revokeToken revokes an API token.
func (h *Handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.renderTokensPanel(w, r, "", "", "invalid token id")
		return
	}
	ctx := r.Context()
	if err := h.store.RevokeToken(ctx, id, time.Now().UTC()); err != nil {
		h.renderTokensPanel(w, r, "", "", "revoking token: "+err.Error())
		return
	}
	if h.audit != nil {
		actorID := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{
			Action: audit.ActionTokenRevoke, Actor: actorOf(actorID), Target: strconv.FormatInt(id, 10),
		})
	}
	h.renderTokensPanel(w, r, "", "", "")
}

func (h *Handler) renderTokensPanel(w http.ResponseWriter, r *http.Request, newToken, newTokenFor, errMsg string) {
	data, err := h.loadTokensPanel(r, newToken, newTokenFor, errMsg)
	if err != nil {
		http.Error(w, "loading tokens: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, "tokens_panel", data)
}

func (h *Handler) loadTokensPanel(r *http.Request, newToken, newTokenFor, errMsg string) (tokensPanelData, error) {
	ctx := r.Context()
	tokens, err := h.store.ListTokens(ctx)
	if err != nil {
		return tokensPanelData{}, err
	}
	grants, err := h.store.ListTokenGrants(ctx)
	if err != nil {
		return tokensPanelData{}, err
	}
	rows := make([]tokenRow, 0, len(tokens))
	for _, t := range tokens {
		row := tokenRow{
			ID: t.ID, Name: t.Name, Prefix: t.TokenPrefix, Scopes: joinScopes(t.Scopes),
			CreatedAt: t.CreatedAt.UTC().Format("2006-01-02 15:04"), Revoked: t.RevokedAt != nil,
			Restricted: t.Restricted, GrantCount: len(grants[t.ID]),
		}
		rows = append(rows, row)
	}
	known := make([]string, 0, len(auth.KnownScopes))
	for _, s := range auth.KnownScopes {
		known = append(known, string(s))
	}
	return tokensPanelData{
		AdminPath: h.cfg.Admin.Path, Tokens: rows, KnownScopes: known,
		NewToken: newToken, NewTokenFor: newTokenFor, Error: errMsg,
	}, nil
}

func joinScopes(raw []byte) string {
	set, err := auth.ParseScopes(raw)
	if err != nil {
		return ""
	}
	scopes := set.Strings()
	sort.Strings(scopes)
	out := ""
	for i, s := range scopes {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func scopesJSON(scopes []string) ([]byte, error) {
	return json.Marshal(scopes)
}
