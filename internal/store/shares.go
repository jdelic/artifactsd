package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const shareCols = `id, share_id, collection_id, target_path, target_kind, metadata,
	created_at, expires_at, revoked_at, last_access_at`

func scanShare(row interface{ Scan(...any) error }) (*Share, error) {
	var (
		sh         Share
		meta       string
		created    string
		expires    sql.NullString
		revoked    sql.NullString
		lastAccess sql.NullString
	)
	if err := row.Scan(
		&sh.ID, &sh.ShareID, &sh.CollectionID, &sh.TargetPath, &sh.TargetKind,
		&meta, &created, &expires, &revoked, &lastAccess,
	); err != nil {
		return nil, err
	}
	sh.Metadata = []byte(meta)
	var err error
	if sh.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if sh.ExpiresAt, err = parseTimePtr(expires); err != nil {
		return nil, err
	}
	if sh.RevokedAt, err = parseTimePtr(revoked); err != nil {
		return nil, err
	}
	if sh.LastAccessAt, err = parseTimePtr(lastAccess); err != nil {
		return nil, err
	}
	return &sh, nil
}

// CreateShare inserts a new share. The caller supplies the unguessable ShareID;
// the denormalized expires_at is derived from the share's metadata.
func (s *Store) CreateShare(ctx context.Context, sh *Share) error {
	sh.CreatedAt = s.now()
	_, exp, err := DeriveDenormalized(sh.Metadata, sh.CreatedAt, time.Time{})
	if err != nil {
		return err
	}
	sh.ExpiresAt = exp
	res, err := s.db.sql.ExecContext(
		ctx,
		`INSERT INTO shares (share_id, collection_id, target_path, target_kind, metadata, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sh.ShareID, sh.CollectionID, sh.TargetPath, sh.TargetKind,
		defaultJSON(sh.Metadata), fmtTime(sh.CreatedAt), fmtTimePtr(sh.ExpiresAt),
	)
	if err != nil {
		return err
	}
	sh.ID, err = res.LastInsertId()
	return err
}

// ResolveShareTarget resolves the target kind for a share being created.
// cleanedPath must already be validated and normalized (no leading slash, no
// traversal). An empty cleanedPath targets the whole collection and returns
// ("collection", nil). Otherwise the node at cleanedPath must exist; returns
// ErrNotFound when it does not. This is the shared core for both the
// management API and admin UI share-creation flows.
func (s *Store) ResolveShareTarget(ctx context.Context, collectionID int64, cleanedPath string) (kind string, err error) {
	if cleanedPath == "" {
		return "collection", nil
	}
	node, err := s.GetNode(ctx, collectionID, cleanedPath)
	if err != nil {
		return "", err
	}
	return node.Kind, nil
}

// GetShare returns a share by its public share_id.
func (s *Store) GetShare(ctx context.Context, shareID string) (*Share, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+shareCols+` FROM shares WHERE share_id = ?`, shareID)
	sh, err := scanShare(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sh, err
}

// ListSharesByCollection returns a collection's shares, newest first.
func (s *Store) ListSharesByCollection(ctx context.Context, collectionID int64) ([]*Share, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+shareCols+` FROM shares WHERE collection_id = ? ORDER BY created_at DESC`, collectionID)
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

// ShareWithCollection is a share plus its owning collection's name, for the
// admin cross-collection shares overview (artifactsd-rmn).
type ShareWithCollection struct {
	Share
	CollectionName string
}

// ListAllShares returns every share across all collections, newest first, each
// carrying its collection's name.
func (s *Store) ListAllShares(ctx context.Context) ([]*ShareWithCollection, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT sh.id, sh.share_id, sh.collection_id, sh.target_path, sh.target_kind, sh.metadata,
			sh.created_at, sh.expires_at, sh.revoked_at, sh.last_access_at, c.name
		 FROM shares sh JOIN collections c ON c.id = sh.collection_id
		 ORDER BY sh.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*ShareWithCollection
	for rows.Next() {
		var (
			sh         Share
			meta       string
			created    string
			expires    sql.NullString
			revoked    sql.NullString
			lastAccess sql.NullString
			collName   string
		)
		if err := rows.Scan(
			&sh.ID, &sh.ShareID, &sh.CollectionID, &sh.TargetPath, &sh.TargetKind,
			&meta, &created, &expires, &revoked, &lastAccess, &collName,
		); err != nil {
			return nil, err
		}
		sh.Metadata = []byte(meta)
		var perr error
		if sh.CreatedAt, perr = parseTime(created); perr != nil {
			return nil, perr
		}
		if sh.ExpiresAt, perr = parseTimePtr(expires); perr != nil {
			return nil, perr
		}
		if sh.RevokedAt, perr = parseTimePtr(revoked); perr != nil {
			return nil, perr
		}
		if sh.LastAccessAt, perr = parseTimePtr(lastAccess); perr != nil {
			return nil, perr
		}
		out = append(out, &ShareWithCollection{Share: sh, CollectionName: collName})
	}
	return out, rows.Err()
}

// UpdateShare replaces a share's metadata and re-derives its denormalized
// expires_at from the new metadata (PATCH). The share's original created_at is
// used as the basis for any creation-relative expiry; changing the idle-expiry
// rule restarts its clock (see idleRuleChanged*SQL).
func (s *Store) UpdateShare(ctx context.Context, sh *Share) error {
	// Validate the new metadata before writing anything.
	if _, _, err := DeriveDenormalized(sh.Metadata, sh.CreatedAt, time.Time{}); err != nil {
		return err
	}
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE shares SET
			last_access_at = CASE WHEN `+idleRuleChangedShareSQL+`
				THEN ?2 ELSE last_access_at END,
			metadata = ?1
		 WHERE share_id = ?3`,
		defaultJSON(sh.Metadata), fmtTime(s.now()), sh.ShareID)
	if err != nil {
		return err
	}
	if err := requireOne(res); err != nil {
		return err
	}
	cur, err := s.GetShare(ctx, sh.ShareID)
	if err != nil {
		return err
	}
	if err := s.persistShareExpiry(ctx, cur); err != nil {
		return err
	}
	sh.ExpiresAt, sh.LastAccessAt = cur.ExpiresAt, cur.LastAccessAt
	return nil
}

// TouchShareLastAccess sets last_access_at on a share, identified by its
// numeric row id.
func (s *Store) TouchShareLastAccess(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.sql.ExecContext(ctx,
		`UPDATE shares SET last_access_at = ? WHERE id = ?`, fmtTime(at), id)
	return err
}

// RecomputeShareDenormalized re-derives and persists a share's expires_at,
// identified by its numeric row id.
func (s *Store) RecomputeShareDenormalized(ctx context.Context, id int64) error {
	row := s.db.sql.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares WHERE id = ?`, id)
	sh, err := scanShare(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.persistShareExpiry(ctx, sh)
}

// persistShareExpiry re-derives sh's expires_at from its stored metadata,
// created_at, and last_access_at, persists it, and updates sh in place.
func (s *Store) persistShareExpiry(ctx context.Context, sh *Share) error {
	var lastAccess time.Time
	if sh.LastAccessAt != nil {
		lastAccess = *sh.LastAccessAt
	}
	_, exp, err := DeriveDenormalized(sh.Metadata, sh.CreatedAt, lastAccess)
	if err != nil {
		return err
	}
	if _, err := s.db.sql.ExecContext(ctx,
		`UPDATE shares SET expires_at = ? WHERE id = ?`, fmtTimePtr(exp), sh.ID); err != nil {
		return err
	}
	sh.ExpiresAt = exp
	return nil
}

// RevokeShare marks a share revoked at the given time.
func (s *Store) RevokeShare(ctx context.Context, shareID string, at time.Time) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE shares SET revoked_at = ? WHERE share_id = ? AND revoked_at IS NULL`,
		fmtTime(at), shareID)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// DeleteShare removes a share row.
func (s *Store) DeleteShare(ctx context.Context, shareID string) error {
	res, err := s.db.sql.ExecContext(ctx, `DELETE FROM shares WHERE share_id = ?`, shareID)
	if err != nil {
		return err
	}
	return requireOne(res)
}
