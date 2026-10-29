package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.DBFilename))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store.New(db)
}

func TestSeedBootstrapToken_Empty(t *testing.T) {
	st := openTestStore(t)
	log := audit.NewAppLogger(io.Discard, "error")
	if err := seedBootstrapToken(context.Background(), st, "", log); err != nil {
		t.Fatalf("seedBootstrapToken: %v", err)
	}
	toks, err := st.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(toks) != 0 {
		t.Errorf("tokens = %d, want 0 for an empty bootstrap token", len(toks))
	}
}

func TestSeedBootstrapToken_SeedsOnce(t *testing.T) {
	st := openTestStore(t)
	log := audit.NewAppLogger(io.Discard, "error")
	ctx := context.Background()

	if err := seedBootstrapToken(ctx, st, "ci-secret", log); err != nil {
		t.Fatalf("seedBootstrapToken: %v", err)
	}
	// Re-seeding with the same token (e.g. a restart) must not duplicate it.
	if err := seedBootstrapToken(ctx, st, "ci-secret", log); err != nil {
		t.Fatalf("seedBootstrapToken (2nd boot): %v", err)
	}

	toks, err := st.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(toks) != 1 {
		t.Fatalf("tokens = %d, want exactly 1 after two boots", len(toks))
	}
	if string(toks[0].Scopes) != `["admin"]` {
		t.Errorf("scopes = %s, want admin", toks[0].Scopes)
	}
}

// A bootstrap secret that already carries tok_ is stored under its own hash,
// not "tok_tok_...", and a row seeded that way by an older release is revoked
// so it does not linger as a second live admin token.
func TestSeedBootstrapToken_PrefixedSecret(t *testing.T) {
	st := openTestStore(t)
	log := audit.NewAppLogger(io.Discard, "error")
	ctx := context.Background()

	legacy := &store.APIToken{
		Name: "bootstrap", TokenHash: auth.HashToken("tok_tok_ci-secret"),
		TokenPrefix: auth.TokenPrefix("tok_tok_ci-secret"), Scopes: []byte(`["admin"]`),
	}
	if err := st.CreateToken(ctx, legacy); err != nil {
		t.Fatalf("CreateToken legacy: %v", err)
	}

	if err := seedBootstrapToken(ctx, st, "tok_ci-secret", log); err != nil {
		t.Fatalf("seedBootstrapToken: %v", err)
	}
	seeded, err := st.GetTokenByHash(ctx, auth.HashToken("tok_ci-secret"))
	if err != nil {
		t.Fatalf("seeded token not stored under the secret's own hash: %v", err)
	}
	if seeded.TokenPrefix != auth.TokenPrefix("tok_ci-secret") {
		t.Errorf("prefix = %q, want %q", seeded.TokenPrefix, auth.TokenPrefix("tok_ci-secret"))
	}
	old, err := st.GetTokenByHash(ctx, legacy.TokenHash)
	if err != nil {
		t.Fatalf("GetTokenByHash legacy: %v", err)
	}
	if old.RevokedAt == nil {
		t.Error("legacy double-prefixed bootstrap token was not revoked")
	}
}
