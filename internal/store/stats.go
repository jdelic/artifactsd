package store

import "context"

// Stats is a point-in-time summary of stored objects and storage usage, served
// by the management stats endpoint (design/04 §8).
type Stats struct {
	Collections  int64 `json:"collections"`
	Files        int64 `json:"files"`
	Folders      int64 `json:"folders"`
	Shares       int64 `json:"shares"`
	ActiveTokens int64 `json:"active_tokens"`
	Blobs        int64 `json:"blobs"`         // distinct content-addressed blobs
	StorageBytes int64 `json:"storage_bytes"` // bytes occupied by distinct blobs
}

// Stats computes the storage summary in a handful of indexed aggregate queries.
// StorageBytes counts each content-addressed blob once even when several nodes
// reference it (content-addressed dedup).
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	var st Stats
	row := s.db.sql.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM collections),
		(SELECT COUNT(*) FROM nodes WHERE kind = 'file'),
		(SELECT COUNT(*) FROM nodes WHERE kind = 'folder'),
		(SELECT COUNT(*) FROM shares),
		(SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL)`)
	if err := row.Scan(&st.Collections, &st.Files, &st.Folders, &st.Shares, &st.ActiveTokens); err != nil {
		return nil, err
	}

	blobRow := s.db.sql.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM (
		SELECT blob_id, MAX(size) AS size FROM nodes
		WHERE blob_id IS NOT NULL GROUP BY blob_id)`)
	if err := blobRow.Scan(&st.Blobs, &st.StorageBytes); err != nil {
		return nil, err
	}
	return &st, nil
}
