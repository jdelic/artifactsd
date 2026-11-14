package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound is returned by lookups when no matching row exists.
var ErrNotFound = errors.New("store: not found")

// ErrKindConflict is returned by UpsertNode when a write would violate the
// file/folder tree invariants: a path's kind cannot flip between file and
// folder in place, and a node's parent must be a folder (artifactsd-2bw).
var ErrKindConflict = errors.New("store: path kind conflict")

// ErrCollectionFull is returned when creating a file would exceed the
// per-collection file cap (WithMaxFilesPerCollection).
var ErrCollectionFull = errors.New("store: collection has reached its file limit")

// Clock returns the current time; injectable so tests can use a fixed clock.
type Clock func() time.Time

// Store provides typed CRUD over the SQLite database. Methods keep transactions
// short and rely on SQLite's ON DELETE CASCADE for referential cleanup.
type Store struct {
	db  *DB
	now Clock
	// maxFilesPerCollection caps file nodes per collection (0 = unlimited).
	maxFilesPerCollection int
	// defaultDeleteAfterCreation is the server-wide default object expiry
	// (ARTIFACTSD_DEFAULT_DELETE_AFTER_CREATION). When non-zero it is applied
	// during denormalization to any collection/node whose own metadata sets no
	// expiry, so new content inherits the configured default (artifactsd-8yy).
	defaultDeleteAfterCreation time.Duration
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

// WithMaxFilesPerCollection caps the number of file nodes per collection
// (ARTIFACTSD_MAX_FILES_PER_COLLECTION). Creating a new file beyond it fails
// with ErrCollectionFull; replacing a file and creating folders are unaffected.
// Zero or negative disables the cap.
func (s *Store) WithMaxFilesPerCollection(n int) *Store {
	s.maxFilesPerCollection = n
	return s
}

// MaxFilesPerCollection returns the configured cap (0 = unlimited).
func (s *Store) MaxFilesPerCollection() int { return max(s.maxFilesPerCollection, 0) }

// WithDefaultDeleteAfterCreation sets the server-wide default object expiry
// applied during denormalization when an object sets no expiry of its own.
func (s *Store) WithDefaultDeleteAfterCreation(d time.Duration) *Store {
	s.defaultDeleteAfterCreation = d
	return s
}

// applyDefaultExpiry returns exp unchanged when it is already set or no default
// is configured; otherwise it is createdAt + the configured default.
func (s *Store) applyDefaultExpiry(exp *time.Time, createdAt time.Time) *time.Time {
	if exp != nil || s.defaultDeleteAfterCreation <= 0 {
		return exp
	}
	t := createdAt.Add(s.defaultDeleteAfterCreation).UTC()
	return &t
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
