package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const uploadCols = `id, upload_id, collection_id, target_path, metadata, content_type,
	status, temp_blob_id, created_at, expires_at, completed_at, metadata_supplied`

func scanUpload(row interface{ Scan(...any) error }) (*UploadSession, error) {
	var (
		u           UploadSession
		meta        string
		contentType sql.NullString
		tempBlob    sql.NullString
		created     string
		expires     string
		completed   sql.NullString
	)
	if err := row.Scan(
		&u.ID, &u.UploadID, &u.CollectionID, &u.TargetPath, &meta, &contentType,
		&u.Status, &tempBlob, &created, &expires, &completed, &u.MetadataSupplied,
	); err != nil {
		return nil, err
	}
	u.Metadata = []byte(meta)
	u.ContentType = strPtr(contentType)
	u.TempBlobID = strPtr(tempBlob)
	var err error
	if u.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if u.ExpiresAt, err = parseTime(expires); err != nil {
		return nil, err
	}
	if u.CompletedAt, err = parseTimePtr(completed); err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUploadSession inserts a new pending upload session.
func (s *Store) CreateUploadSession(ctx context.Context, u *UploadSession) error {
	u.CreatedAt = s.now()
	if u.Status == "" {
		u.Status = UploadPending
	}
	res, err := s.db.sql.ExecContext(
		ctx,
		`INSERT INTO upload_sessions
			(upload_id, collection_id, target_path, metadata, content_type, status, temp_blob_id, created_at, expires_at,
			 metadata_supplied)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.UploadID, u.CollectionID, u.TargetPath, defaultJSON(u.Metadata), u.ContentType,
		u.Status, u.TempBlobID, fmtTime(u.CreatedAt), fmtTime(u.ExpiresAt),
		boolToInt(u.MetadataSupplied),
	)
	if err != nil {
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

// GetUploadSession returns a session by its upload_id.
func (s *Store) GetUploadSession(ctx context.Context, uploadID string) (*UploadSession, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+uploadCols+` FROM upload_sessions WHERE upload_id = ?`, uploadID)
	u, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// CommitUpload completes a large-upload session and writes its file node in
// ONE transaction (artifactsd-m9e.13): it claims the session (pending →
// completed, recording n.BlobID; ErrNotFound when it is no longer pending),
// materializes the parent folders, upserts n and recomputes its denormalized
// columns. When the session was begun without metadata (MetadataSupplied
// false) and n replaces an existing file, that file's metadata — and its
// content type, if n has none — are kept (artifactsd-m9e.3). On any error
// everything rolls back and the session stays pending,
// so the client can retry or abort. Of two concurrent commits for one session
// exactly one succeeds (artifactsd-ku3). n.ParentID and n.ID are set on
// success.
func (s *Store) CommitUpload(ctx context.Context, uploadID string, n *Node, at time.Time) error {
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// The session must still be pending AND unexpired at commit time: it may
	// have expired while the body was streaming (artifactsd-7tl.3).
	var expires string
	err = tx.QueryRowContext(ctx,
		`SELECT expires_at FROM upload_sessions WHERE upload_id = ? AND status = ?`,
		uploadID, UploadPending).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	exp, err := parseTime(expires)
	if err != nil {
		return err
	}
	if !at.Before(exp) {
		return ErrUploadSessionExpired
	}
	var metadataSupplied bool
	err = tx.QueryRowContext(ctx,
		`UPDATE upload_sessions SET status = ?, temp_blob_id = ?, completed_at = ?
		 WHERE upload_id = ? AND status = ?
		 RETURNING metadata_supplied`,
		UploadCompleted, n.BlobID, fmtTime(at), uploadID, UploadPending).Scan(&metadataSupplied)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !metadataSupplied {
		existing, err := getNode(ctx, tx, n.CollectionID, n.Path)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return err
		case existing.Kind == KindFile:
			n.Metadata = existing.Metadata
			if n.ContentType == nil {
				n.ContentType = existing.ContentType
			}
		}
	}
	parentID, err := s.ensureParentFolders(ctx, tx, n.CollectionID, n.Path)
	if err != nil {
		return err
	}
	n.ParentID = parentID
	if err := s.upsertNode(ctx, tx, n); err != nil {
		return err
	}
	if err := s.recomputeNodeDenormalized(ctx, tx, n.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetUploadStatus updates a session's status (e.g. aborted/expired).
func (s *Store) SetUploadStatus(ctx context.Context, uploadID, status string) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE upload_sessions SET status = ? WHERE upload_id = ?`, status, uploadID)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// ListExpiredUploadSessions returns pending sessions whose expiry has passed,
// up to limit (for the janitor to reap).
func (s *Store) ListExpiredUploadSessions(ctx context.Context, now time.Time, limit int) ([]*UploadSession, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+uploadCols+` FROM upload_sessions
		 WHERE status = ? AND expires_at <= ? ORDER BY expires_at LIMIT ?`,
		UploadPending, fmtTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*UploadSession
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUploadSession removes a session row.
func (s *Store) DeleteUploadSession(ctx context.Context, uploadID string) error {
	res, err := s.db.sql.ExecContext(ctx, `DELETE FROM upload_sessions WHERE upload_id = ?`, uploadID)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// ErrUploadSessionExpired is returned by CommitUpload when the session's
// expiry has passed by commit time.
var ErrUploadSessionExpired = errors.New("store: upload session expired")
