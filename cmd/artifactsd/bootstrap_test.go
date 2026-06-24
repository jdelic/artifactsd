package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
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
