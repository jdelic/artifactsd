package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrincipal_GrantGates(t *testing.T) {
	p := &Principal{Restricted: true, Grants: map[int64]ScopeSet{
		1: NewScopeSet(ScopeCollectionsWrite),
		2: NewScopeSet(ScopeSharesRead),
	}}
	for _, tc := range []struct {
		scope Scope
		want  bool
	}{
		{ScopeCollectionsRead, true}, // implied by write on 1
		{ScopeCollectionsWrite, true},
		{ScopeSharesRead, true},
		{ScopeCollectionsDelete, false},
		{ScopeSharesWrite, false},
		{ScopeAdmin, false},
	} {
		if got := p.Allows(tc.scope); got != tc.want {
			t.Errorf("Allows(%s) = %v, want %v", tc.scope, got, tc.want)
		}
	}
	for _, tc := range []struct {
		id               int64
		scope            Scope
		visible, allowed bool
	}{
		{1, ScopeCollectionsWrite, true, true},
		{1, ScopeSharesRead, true, false},
		{2, ScopeCollectionsRead, true, false},
		{3, ScopeCollectionsRead, false, false},
	} {
		v, a := p.CollectionAccess(tc.id, tc.scope)
		if v != tc.visible || a != tc.allowed {
			t.Errorf("CollectionAccess(%d, %s) = %v,%v, want %v,%v", tc.id, tc.scope, v, a, tc.visible, tc.allowed)
		}
	}
	if p.IsAdmin() {
		t.Error("restricted principal is admin")
	}

	// Restricted with no grants reaches nothing.
	none := &Principal{Restricted: true}
	if none.Allows(ScopeCollectionsRead) {
		t.Error("grantless restricted principal passes the route gate")
	}
	// Unrestricted principals are unaffected by the collection gate.
	open := &Principal{Scopes: NewScopeSet(ScopeCollectionsRead)}
	if v, a := open.CollectionAccess(42, ScopeCollectionsRead); !v || !a {
		t.Error("unrestricted principal blocked by the collection gate")
	}
}

// TestAuthenticate_RestrictedIgnoresTokenScopes: a restricted token's
// token-level scopes never count, even if the row carries some (design/16 §2).
func TestAuthenticate_RestrictedIgnoresTokenScopes(t *testing.T) {
	store := newFakeStore()
	store.add("tok_agent", &TokenRecord{
		ID: 3, Name: "agent", Scopes: NewScopeSet(ScopeAdmin), Restricted: true,
		Grants: map[int64]ScopeSet{9: NewScopeSet(ScopeCollectionsRead)},
	})
	a := NewAuthenticator(store).WithClock(fixedClock)
	p, err := a.Authenticate(context.Background(), apiReq("Bearer tok_agent"))
	if err != nil {
		t.Fatal(err)
	}
	if p.IsAdmin() || p.Allows(ScopeAdmin) || len(p.Scopes) != 0 || !p.Allows(ScopeCollectionsRead) {
		t.Errorf("principal = %+v, want grant scopes only", p)
	}

	var seen Scope
	h := a.Middleware(ScopeCollectionsRead, func(w http.ResponseWriter, _ *http.Request, status int, _, _ string) {
		w.WriteHeader(status)
	})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequiredScopeFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), apiReq("Bearer tok_agent"))
	if seen != ScopeCollectionsRead {
		t.Errorf("required scope in context = %q", seen)
	}
}
