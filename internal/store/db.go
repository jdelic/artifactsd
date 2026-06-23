// Package store provides SQLite-backed persistence for artifactsd: schema
// migrations, models, and CRUD for collections, nodes, shares, upload sessions,
// API tokens, and audit logs (design/02).
//
// The database is a single local SQLite file opened in WAL mode with foreign
// keys enforced. Writes are serialized by SQLite; transactions are kept short.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (decision B2)
)

// DBFilename is the SQLite database file name inside the data directory.
const DBFilename = "artifactsd.db"

// DB wraps the SQL connection pool with artifactsd-specific helpers.
type DB struct {
	sql *sql.DB
}

// openOptions are the connection pragmas applied to every connection.
//
//   - journal_mode=WAL: concurrent readers alongside a writer (persistent).
//   - foreign_keys=ON: enforce ON DELETE CASCADE (per-connection in SQLite).
//   - busy_timeout: wait rather than fail immediately on a locked DB.
var openPragmas = []string{
	"journal_mode(WAL)",
	"foreign_keys(ON)",
	"busy_timeout(5000)",
}

// Open opens (creating if needed) the SQLite database at dbPath.
func Open(ctx context.Context, dbPath string) (*DB, error) {
	dsn := dsnFor(dbPath)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}
	// SQLite is single-writer; a small pool avoids "database is locked" churn
	// while still allowing concurrent reads under WAL.
	sqldb.SetMaxOpenConns(1)
	sqldb.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqldb.PingContext(pingCtx); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("store: ping %s: %w", dbPath, err)
	}
	return &DB{sql: sqldb}, nil
}

// OpenInDir opens the database at <dataDir>/artifactsd.db.
func OpenInDir(ctx context.Context, dataDir string) (*DB, error) {
	return Open(ctx, filepath.Join(dataDir, DBFilename))
}

// dsnFor builds a modernc.org/sqlite DSN with the connection pragmas applied.
func dsnFor(dbPath string) string {
	q := url.Values{}
	for _, p := range openPragmas {
		q.Add("_pragma", p)
	}
	return "file:" + dbPath + "?" + q.Encode()
}

// SQL exposes the underlying *sql.DB for packages that build their own queries.
func (db *DB) SQL() *sql.DB { return db.sql }

// Close closes the connection pool.
func (db *DB) Close() error { return db.sql.Close() }
