package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/store"
)

// seedBootstrapToken seeds cfg's bootstrap admin token (ARTIFACTSD_BOOTSTRAP_TOKEN,
// design/12 §2.1) as an admin-scoped API token, unless a token with that hash
// already exists. This gives a fresh deployment a way into the management API
// without direct DB access; it is a no-op when the token is empty or already
// seeded, so it is safe to run on every boot.
func seedBootstrapToken(ctx context.Context, st *store.Store, token string, log *slog.Logger) error {
	if token == "" {
		return nil
	}
	hash := auth.HashToken(token)
	if _, err := st.GetTokenByHash(ctx, hash); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := st.CreateToken(ctx, &store.APIToken{
		Name:        "bootstrap",
		TokenHash:   hash,
		TokenPrefix: auth.TokenPrefix(token),
		Scopes:      []byte(`["admin"]`),
	}); err != nil {
		return err
	}
	log.Info("seeded bootstrap admin token", "prefix", auth.TokenPrefix(token))
	return nil
}
