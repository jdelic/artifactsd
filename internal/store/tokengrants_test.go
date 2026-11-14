package store

import (
	"errors"
	"testing"
)

func TestTokenGrants_Store(t *testing.T) {
	s, ctx := newStore(t)
	alpha := mustCollection(t, s, ctx, "alpha")
	beta := mustCollection(t, s, ctx, "beta")

	plain := &APIToken{Name: "plain", TokenHash: "h1", TokenPrefix: "tok_1", Scopes: []byte(`["collections:read"]`)}
	if err := s.CreateToken(ctx, plain); err != nil {
		t.Fatal(err)
	}
	// Grants only attach to restricted tokens.
	if err := s.SetTokenGrant(ctx, &TokenGrant{TokenID: plain.ID, CollectionID: alpha.ID, Scopes: []byte(`["collections:read"]`)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("grant on unrestricted token = %v, want ErrNotFound", err)
	}
	if err := s.CreateTokenWithGrants(ctx, &APIToken{Name: "x", TokenHash: "h9"}, []*TokenGrant{{CollectionID: alpha.ID}}); err == nil {
		t.Error("CreateTokenWithGrants accepted grants on an unrestricted token")
	}

	agent := &APIToken{Name: "agent", TokenHash: "h2", TokenPrefix: "tok_2", Restricted: true}
	if err := s.CreateTokenWithGrants(ctx, agent, []*TokenGrant{
		{CollectionID: beta.ID, Scopes: []byte(`["collections:read"]`)},
		{CollectionID: alpha.ID, Scopes: []byte(`["collections:write"]`)},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetToken(ctx, agent.ID)
	if err != nil || !got.Restricted {
		t.Fatalf("GetToken = %+v, %v", got, err)
	}
	grants, err := s.TokenGrants(ctx, agent.ID)
	if err != nil || len(grants) != 2 || grants[0].CollectionName != "alpha" || grants[1].CollectionName != "beta" {
		t.Fatalf("TokenGrants = %+v, %v", grants, err)
	}

	// Upsert replaces the scopes.
	if err := s.SetTokenGrant(ctx, &TokenGrant{TokenID: agent.ID, CollectionID: beta.ID, Scopes: []byte(`["shares:read"]`)}); err != nil {
		t.Fatal(err)
	}
	grants, _ = s.TokenGrants(ctx, agent.ID)
	if string(grants[1].Scopes) != `["shares:read"]` {
		t.Errorf("replaced grant scopes = %s", grants[1].Scopes)
	}

	// Deleting a collection drops its grants.
	if err := s.DeleteCollection(ctx, alpha.ID); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListTokenGrants(ctx)
	if err != nil || len(all[agent.ID]) != 1 || all[agent.ID][0].CollectionName != "beta" {
		t.Errorf("ListTokenGrants after delete = %+v, %v", all[agent.ID], err)
	}

	if err := s.DeleteTokenGrant(ctx, agent.ID, beta.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTokenGrant(ctx, agent.ID, beta.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteTokenGrant = %v, want ErrNotFound", err)
	}

	// The auth adapter loads grants for restricted tokens.
	if err := s.SetTokenGrant(ctx, &TokenGrant{TokenID: agent.ID, CollectionID: beta.ID, Scopes: []byte(`["collections:read"]`)}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.AuthTokenSource().LookupToken(ctx, "h2")
	if err != nil || !rec.Restricted || len(rec.Grants) != 1 || !rec.Grants[beta.ID].Has("collections:read") {
		t.Errorf("LookupToken = %+v, %v", rec, err)
	}
}
