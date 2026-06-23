package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const nodeCols = `id, collection_id, parent_id, path, name, kind, blob_id, size,
	content_type, checksum, metadata, created_at, updated_at, last_access_at,
	visibility, expires_at`

func scanNode(row interface{ Scan(...any) error }) (*Node, error) {
	var (
		n           Node
		parentID    sql.NullInt64
		blobID      sql.NullString
		size        sql.NullInt64
		contentType sql.NullString
		checksum    sql.NullString
		meta        string
		created     string
		updated     string
		lastAccess  sql.NullString
		visibility  sql.NullString
		expires     sql.NullString
	)
	if err := row.Scan(
		&n.ID, &n.CollectionID, &parentID, &n.Path, &n.Name, &n.Kind,
		&blobID, &size, &contentType, &checksum, &meta,
		&created, &updated, &lastAccess, &visibility, &expires,
	); err != nil {
		return nil, err
	}

	n.ParentID = int64Ptr(parentID)
	n.BlobID = strPtr(blobID)
	n.Size = int64Ptr(size)
	n.ContentType = strPtr(contentType)
	n.Checksum = strPtr(checksum)
	n.Metadata = []byte(meta)
	n.Visibility = strPtr(visibility)

	var err error
	if n.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if n.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	if n.LastAccessAt, err = parseTimePtr(lastAccess); err != nil {
		return nil, err
	}
	if n.ExpiresAt, err = parseTimePtr(expires); err != nil {
		return nil, err
	}
	return &n, nil
}

// UpsertNode inserts a node or updates the existing one with the same
// (collection_id, path), preserving created_at and last_access_at on update.
// The node's ID is populated on return.
func (s *Store) UpsertNode(ctx context.Context, n *Node) error {
	now := s.now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now

	err := s.db.sql.QueryRowContext(
		ctx,
		`INSERT INTO nodes
			(collection_id, parent_id, path, name, kind, blob_id, size, content_type,
			 checksum, metadata, created_at, updated_at, last_access_at, visibility, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(collection_id, path) DO UPDATE SET
			parent_id    = excluded.parent_id,
			name         = excluded.name,
			kind         = excluded.kind,
			blob_id      = excluded.blob_id,
			size         = excluded.size,
			content_type = excluded.content_type,
			checksum     = excluded.checksum,
			metadata     = excluded.metadata,
			updated_at   = excluded.updated_at,
			visibility   = excluded.visibility,
			expires_at   = excluded.expires_at
		 RETURNING id`,
		n.CollectionID, n.ParentID, n.Path, n.Name, n.Kind, n.BlobID, n.Size, n.ContentType,
		n.Checksum, defaultJSON(n.Metadata), fmtTime(n.CreatedAt), fmtTime(n.UpdatedAt),
		fmtTimePtr(n.LastAccessAt), n.Visibility, fmtTimePtr(n.ExpiresAt),
	).Scan(&n.ID)
	return err
}

// GetNode returns a node by (collection, path).
func (s *Store) GetNode(ctx context.Context, collectionID int64, path string) (*Node, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+nodeCols+` FROM nodes WHERE collection_id = ? AND path = ?`, collectionID, path)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// GetNodeByID returns a node by id.
func (s *Store) GetNodeByID(ctx context.Context, id int64) (*Node, error) {
	row := s.db.sql.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// ListChildren returns the direct children of a parent node within a
// collection. A nil parentID lists the collection's root-level nodes.
func (s *Store) ListChildren(ctx context.Context, collectionID int64, parentID *int64) ([]*Node, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if parentID == nil {
		rows, err = s.db.sql.QueryContext(ctx,
			`SELECT `+nodeCols+` FROM nodes WHERE collection_id = ? AND parent_id IS NULL ORDER BY name`,
			collectionID)
	} else {
		rows, err = s.db.sql.QueryContext(ctx,
			`SELECT `+nodeCols+` FROM nodes WHERE collection_id = ? AND parent_id = ? ORDER BY name`,
			collectionID, *parentID)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return collectNodes(rows)
}

// ListCollectionNodes returns all nodes in a collection ordered by path.
func (s *Store) ListCollectionNodes(ctx context.Context, collectionID int64) ([]*Node, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+nodeCols+` FROM nodes WHERE collection_id = ? ORDER BY path`, collectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectNodes(rows)
}

func collectNodes(rows *sql.Rows) ([]*Node, error) {
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DeleteNode removes a node and (via parent_id cascade) its descendants.
func (s *Store) DeleteNode(ctx context.Context, id int64) error {
	res, err := s.db.sql.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// TouchLastAccess sets last_access_at on a node.
func (s *Store) TouchLastAccess(ctx context.Context, id int64, at time.Time) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE nodes SET last_access_at = ? WHERE id = ?`, fmtTime(at), id)
	if err != nil {
		return err
	}
	return requireOne(res)
}
