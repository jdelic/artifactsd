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
}

func toTokenView(t *store.APIToken) tokenView {
	v := tokenView{
		ID: t.ID, Name: t.Name, Prefix: t.TokenPrefix,
		Scopes:    scopeStrings(t.Scopes),
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
	}
	v.LastUsedAt = fmtTimePtr(t.LastUsedAt)
	v.ExpiresAt = fmtTimePtr(t.ExpiresAt)
	v.RevokedAt = fmtTimePtr(t.RevokedAt)
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
	items := make([]tokenView, 0, len(tokens))
	for _, t := range tokens {
		items = append(items, toTokenView(t))
	}
	writeJSON(w, http.StatusOK, listResponse[tokenView]{Items: items})
}

type createTokenRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at,omitempty"`
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
	if len(req.Scopes) == 0 {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "at least one scope is required")
		return
	}
	for _, s := range req.Scopes {
		if !auth.IsKnownScope(auth.Scope(s)) {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "unknown scope: "+s)
			return
		}
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
		Scopes: scopesJSON, ExpiresAt: expiresAt,
	}
	if err := h.store.CreateToken(r.Context(), tok); err != nil {
		h.internalServerError(w, "create token", err)
		return
	}
	_ = h.audit.Record(r.Context(), &audit.Entry{
		Action: audit.ActionTokenCreate, Actor: actor(r), Target: tok.Name,
	})
	writeJSON(w, http.StatusCreated, createTokenResponse{tokenView: toTokenView(tok), Token: secret})
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
