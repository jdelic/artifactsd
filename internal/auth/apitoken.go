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
// carries no tok_ prefix, so Authenticate recognizes it by exact match and
// normalizes it to tok_+token before the usual hash lookup — matching how
// cmd/artifactsd/bootstrap.go seeded it into the store.
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
		token = TokenPrefixValue + token
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

	return &Principal{TokenID: rec.ID, Name: rec.Name, Prefix: rec.Prefix, Scopes: rec.Scopes}, nil
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
			if !principal.Scopes.Satisfies(required) {
				status, code, msg := statusFor(ErrInsufficientScope)
				onError(w, r, status, code, msg)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
		})
	}
}

// principalKey is the context key for the authenticated Principal.
type principalKey struct{}

// WithPrincipal returns a context carrying the principal.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext returns the authenticated principal, or nil.
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
