package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration is a single forward-only schema change.
type migration struct {
	version int
	name    string
	sql     string
}

// Migrate applies all pending migrations in version order inside a transaction
// each, recording applied versions in schema_migrations. It is idempotent:
// re-running a fully-migrated database is a no-op. Returns the highest version
// applied (or already present).
func (db *DB) Migrate(ctx context.Context) (int, error) {
	if err := ensureMigrationsTable(ctx, db.sql); err != nil {
		return 0, err
	}
	applied, err := appliedVersions(ctx, db.sql)
	if err != nil {
		return 0, err
	}

	migrations, err := loadMigrations()
	if err != nil {
		return 0, err
	}

	highest := 0
	for _, m := range migrations {
		if m.version > highest {
			highest = m.version
		}
		if applied[m.version] {
			continue
		}
		if err := applyMigration(ctx, db.sql, m); err != nil {
			return 0, fmt.Errorf("store: applying migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return highest, nil
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("store: creating schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: reading schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// loadMigrations parses the embedded migration files, sorted by version. Files
// are named NNNN_description.sql.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: reading embedded migrations: %w", err)
	}

	var migrations []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(e.Name())
		if err != nil {
			return nil, err
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, migration{version: version, name: name, sql: string(body)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	for i := 1; i < len(migrations); i++ {
		if migrations[i].version == migrations[i-1].version {
			return nil, fmt.Errorf("store: duplicate migration version %04d", migrations[i].version)
		}
	}
	return migrations, nil
}

func parseMigrationName(filename string) (version int, name string, err error) {
	base := strings.TrimSuffix(filename, ".sql")
	prefix, rest, ok := strings.Cut(base, "_")
	if !ok {
		return 0, "", fmt.Errorf("store: malformed migration name %q (want NNNN_description.sql)", filename)
	}
	version, err = strconv.Atoi(prefix)
	if err != nil {
		return 0, "", fmt.Errorf("store: migration %q has non-numeric version prefix: %w", filename, err)
	}
	return version, rest, nil
}
