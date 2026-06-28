package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const uploadRequestCols = `id, request_id, collection_id, target_folder, label, description,
	file_metadata, allow_uploader_access, single_use, use_submission_folders,
	max_files, max_file_size, files_uploaded, status, created_at, expires_at, revoked_at, last_used_at`

func scanUploadRequest(row interface{ Scan(...any) error }) (*UploadRequest, error) {
	var (
		ur          UploadRequest
		label       sql.NullString
		description sql.NullString
		meta        string
		maxFiles    sql.NullInt64
		maxFileSize sql.NullInt64
		created     string
		expires     sql.NullString
		revoked     sql.NullString
		lastUsed    sql.NullString
	)
	if err := row.Scan(
		&ur.ID, &ur.RequestID, &ur.CollectionID, &ur.TargetFolder,
		&label, &description, &meta,
		&ur.AllowUploaderAccess, &ur.SingleUse, &ur.UseSubmissionFolders,
		&maxFiles, &maxFileSize, &ur.FilesUploaded,
		&ur.Status, &created, &expires, &revoked, &lastUsed,
	); err != nil {
		return nil, err
	}
	ur.FileMetadata = []byte(meta)
	if label.Valid {
		ur.Label = &label.String
	}
	if description.Valid {
		ur.Description = &description.String
	}
	if maxFiles.Valid {
		ur.MaxFiles = &maxFiles.Int64
	}
	if maxFileSize.Valid {
		ur.MaxFileSize = &maxFileSize.Int64
	}
	var err error
	if ur.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if ur.ExpiresAt, err = parseTimePtr(expires); err != nil {
		return nil, err
	}
	if ur.RevokedAt, err = parseTimePtr(revoked); err != nil {
		return nil, err
	}
	if ur.LastUsedAt, err = parseTimePtr(lastUsed); err != nil {
		return nil, err
	}
	return &ur, nil
}

// CreateUploadRequest inserts a new upload request link.
func (s *Store) CreateUploadRequest(ctx context.Context, ur *UploadRequest) error {
	ur.CreatedAt = s.now()
	if ur.Status == "" {
		ur.Status = UploadRequestActive
	}
	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO upload_requests
			(request_id, collection_id, target_folder, label, description, file_metadata,
			 allow_uploader_access, single_use, use_submission_folders,
			 max_files, max_file_size, files_uploaded, status, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		ur.RequestID, ur.CollectionID, ur.TargetFolder,
		ur.Label, ur.Description, defaultJSON(ur.FileMetadata),
		boolToInt(ur.AllowUploaderAccess), boolToInt(ur.SingleUse), boolToInt(ur.UseSubmissionFolders),
		ur.MaxFiles, ur.MaxFileSize,
		ur.Status, fmtTime(ur.CreatedAt), fmtTimePtr(ur.ExpiresAt),
	)
	if err != nil {
		return err
	}
	ur.ID, err = res.LastInsertId()
	return err
}

// GetUploadRequest returns an upload request by its public request_id.
func (s *Store) GetUploadRequest(ctx context.Context, requestID string) (*UploadRequest, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+uploadRequestCols+` FROM upload_requests WHERE request_id = ?`, requestID)
	ur, err := scanUploadRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ur, err
}

// GetUploadRequestByID returns an upload request by its internal primary key.
func (s *Store) GetUploadRequestByID(ctx context.Context, id int64) (*UploadRequest, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+uploadRequestCols+` FROM upload_requests WHERE id = ?`, id)
	ur, err := scanUploadRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ur, err
}

// ListUploadRequests returns all upload requests ordered by creation time (newest
// first). A non-nil collectionID filters to one collection.
func (s *Store) ListUploadRequests(ctx context.Context, collectionID *int64) ([]*UploadRequest, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if collectionID != nil {
		rows, err = s.db.sql.QueryContext(ctx,
			`SELECT `+uploadRequestCols+` FROM upload_requests WHERE collection_id = ? ORDER BY created_at DESC`,
			*collectionID)
	} else {
		rows, err = s.db.sql.QueryContext(ctx,
			`SELECT `+uploadRequestCols+` FROM upload_requests ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*UploadRequest
	for rows.Next() {
		ur, err := scanUploadRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ur)
	}
	return out, rows.Err()
}

// UpdateUploadRequest saves mutable fields of an upload request (label,
// description, status, expires_at).
func (s *Store) UpdateUploadRequest(ctx context.Context, ur *UploadRequest) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE upload_requests
		 SET label = ?, description = ?, status = ?, expires_at = ?, revoked_at = ?
		 WHERE request_id = ?`,
		ur.Label, ur.Description, ur.Status,
		fmtTimePtr(ur.ExpiresAt), fmtTimePtr(ur.RevokedAt),
		ur.RequestID,
	)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// CloseUploadRequest sets status to closed/revoked and records revoked_at.
func (s *Store) CloseUploadRequest(ctx context.Context, requestID, status string, at time.Time) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE upload_requests SET status = ?, revoked_at = ?
		 WHERE request_id = ? AND status = ?`,
		status, fmtTime(at), requestID, UploadRequestActive)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// CommitUploadRequestSubmission creates a submission record, increments the
// files_uploaded counter, and (when single_use is true) closes the request —
// all in one transaction so the state is consistent even under concurrent POSTs.
func (s *Store) CommitUploadRequestSubmission(
	ctx context.Context,
	sub *UploadRequestSubmission,
	singleUse bool,
	now time.Time,
) error {
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Re-read status inside the transaction to ensure we win the race.
	var status string
	var filesUploaded, maxFiles int64
	var maxFilesNull sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT status, files_uploaded, max_files FROM upload_requests WHERE id = ?`,
		sub.UploadRequestID,
	).Scan(&status, &filesUploaded, &maxFilesNull); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if maxFilesNull.Valid {
		maxFiles = maxFilesNull.Int64
	}
	if status != UploadRequestActive {
		return ErrUploadRequestNotActive
	}
	if maxFiles > 0 && filesUploaded+sub.FileCount > maxFiles {
		return ErrUploadRequestQuotaExceeded
	}

	sub.CreatedAt = now
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO upload_request_submissions
			(upload_request_id, submission_id, file_count, total_bytes, client_ip, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sub.UploadRequestID, sub.SubmissionID, sub.FileCount, sub.TotalBytes,
		sub.ClientIP, fmtTime(sub.CreatedAt),
	); err != nil {
		return err
	}

	newStatus := UploadRequestActive
	if singleUse {
		newStatus = UploadRequestClosed
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE upload_requests
		 SET files_uploaded = files_uploaded + ?, last_used_at = ?, status = ?
		 WHERE id = ?`,
		sub.FileCount, fmtTime(now), newStatus, sub.UploadRequestID,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// ListUploadRequestSubmissions returns submissions for one request, newest first.
func (s *Store) ListUploadRequestSubmissions(ctx context.Context, uploadRequestID int64) ([]*UploadRequestSubmission, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, upload_request_id, submission_id, file_count, total_bytes, client_ip, created_at
		 FROM upload_request_submissions WHERE upload_request_id = ? ORDER BY created_at DESC`,
		uploadRequestID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*UploadRequestSubmission
	for rows.Next() {
		var sub UploadRequestSubmission
		var clientIP sql.NullString
		var created string
		if err := rows.Scan(
			&sub.ID, &sub.UploadRequestID, &sub.SubmissionID,
			&sub.FileCount, &sub.TotalBytes, &clientIP, &created,
		); err != nil {
			return nil, err
		}
		if clientIP.Valid {
			sub.ClientIP = &clientIP.String
		}
		if sub.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, &sub)
	}
	return out, rows.Err()
}

// ListExpiredUploadRequests returns active upload requests whose expires_at has
// passed, up to limit (for the janitor to mark expired).
func (s *Store) ListExpiredUploadRequests(ctx context.Context, now time.Time, limit int) ([]*UploadRequest, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+uploadRequestCols+` FROM upload_requests
		 WHERE status = ? AND expires_at IS NOT NULL AND expires_at <= ? ORDER BY expires_at LIMIT ?`,
		UploadRequestActive, fmtTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*UploadRequest
	for rows.Next() {
		ur, err := scanUploadRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ur)
	}
	return out, rows.Err()
}

// SetUploadRequestStatus sets an upload request's status (e.g. expired).
func (s *Store) SetUploadRequestStatus(ctx context.Context, requestID, status string) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE upload_requests SET status = ? WHERE request_id = ?`, status, requestID)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// boolToInt converts a bool to 0/1 for SQLite BOOLEAN columns.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Sentinel errors for upload-request operations.
var (
	ErrUploadRequestNotActive    = errors.New("store: upload request is not active")
	ErrUploadRequestQuotaExceeded = errors.New("store: upload request max_files quota exceeded")
)
