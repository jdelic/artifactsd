package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const uploadCols = `id, upload_id, collection_id, target_path, metadata, content_type,
	status, temp_blob_id, created_at, expires_at, completed_at`

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
		&u.Status, &tempBlob, &created, &expires, &completed,
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
			(upload_id, collection_id, target_path, metadata, content_type, status, temp_blob_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.UploadID, u.CollectionID, u.TargetPath, defaultJSON(u.Metadata), u.ContentType,
		u.Status, u.TempBlobID, fmtTime(u.CreatedAt), fmtTime(u.ExpiresAt),
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

// CompleteUploadSession marks a session completed with its final temp blob.
func (s *Store) CompleteUploadSession(ctx context.Context, uploadID, tempBlobID string, at time.Time) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE upload_sessions SET status = ?, temp_blob_id = ?, completed_at = ?
		 WHERE upload_id = ? AND status = ?`,
		UploadCompleted, tempBlobID, fmtTime(at), uploadID, UploadPending)
	if err != nil {
		return err
	}
	return requireOne(res)
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
