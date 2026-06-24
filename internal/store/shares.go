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
	_, exp, err := DeriveDenormalized(sh.Metadata, sh.CreatedAt)
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

// UpdateShare replaces a share's metadata and re-derives its denormalized
// expires_at from the new metadata (PATCH). The share's original created_at is
// used as the basis for any creation-relative expiry.
func (s *Store) UpdateShare(ctx context.Context, sh *Share) error {
	_, exp, err := DeriveDenormalized(sh.Metadata, sh.CreatedAt)
	if err != nil {
		return err
	}
	sh.ExpiresAt = exp
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE shares SET metadata = ?, expires_at = ? WHERE share_id = ?`,
		defaultJSON(sh.Metadata), fmtTimePtr(sh.ExpiresAt), sh.ShareID)
	if err != nil {
		return err
	}
	return requireOne(res)
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
