package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound is returned by lookups when no matching row exists.
var ErrNotFound = errors.New("store: not found")

// Clock returns the current time; injectable so tests can use a fixed clock.
type Clock func() time.Time

// Store provides typed CRUD over the SQLite database. Methods keep transactions
// short and rely on SQLite's ON DELETE CASCADE for referential cleanup.
type Store struct {
	db  *DB
	now Clock
}

// New returns a Store backed by db, using the wall clock (UTC).
func New(db *DB) *Store {
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock (for tests).
func (s *Store) WithClock(c Clock) *Store {
	s.now = c
	return s
}

// DB exposes the underlying database handle.
func (s *Store) DB() *DB { return s.db }

// --- time / json helpers ---------------------------------------------------

// timeLayout is the canonical on-disk timestamp format (sortable as text).
const timeLayout = time.RFC3339Nano

func fmtTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// fmtTimePtr renders a nullable timestamp as a SQL argument (string or NULL).
func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

// parseTimePtr parses a nullable timestamp column.
func parseTimePtr(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := time.Parse(timeLayout, ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeLayout, s)
}

// defaultJSON returns "{}" for empty metadata so the NOT NULL column is honored.
func defaultJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

// defaultJSONArray returns "[]" for empty arrays (e.g. token scopes).
func defaultJSONArray(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "[]"
	}
	return string(raw)
}

// strPtr converts a NULL-able text column to *string.
func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

// int64Ptr converts a NULL-able integer column to *int64.
func int64Ptr(ni sql.NullInt64) *int64 {
	if !ni.Valid {
		return nil
	}
	return &ni.Int64
}
