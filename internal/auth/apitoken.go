package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"
)

// API-token authentication errors.
var (
	ErrNoCredentials     = errors.New("auth: no bearer credentials")
	ErrUnknownToken      = errors.New("auth: unknown token")
	ErrTokenExpired      = errors.New("auth: token expired")
	ErrTokenRevoked      = errors.New("auth: token revoked")
	ErrInsufficientScope = errors.New("auth: insufficient scope")
)

// TokenRecord is the subset of a stored API token needed to authenticate a
// request. The concrete store maps its row into this shape.
type TokenRecord struct {
	ID        int64
	Name      string
	Prefix    string
	Scopes    ScopeSet
	ExpiresAt *time.Time
	RevokedAt *time.Time
	// Restricted tokens reach only the collections in Grants, each with that
	// grant's scopes; Scopes is then empty (design/16 §2).
	Restricted bool
	Grants     map[int64]ScopeSet
}

// TokenStore is the narrow dependency the Authenticator needs (decoupled from
// the store package to keep auth a low-level library).
type TokenStore interface {
	// LookupToken returns the token for a sha256 hex hash, or ErrUnknownToken.
	LookupToken(ctx context.Context, hash string) (*TokenRecord, error)
	// MarkTokenUsed records last-used; failures are non-fatal to the request.
	MarkTokenUsed(ctx context.Context, id int64, at time.Time) error
}

// Principal is an authenticated management-API caller.
type Principal struct {
	TokenID int64
	Name    string
	Prefix  string
	Scopes  ScopeSet
	// Restricted principals reach only the collections they hold a grant for
	// (keyed by collection id), with that grant's scopes (design/16 §2).
	Restricted bool
	Grants     map[int64]ScopeSet
}

// Allows is the route gate (design/16 §4): an unrestricted principal needs the
// scope itself; a restricted one needs it on at least one grant, and the
// collection gate (CollectionAccess) then checks the addressed collection.
func (p *Principal) Allows(required Scope) bool {
	if !p.Restricted {
		return p.Scopes.Satisfies(required)
	}
	if required == ScopeAdmin {
		return false
	}
	for _, g := range p.Grants {
		if g.Satisfies(required) {
			return true
		}
	}
	return false
}

// CollectionAccess is the collection gate (design/16 §4). visible reports
// whether the principal may know the collection exists at all; allowed
// whether it holds the required scope on it. Unrestricted principals are
// governed by the route gate alone.
func (p *Principal) CollectionAccess(collectionID int64, required Scope) (visible, allowed bool) {
	if !p.Restricted {
		return true, p.Scopes.Satisfies(required)
	}
	g, ok := p.Grants[collectionID]
	if !ok {
		return false, false
	}
	return true, required != ScopeAdmin && g.Satisfies(required)
}

// IsAdmin reports whether the principal holds the admin scope. Restricted
// principals never do (design/16 §2).
func (p *Principal) IsAdmin() bool {
	return p != nil && !p.Restricted && p.Scopes.Has(ScopeAdmin)
}

// Authenticator authenticates management-API requests by their bearer token.
type Authenticator struct {
	store          TokenStore
	now            func() time.Time
	bootstrapToken string
}

// NewAuthenticator builds an Authenticator over a token store.
func NewAuthenticator(store TokenStore) *Authenticator {
	return &Authenticator{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock (tests).
func (a *Authenticator) WithClock(now func() time.Time) *Authenticator {
	a.now = now
	return a
}

// WithBootstrapToken registers the operator-chosen bootstrap secret
// (ARTIFACTSD_BOOTSTRAP_TOKEN, design/12 §2.1). Unlike every other token, it
// need not carry the tok_ prefix, so Authenticate recognizes it by exact match
// and normalizes it with NormalizeBootstrapToken before the usual hash lookup
// — matching how cmd/artifactsd/bootstrap.go seeded it into the store.
func (a *Authenticator) WithBootstrapToken(token string) *Authenticator {
	a.bootstrapToken = token
	return a
}

// Authenticate resolves the API token on the request into a Principal. It
// returns ErrNoCredentials when no `Bearer tok_...` is present, or a specific
// error for unknown/expired/revoked tokens.
func (a *Authenticator) Authenticate(ctx context.Context, r *http.Request) (*Principal, error) {
	scheme, token, ok := parseAuthHeader(r)
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return nil, ErrNoCredentials
	}
	if a.bootstrapToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.bootstrapToken)) == 1 {
		token = NormalizeBootstrapToken(token)
	}
	if !strings.HasPrefix(token, TokenPrefixValue) {
		return nil, ErrNoCredentials
	}

	rec, err := a.store.LookupToken(ctx, HashToken(token))
	if err != nil {
		return nil, err
	}
	now := a.now()
	if rec.RevokedAt != nil {
		return nil, ErrTokenRevoked
	}
	if rec.ExpiresAt != nil && !now.Before(*rec.ExpiresAt) {
		return nil, ErrTokenExpired
	}

	// Best-effort last-used tracking; never fail the request on it.
	_ = a.store.MarkTokenUsed(ctx, rec.ID, now)

	p := &Principal{TokenID: rec.ID, Name: rec.Name, Prefix: rec.Prefix, Scopes: rec.Scopes, Restricted: rec.Restricted}
	if rec.Restricted {
		// Grant scopes only; never anything the token-level column says.
		p.Scopes = ScopeSet{}
		p.Grants = rec.Grants
	}
	return p, nil
}

// ErrorResponder writes an auth failure response. The HTTP server injects one
// that emits the API's JSON error envelope (design/04 §1); auth stays unaware
// of the wire format.
type ErrorResponder func(w http.ResponseWriter, r *http.Request, status int, code, message string)

// statusFor maps an auth error to an HTTP status and error code.
func statusFor(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, ErrInsufficientScope):
		return http.StatusForbidden, "forbidden", "insufficient scope"
	case errors.Is(err, ErrNoCredentials):
		return http.StatusUnauthorized, "unauthorized", "authentication required"
	case errors.Is(err, ErrTokenExpired):
		return http.StatusUnauthorized, "unauthorized", "token expired"
	case errors.Is(err, ErrTokenRevoked):
		return http.StatusUnauthorized, "unauthorized", "token revoked"
	default: // ErrUnknownToken and any store error
		return http.StatusUnauthorized, "unauthorized", "invalid credentials"
	}
}

// Middleware authorizes requests for the required scope, attaching the
// authenticated Principal to the request context. On failure it calls onError
// and does not invoke next.
func (a *Authenticator) Middleware(required Scope, onError ErrorResponder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := a.Authenticate(r.Context(), r)
			if err != nil {
				status, code, msg := statusFor(err)
				onError(w, r, status, code, msg)
				return
			}
			if !principal.Allows(required) {
				status, code, msg := statusFor(ErrInsufficientScope)
				onError(w, r, status, code, msg+": requires "+string(required))
				return
			}
			ctx := WithPrincipal(r.Context(), principal)
			ctx = context.WithValue(ctx, requiredScopeKey{}, required)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// principalKey is the context key for the authenticated Principal.
type principalKey struct{}

// requiredScopeKey is the context key for the scope the route gate checked.
type requiredScopeKey struct{}

// RequiredScopeFromContext returns the scope the route required (set by
// Middleware), so collection-level checks apply the same scope; "" if unset.
func RequiredScopeFromContext(ctx context.Context) Scope {
	sc, _ := ctx.Value(requiredScopeKey{}).(Scope)
	return sc
}

// WithPrincipal returns a context carrying the principal.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext returns the authenticated principal, or nil.
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
