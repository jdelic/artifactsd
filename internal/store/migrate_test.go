package store

import (
	"context"
	"path/filepath"
	"testing"
)

// openTestDB opens a fresh migrated database in a temp dir.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), DBFilename))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrate_AppliesOnFreshDB(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	highest, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if highest < 1 {
		t.Errorf("highest version = %d, want ≥1", highest)
	}

	// All expected tables should exist.
	for _, table := range []string{
		"collections", "nodes", "shares", "upload_sessions",
		"api_tokens", "audit_logs", "schema_migrations",
		"upload_requests", "upload_request_submissions",
	} {
		var name string
		err := db.SQL().QueryRowContext(
			ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var count int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	// Count should equal the number of migration files (each applied once).
	var fileCount int
	entries, _ := migrationFS.ReadDir("migrations")
	for _, e := range entries {
		if !e.IsDir() {
			fileCount++
		}
	}
	if count != fileCount {
		t.Errorf("schema_migrations rows = %d, want %d after double migrate", count, fileCount)
	}
}

func TestMigrate_ForeignKeysEnforced(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Inserting a node referencing a non-existent collection must fail with FK on.
	_, err := db.SQL().ExecContext(ctx,
		`INSERT INTO nodes (collection_id, path, name, kind, metadata, created_at, updated_at)
		 VALUES (999, 'x', 'x', 'file', '{}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("expected foreign key violation, got nil")
	}
}

func TestMigrate_CascadeDeletesNodes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const now = "2026-01-01T00:00:00Z"
	res, err := db.SQL().ExecContext(ctx,
		`INSERT INTO collections (name, created_at, updated_at) VALUES ('demo', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert collection: %v", err)
	}
	cid, _ := res.LastInsertId()
	if _, err := db.SQL().ExecContext(ctx,
		`INSERT INTO nodes (collection_id, path, name, kind, metadata, created_at, updated_at)
		 VALUES (?, 'a.txt', 'a.txt', 'file', '{}', ?, ?)`, cid, now, now); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	if _, err := db.SQL().ExecContext(ctx, `DELETE FROM collections WHERE id=?`, cid); err != nil {
		t.Fatalf("delete collection: %v", err)
	}
	var n int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&n); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if n != 0 {
		t.Errorf("nodes after cascade = %d, want 0", n)
	}
}

func TestParseMigrationName(t *testing.T) {
	v, name, err := parseMigrationName("0001_init.sql")
	if err != nil || v != 1 || name != "init" {
		t.Fatalf("parseMigrationName = (%d,%q,%v), want (1,init,nil)", v, name, err)
	}
	if _, _, err := parseMigrationName("bad.sql"); err == nil {
		t.Error("expected error for malformed name")
	}
}
