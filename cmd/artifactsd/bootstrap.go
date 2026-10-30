package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/store"
)

// seedBootstrapToken seeds cfg's bootstrap admin token (ARTIFACTSD_BOOTSTRAP_TOKEN,
// design/12 §2.1) as an admin-scoped API token, unless a token with that hash
// already exists. This gives a fresh deployment a way into the management API
// without direct DB access; it is a no-op when the token is empty or already
// seeded, so it is safe to run on every boot.
//
// The stored row is hashed and prefixed via auth.NormalizeBootstrapToken
// (tok_ added unless already present), not the raw operator value: every other
// token carries the tok_ prefix, and auth.Authenticator's WithBootstrapToken
// normalizes a presented bootstrap secret the same way before looking it up,
// so the two sides stay consistent.
func seedBootstrapToken(ctx context.Context, st *store.Store, token string, log *slog.Logger) error {
	if token == "" {
		return nil
	}
	normalized := auth.NormalizeBootstrapToken(token)
	hash := auth.HashToken(normalized)
	if _, err := st.GetTokenByHash(ctx, hash); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := revokeLegacyBootstrapToken(ctx, st, token, log); err != nil {
		return err
	}
	if err := st.CreateToken(ctx, &store.APIToken{
		Name:        "bootstrap",
		TokenHash:   hash,
		TokenPrefix: auth.TokenPrefix(normalized),
		Scopes:      []byte(`["admin"]`),
	}); err != nil {
		return err
	}
	log.Info("seeded bootstrap admin token", "prefix", auth.TokenPrefix(normalized))
	return nil
}

// revokeLegacyBootstrapToken revokes the row older releases seeded for a
// bootstrap secret that already started with tok_: they always prepended the
// prefix, storing it under the hash of "tok_tok_...". That row would otherwise
// linger as a second live admin token once the secret is re-seeded under its
// correct hash.
func revokeLegacyBootstrapToken(ctx context.Context, st *store.Store, token string, log *slog.Logger) error {
	if !strings.HasPrefix(token, auth.TokenPrefixValue) {
		return nil
	}
	legacy, err := st.GetTokenByHash(ctx, auth.HashToken(auth.TokenPrefixValue+token))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if legacy.RevokedAt != nil {
		return nil
	}
	if err := st.RevokeToken(ctx, legacy.ID, time.Now().UTC()); err != nil {
		return err
	}
	log.Info("revoked legacy double-prefixed bootstrap token", "id", legacy.ID, "prefix", legacy.TokenPrefix)
	return nil
}
