package store

import (
	"context"
	"errors"
	"time"

	"github.com/jdelic/artifactsd/internal/auth"
)

// TokenSource adapts the store to auth.TokenStore so the management-API
// authenticator can resolve and track API tokens against the database.
type TokenSource struct {
	s *Store
}

// AuthTokenSource returns an auth.TokenStore backed by this store.
func (s *Store) AuthTokenSource() *TokenSource {
	return &TokenSource{s: s}
}

// LookupToken implements auth.TokenStore.
func (t *TokenSource) LookupToken(ctx context.Context, hash string) (*auth.TokenRecord, error) {
	tok, err := t.s.GetTokenByHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return nil, auth.ErrUnknownToken
	}
	if err != nil {
		return nil, err
	}
	scopes, err := auth.ParseScopes(tok.Scopes)
	if err != nil {
		return nil, err
	}
	rec := &auth.TokenRecord{
		ID:         tok.ID,
		Name:       tok.Name,
		Prefix:     tok.TokenPrefix,
		Scopes:     scopes,
		ExpiresAt:  tok.ExpiresAt,
		RevokedAt:  tok.RevokedAt,
		Restricted: tok.Restricted,
	}
	if tok.Restricted {
		grants, err := t.s.TokenGrants(ctx, tok.ID)
		if err != nil {
			return nil, err
		}
		rec.Grants = make(map[int64]auth.ScopeSet, len(grants))
		for _, g := range grants {
			set, err := auth.ParseScopes(g.Scopes)
			if err != nil {
				return nil, err
			}
			rec.Grants[g.CollectionID] = set
		}
	}
	return rec, nil
}

// MarkTokenUsed implements auth.TokenStore.
func (t *TokenSource) MarkTokenUsed(ctx context.Context, id int64, at time.Time) error {
	return t.s.TouchTokenUsed(ctx, id, at)
}
