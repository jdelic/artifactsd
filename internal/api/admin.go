package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/store"
)

// --- Tokens (design/04 §8) -------------------------------------------------

// tokenView is the API representation of an API token. It never carries the
// secret; the cleartext is returned only once, by createToken.
type tokenView struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt *string  `json:"last_used_at,omitempty"`
	ExpiresAt  *string  `json:"expires_at,omitempty"`
	RevokedAt  *string  `json:"revoked_at,omitempty"`
	// Restricted tokens reach only the collections in Grants (design/16).
	Restricted bool        `json:"restricted"`
	Grants     []grantView `json:"grants,omitempty"`
}

// grantView is one per-collection grant of a restricted token.
type grantView struct {
	Collection string   `json:"collection"`
	Scopes     []string `json:"scopes"`
}

func toTokenView(t *store.APIToken, grants []*store.TokenGrant) tokenView {
	v := tokenView{
		ID: t.ID, Name: t.Name, Prefix: t.TokenPrefix,
		Scopes:     scopeStrings(t.Scopes),
		CreatedAt:  t.CreatedAt.UTC().Format(time.RFC3339),
		Restricted: t.Restricted,
	}
	v.LastUsedAt = fmtTimePtr(t.LastUsedAt)
	v.ExpiresAt = fmtTimePtr(t.ExpiresAt)
	v.RevokedAt = fmtTimePtr(t.RevokedAt)
	if t.Restricted {
		v.Grants = make([]grantView, 0, len(grants))
		for _, g := range grants {
			v.Grants = append(v.Grants, grantView{Collection: g.CollectionName, Scopes: scopeStrings(g.Scopes)})
		}
	}
	return v
}

// scopeStrings parses the stored scopes JSON array into a string slice (best
// effort: an unparseable column yields an empty list rather than an error).
func scopeStrings(raw []byte) []string {
	set, err := auth.ParseScopes(raw)
	if err != nil {
		return []string{}
	}
	out := set.Strings()
	if out == nil {
		out = []string{}
	}
	return out
}

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.store.ListTokens(r.Context())
	if err != nil {
		h.internalServerError(w, "list tokens", err)
		return
	}
	grants, err := h.store.ListTokenGrants(r.Context())
	if err != nil {
		h.internalServerError(w, "list token grants", err)
		return
	}
	items := make([]tokenView, 0, len(tokens))
	for _, t := range tokens {
		items = append(items, toTokenView(t, grants[t.ID]))
	}
	writeJSON(w, http.StatusOK, listResponse[tokenView]{Items: items})
}

type createTokenRequest struct {
	Name       string         `json:"name"`
	Scopes     []string       `json:"scopes"`
	ExpiresAt  string         `json:"expires_at,omitempty"`
	Restricted bool           `json:"restricted,omitempty"`
	Grants     []grantRequest `json:"grants,omitempty"`
}

// grantRequest is one grant in a token creation request.
type grantRequest struct {
	Collection string   `json:"collection"`
	Scopes     []string `json:"scopes"`
}

// createTokenResponse embeds the token view and adds the one-time secret.
type createTokenResponse struct {
	tokenView
	Token string `json:"token"`
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "name is required")
		return
	}
	if msg := validateTokenScopes(&req); msg != "" {
		writeError(w, http.StatusBadRequest, CodeBadRequest, msg)
		return
	}
	grants, ok := h.resolveGrants(w, r, req.Grants)
	if !ok {
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid expires_at: "+err.Error())
			return
		}
		t = t.UTC()
		expiresAt = &t
	}

	secret, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		h.internalServerError(w, "generate token", err)
		return
	}
	scopesJSON, err := scopesJSON(req.Scopes)
	if err != nil {
		h.internalServerError(w, "encode scopes", err)
		return
	}
	tok := &store.APIToken{
		Name: req.Name, TokenHash: hash, TokenPrefix: prefix,
		Scopes: scopesJSON, ExpiresAt: expiresAt, Restricted: req.Restricted,
	}
	if err := h.store.CreateTokenWithGrants(r.Context(), tok, grants); err != nil {
		h.internalServerError(w, "create token", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionTokenCreate, Actor: actor(r), Target: tok.Name,
	})
	for _, g := range grants {
		h.auditGrant(r, audit.ActionTokenGrant, tok, g.CollectionName, g.Scopes)
	}
	writeJSON(w, http.StatusCreated, createTokenResponse{tokenView: toTokenView(tok, grants), Token: secret})
}

// validateTokenScopes checks a creation request's scopes against its
// restriction (design/16 §2): unrestricted tokens need known token-level
// scopes and no grants; restricted tokens carry their scopes on grants only
// and can never be admin. Returns "" when valid.
func validateTokenScopes(req *createTokenRequest) string {
	if !req.Restricted {
		if len(req.Grants) > 0 {
			return "grants require a restricted token"
		}
		if len(req.Scopes) == 0 {
			return "at least one scope is required"
		}
		for _, s := range req.Scopes {
			if !auth.IsKnownScope(auth.Scope(s)) {
				return "unknown scope: " + s
			}
		}
		return ""
	}
	for _, s := range req.Scopes {
		if auth.Scope(s) == auth.ScopeAdmin {
			return "admin tokens cannot be restricted"
		}
	}
	if len(req.Scopes) > 0 {
		return "restricted tokens carry scopes on their grants, not on the token"
	}
	return ""
}

// validateGrantScopes checks one grant's scope list; "" when valid.
func validateGrantScopes(scopes []string) string {
	if len(scopes) == 0 {
		return "a grant needs at least one scope"
	}
	for _, s := range scopes {
		if auth.Scope(s) == auth.ScopeAdmin {
			return "admin cannot be granted per collection"
		}
		if !auth.IsGrantScope(auth.Scope(s)) {
			return "unknown scope: " + s
		}
	}
	return ""
}

// resolveGrants validates the grants of a token creation request and maps
// collection names to ids. Writes a 400 and returns false on invalid input.
func (h *Handler) resolveGrants(w http.ResponseWriter, r *http.Request, reqs []grantRequest) ([]*store.TokenGrant, bool) {
	out := make([]*store.TokenGrant, 0, len(reqs))
	seen := map[string]bool{}
	for _, g := range reqs {
		if seen[g.Collection] {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "duplicate grant for collection "+g.Collection)
			return nil, false
		}
		seen[g.Collection] = true
		if msg := validateGrantScopes(g.Scopes); msg != "" {
			writeError(w, http.StatusBadRequest, CodeBadRequest, msg)
			return nil, false
		}
		c, err := h.store.GetCollectionByName(r.Context(), g.Collection)
		if isNotFound(err) {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "unknown collection: "+g.Collection)
			return nil, false
		}
		if err != nil {
			h.internalServerError(w, "lookup collection", err)
			return nil, false
		}
		scopes, err := scopesJSON(g.Scopes)
		if err != nil {
			h.internalServerError(w, "encode scopes", err)
			return nil, false
		}
		out = append(out, &store.TokenGrant{CollectionID: c.ID, CollectionName: c.Name, Scopes: scopes})
	}
	return out, true
}

// auditGrant records a grant change (design/16 §6).
func (h *Handler) auditGrant(r *http.Request, action string, tok *store.APIToken, collection string, scopes []byte) {
	details := map[string]any{"token_id": tok.ID, "collection": collection}
	if scopes != nil {
		details["scopes"] = scopeStrings(scopes)
	}
	raw, _ := json.Marshal(details)
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: action, Actor: actor(r), Target: tok.Name + " " + collection, Details: raw,
	})
}

type putGrantRequest struct {
	Scopes []string `json:"scopes"`
}

// lookupGrantTarget resolves {id} and {c} of the grant routes. Grants only
// exist on restricted tokens (400 otherwise).
func (h *Handler) lookupGrantTarget(w http.ResponseWriter, r *http.Request) (*store.APIToken, *store.Collection, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid token id")
		return nil, nil, false
	}
	tok, err := h.store.GetToken(r.Context(), id)
	if isNotFound(err) {
		writeError(w, http.StatusNotFound, CodeNotFound, "token does not exist")
		return nil, nil, false
	}
	if err != nil {
		h.internalServerError(w, "lookup token", err)
		return nil, nil, false
	}
	if !tok.Restricted {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "grants require a restricted token")
		return nil, nil, false
	}
	c, err := h.store.GetCollectionByName(r.Context(), r.PathValue("c"))
	if isNotFound(err) {
		writeError(w, http.StatusNotFound, CodeNotFound, "collection does not exist")
		return nil, nil, false
	}
	if err != nil {
		h.internalServerError(w, "lookup collection", err)
		return nil, nil, false
	}
	return tok, c, true
}

// putTokenGrant handles PUT /api/v1/tokens/{id}/grants/{c}: create or
// replace one grant (design/16 §5). Responds with the updated token.
func (h *Handler) putTokenGrant(w http.ResponseWriter, r *http.Request) {
	tok, c, ok := h.lookupGrantTarget(w, r)
	if !ok {
		return
	}
	var req putGrantRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := validateGrantScopes(req.Scopes); msg != "" {
		writeError(w, http.StatusBadRequest, CodeBadRequest, msg)
		return
	}
	scopes, err := scopesJSON(req.Scopes)
	if err != nil {
		h.internalServerError(w, "encode scopes", err)
		return
	}
	if err := h.store.SetTokenGrant(r.Context(), &store.TokenGrant{
		TokenID: tok.ID, CollectionID: c.ID, Scopes: scopes,
	}); err != nil {
		h.internalServerError(w, "set token grant", err)
		return
	}
	h.auditGrant(r, audit.ActionTokenGrant, tok, c.Name, scopes)
	h.writeTokenView(w, r, tok)
}

// deleteTokenGrant handles DELETE /api/v1/tokens/{id}/grants/{c}.
func (h *Handler) deleteTokenGrant(w http.ResponseWriter, r *http.Request) {
	tok, c, ok := h.lookupGrantTarget(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteTokenGrant(r.Context(), tok.ID, c.ID); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, CodeNotFound, "token has no grant on this collection")
			return
		}
		h.internalServerError(w, "delete token grant", err)
		return
	}
	h.auditGrant(r, audit.ActionTokenUngrant, tok, c.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// writeTokenView responds with a token and its current grants.
func (h *Handler) writeTokenView(w http.ResponseWriter, r *http.Request, tok *store.APIToken) {
	grants, err := h.store.TokenGrants(r.Context(), tok.ID)
	if err != nil {
		h.internalServerError(w, "list token grants", err)
		return
	}
	writeJSON(w, http.StatusOK, toTokenView(tok, grants))
}

func (h *Handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid token id")
		return
	}
	if err := h.store.RevokeToken(r.Context(), id, time.Now().UTC()); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, CodeNotFound, "token does not exist or is already revoked")
			return
		}
		h.internalServerError(w, "revoke token", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionTokenRevoke, Actor: actor(r), Target: strconv.FormatInt(id, 10),
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- Audit log (design/04 §8) ----------------------------------------------

type auditView struct {
	ID        int64           `json:"id"`
	Timestamp string          `json:"timestamp"`
	Actor     *string         `json:"actor,omitempty"`
	Action    string          `json:"action"`
	Target    *string         `json:"target,omitempty"`
	Details   json.RawMessage `json:"details,omitempty"`
	RequestID *string         `json:"request_id,omitempty"`
}

func toAuditView(a *store.AuditLog) auditView {
	return auditView{
		ID: a.ID, Timestamp: a.Timestamp.UTC().Format(time.RFC3339Nano),
		Actor: a.Actor, Action: a.Action, Target: a.Target,
		Details: a.Details, RequestID: a.RequestID,
	}
}

func (h *Handler) getAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.AuditFilter{Action: q.Get("action"), Limit: parseLimit(q.Get("limit"))}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid since: "+err.Error())
			return
		}
		t = t.UTC()
		filter.Since = &t
	}
	entries, err := h.store.QueryAudit(r.Context(), filter)
	if err != nil {
		h.internalServerError(w, "query audit", err)
		return
	}
	items := make([]auditView, 0, len(entries))
	for _, a := range entries {
		items = append(items, toAuditView(a))
	}
	writeJSON(w, http.StatusOK, listResponse[auditView]{Items: items})
}

// --- Stats (design/04 §8) --------------------------------------------------

func (h *Handler) getStats(w http.ResponseWriter, r *http.Request) {
	st, err := h.store.Stats(r.Context())
	if err != nil {
		h.internalServerError(w, "stats", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// --- Maintenance (design/04 §8, design/08 §1) ------------------------------

// postMaintenanceCleanup triggers one on-demand janitor pass. The janitor is
// injected as a Cleaner; when none is configured the capability is reported as
// unavailable (503).
func (h *Handler) postMaintenanceCleanup(w http.ResponseWriter, r *http.Request) {
	if h.cleaner == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "cleanup is not configured")
		return
	}
	result, err := h.cleaner.RunOnce(r.Context())
	if err != nil {
		h.internalServerError(w, "cleanup", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleanup": result})
}

// fmtTimePtr renders a nullable timestamp as an RFC3339 string pointer.
func fmtTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// isNotFound reports whether err is the store's not-found sentinel.
func isNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}

// scopesJSON marshals a scope string slice into the stored JSON array form.
func scopesJSON(scopes []string) (json.RawMessage, error) {
	return json.Marshal(scopes)
}
