package store

import (
	"context"
	"database/sql"
	"time"
)

// ListExpiredNodes returns up to limit nodes (files or folders) whose
// denormalized expires_at has passed, earliest first (design/08 §2). Deleting
// a returned folder cascades its descendants, which may include other rows
// this query also returned — callers should treat a subsequent ErrNotFound on
// delete as already-handled, not an error.
func (s *Store) ListExpiredNodes(ctx context.Context, now time.Time, limit int) ([]*Node, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+nodeCols+` FROM nodes WHERE expires_at IS NOT NULL AND expires_at <= ? ORDER BY expires_at LIMIT ?`,
		fmtTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

// ListExpiredCollections returns up to limit collections whose denormalized
// expires_at has passed, earliest first. Deletion cascades all of a
// collection's nodes, shares, and upload sessions (design/08 §2/§4).
func (s *Store) ListExpiredCollections(ctx context.Context, now time.Time, limit int) ([]*Collection, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+collectionCols+` FROM collections WHERE expires_at IS NOT NULL AND expires_at <= ? ORDER BY expires_at LIMIT ?`,
		fmtTime(now), limit)
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

// ListExpiredShares returns up to limit shares whose expires_at has passed,
// earliest first. A share is a pointer into a collection's tree, so deleting it
// frees no blobs.
func (s *Store) ListExpiredShares(ctx context.Context, now time.Time, limit int) ([]*Share, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+shareCols+` FROM shares WHERE expires_at IS NOT NULL AND expires_at <= ? ORDER BY expires_at LIMIT ?`,
		fmtTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// NodeBlobRefs returns the distinct, non-null blob refs of a node subtree
// (pathPrefix matches itself and any descendant path) or, when pathPrefix is
// "", every blob ref in the collection. Callers gather these *before* deleting
// the owning node/collection (whose cascade removes the rows), then re-check
// BlobRefCount per ref afterward to decide whether to unlink the blob
// (design/08 §4 step 2; blobs are content-addressed and may be shared across
// collections via dedup, design/07 §1).
func (s *Store) NodeBlobRefs(ctx context.Context, collectionID int64, pathPrefix string) ([]string, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if pathPrefix == "" {
		rows, err = s.db.sql.QueryContext(ctx,
			`SELECT DISTINCT blob_id FROM nodes WHERE collection_id = ? AND blob_id IS NOT NULL`, collectionID)
	} else {
		rows, err = s.db.sql.QueryContext(ctx,
			`SELECT DISTINCT blob_id FROM nodes
			 WHERE collection_id = ? AND blob_id IS NOT NULL AND (path = ? OR path LIKE ? || '/%')`,
			collectionID, pathPrefix, pathPrefix)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// BlobRefCount reports how many nodes still reference a blob ref, so the
// janitor can decide whether unlinking it is safe (design/08 §4 step 4/§5).
func (s *Store) BlobRefCount(ctx context.Context, blobID string) (int, error) {
	var n int
	err := s.db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE blob_id = ?`, blobID).Scan(&n)
	return n, err
}
