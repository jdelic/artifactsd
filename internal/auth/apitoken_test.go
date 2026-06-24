package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeTokenStore is an in-memory TokenStore for tests.
type fakeTokenStore struct {
	byHash map[string]*TokenRecord
	used   map[int64]time.Time
}

func newFakeStore() *fakeTokenStore {
	return &fakeTokenStore{byHash: map[string]*TokenRecord{}, used: map[int64]time.Time{}}
}

func (f *fakeTokenStore) add(token string, rec *TokenRecord) {
	f.byHash[HashToken(token)] = rec
}

func (f *fakeTokenStore) LookupToken(_ context.Context, hash string) (*TokenRecord, error) {
	if rec, ok := f.byHash[hash]; ok {
		return rec, nil
	}
	return nil, ErrUnknownToken
}

func (f *fakeTokenStore) MarkTokenUsed(_ context.Context, id int64, at time.Time) error {
	f.used[id] = at
	return nil
}

func apiReq(authz string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/collections", http.NoBody)
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	return r
}

var fixedClock = func() time.Time { return time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC) }

func TestAuthenticate_Success(t *testing.T) {
	store := newFakeStore()
	store.add("tok_secret", &TokenRecord{ID: 7, Name: "ci", Prefix: "tok_secret", Scopes: NewScopeSet(ScopeCollectionsWrite)})
	a := NewAuthenticator(store).WithClock(fixedClock)

	p, err := a.Authenticate(context.Background(), apiReq("Bearer tok_secret"))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.TokenID != 7 || p.Name != "ci" {
		t.Errorf("principal = %+v", p)
	}
	if store.used[7].IsZero() {
		t.Error("MarkTokenUsed not called")
	}
}

func TestAuthenticate_Errors(t *testing.T) {
	store := newFakeStore()
	expired := fixedClock().Add(-time.Hour)
	revoked := fixedClock().Add(-time.Hour)
	store.add("tok_expired", &TokenRecord{ID: 1, ExpiresAt: &expired, Scopes: NewScopeSet(ScopeAdmin)})
	store.add("tok_revoked", &TokenRecord{ID: 2, RevokedAt: &revoked, Scopes: NewScopeSet(ScopeAdmin)})
	a := NewAuthenticator(store).WithClock(fixedClock)
	ctx := context.Background()

	cases := []struct {
		authz string
		want  error
	}{
		{"", ErrNoCredentials},
		{"Basic abc", ErrNoCredentials},
		{"Bearer not-an-api-token", ErrNoCredentials}, // missing tok_ prefix
		{"Bearer tok_unknown", ErrUnknownToken},
		{"Bearer tok_expired", ErrTokenExpired},
		{"Bearer tok_revoked", ErrTokenRevoked},
	}
	for _, c := range cases {
		_, err := a.Authenticate(ctx, apiReq(c.authz))
		if !errors.Is(err, c.want) {
			t.Errorf("Authenticate(%q) err = %v, want %v", c.authz, err, c.want)
		}
	}
}

func TestAuthenticate_BootstrapToken(t *testing.T) {
	store := newFakeStore()
	// Seeded the same way cmd/artifactsd/bootstrap.go does: hashed/prefixed as
	// tok_+token even though the operator's raw secret carries no prefix.
	store.add("tok_raw-secret-no-prefix", &TokenRecord{ID: 9, Name: "bootstrap", Scopes: NewScopeSet(ScopeAdmin)})
	a := NewAuthenticator(store).WithClock(fixedClock).WithBootstrapToken("raw-secret-no-prefix")

	p, err := a.Authenticate(context.Background(), apiReq("Bearer raw-secret-no-prefix"))
	if err != nil {
		t.Fatalf("Authenticate with unprefixed bootstrap token: %v", err)
	}
	if p.TokenID != 9 {
		t.Errorf("principal = %+v, want TokenID 9", p)
	}

	// A token that merely starts with the bootstrap value, or differs in any
	// other way, must not match.
	if _, err := a.Authenticate(context.Background(), apiReq("Bearer raw-secret-no-prefix-extra")); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("Authenticate with extended token: err = %v, want ErrNoCredentials", err)
	}

	// A regular tok_-prefixed token still authenticates normally even when a
	// bootstrap token is configured.
	store.add("tok_normal", &TokenRecord{ID: 1, Scopes: NewScopeSet(ScopeAdmin)})
	if _, err := a.Authenticate(context.Background(), apiReq("Bearer tok_normal")); err != nil {
		t.Errorf("Authenticate with a normal token: %v", err)
	}
}

func TestMiddleware_ScopeEnforcement(t *testing.T) {
	store := newFakeStore()
	store.add("tok_reader", &TokenRecord{ID: 1, Prefix: "tok_reader", Scopes: NewScopeSet(ScopeCollectionsRead)})
	a := NewAuthenticator(store).WithClock(fixedClock)

	type errCall struct {
		status int
		code   string
	}
	var last *errCall
	onError := func(w http.ResponseWriter, _ *http.Request, status int, code, _ string) {
		last = &errCall{status, code}
		w.WriteHeader(status)
	}

	var reached bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reached = true
		if p := PrincipalFromContext(r.Context()); p == nil || p.TokenID != 1 {
			t.Error("principal not in context")
		}
	})

	// Has collections:read, requires collections:read → allowed.
	mw := a.Middleware(ScopeCollectionsRead, onError)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, apiReq("Bearer tok_reader"))
	if !reached {
		t.Error("handler not reached for authorized request")
	}

	// Requires collections:write → 403 forbidden.
	reached, last = false, nil
	mwWrite := a.Middleware(ScopeCollectionsWrite, onError)
	rec = httptest.NewRecorder()
	mwWrite(next).ServeHTTP(rec, apiReq("Bearer tok_reader"))
	if reached {
		t.Error("handler reached despite insufficient scope")
	}
	if last == nil || last.status != http.StatusForbidden || last.code != "forbidden" {
		t.Errorf("scope failure response = %+v, want 403 forbidden", last)
	}

	// No credentials → 401 unauthorized.
	reached, last = false, nil
	rec = httptest.NewRecorder()
	mw(next).ServeHTTP(rec, apiReq(""))
	if reached || last == nil || last.status != http.StatusUnauthorized {
		t.Errorf("missing-credentials response = %+v, want 401", last)
	}
}
