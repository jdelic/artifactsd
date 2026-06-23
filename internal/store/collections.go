package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CreateCollection inserts a new collection. Metadata may be nil ("{}").
// Visibility and ExpiresAt are the denormalized effective values.
func (s *Store) CreateCollection(ctx context.Context, c *Collection) error {
	now := s.now()
	c.CreatedAt = now
	c.UpdatedAt = now
	if c.Visibility == "" {
		c.Visibility = "private"
	}
	res, err := s.db.sql.ExecContext(
		ctx,
		`INSERT INTO collections (name, metadata, created_at, updated_at, visibility, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.Name, defaultJSON(c.Metadata), fmtTime(now), fmtTime(now), c.Visibility, fmtTimePtr(c.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("store: create collection %q: %w", c.Name, err)
	}
	c.ID, err = res.LastInsertId()
	return err
}

const collectionCols = `id, name, metadata, created_at, updated_at, visibility, expires_at`

func scanCollection(row interface{ Scan(...any) error }) (*Collection, error) {
	var (
		c          Collection
		meta       string
		created    string
		updated    string
		expires    sql.NullString
		visibility string
	)
	if err := row.Scan(&c.ID, &c.Name, &meta, &created, &updated, &visibility, &expires); err != nil {
		return nil, err
	}
	c.Metadata = []byte(meta)
	c.Visibility = visibility
	var err error
	if c.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	if c.ExpiresAt, err = parseTimePtr(expires); err != nil {
		return nil, err
	}
	return &c, nil
}

// GetCollection returns a collection by id.
func (s *Store) GetCollection(ctx context.Context, id int64) (*Collection, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+collectionCols+` FROM collections WHERE id = ?`, id)
	c, err := scanCollection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// GetCollectionByName returns a collection by name.
func (s *Store) GetCollectionByName(ctx context.Context, name string) (*Collection, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+collectionCols+` FROM collections WHERE name = ?`, name)
	c, err := scanCollection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListCollections returns all collections ordered by name.
func (s *Store) ListCollections(ctx context.Context) ([]*Collection, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+collectionCols+` FROM collections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Collection
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCollectionMetadata replaces a collection's metadata and denormalized
// visibility/expiry, bumping updated_at.
func (s *Store) UpdateCollectionMetadata(ctx context.Context, c *Collection) error {
	c.UpdatedAt = s.now()
	res, err := s.db.sql.ExecContext(
		ctx,
		`UPDATE collections SET metadata = ?, visibility = ?, expires_at = ?, updated_at = ?
		 WHERE id = ?`,
		defaultJSON(c.Metadata), c.Visibility, fmtTimePtr(c.ExpiresAt), fmtTime(c.UpdatedAt), c.ID,
	)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// DeleteCollection removes a collection and (via cascade) its nodes, shares,
// and upload sessions.
func (s *Store) DeleteCollection(ctx context.Context, id int64) error {
	res, err := s.db.sql.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// requireOne maps a zero-rows-affected result to ErrNotFound.
func requireOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
