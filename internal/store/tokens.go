package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// tokenCols is a SQL column list, not a credential.
const tokenCols = `id, name, token_hash, token_prefix, scopes, created_at,
	last_used_at, expires_at, revoked_at`

func scanToken(row interface{ Scan(...any) error }) (*APIToken, error) {
	var (
		t        APIToken
		scopes   string
		created  string
		lastUsed sql.NullString
		expires  sql.NullString
		revoked  sql.NullString
	)
	if err := row.Scan(
		&t.ID, &t.Name, &t.TokenHash, &t.TokenPrefix, &scopes, &created,
		&lastUsed, &expires, &revoked,
	); err != nil {
		return nil, err
	}
	t.Scopes = []byte(scopes)
	var err error
	if t.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if t.LastUsedAt, err = parseTimePtr(lastUsed); err != nil {
		return nil, err
	}
	if t.ExpiresAt, err = parseTimePtr(expires); err != nil {
		return nil, err
	}
	if t.RevokedAt, err = parseTimePtr(revoked); err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateToken inserts a new API token. Only the hash is stored.
func (s *Store) CreateToken(ctx context.Context, t *APIToken) error {
	t.CreatedAt = s.now()
	res, err := s.db.sql.ExecContext(
		ctx,
		`INSERT INTO api_tokens (name, token_hash, token_prefix, scopes, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.Name, t.TokenHash, t.TokenPrefix, defaultJSONArray(t.Scopes),
		fmtTime(t.CreatedAt), fmtTimePtr(t.ExpiresAt),
	)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// GetTokenByHash looks up a token by its sha256 hash (the auth path).
func (s *Store) GetTokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+tokenCols+` FROM api_tokens WHERE token_hash = ?`, hash)
	t, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListTokens returns all tokens, newest first.
func (s *Store) ListTokens(ctx context.Context) ([]*APIToken, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+tokenCols+` FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TouchTokenUsed records the last time a token was used for auth.
func (s *Store) TouchTokenUsed(ctx context.Context, id int64, at time.Time) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, fmtTime(at), id)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// RevokeToken marks a token revoked.
func (s *Store) RevokeToken(ctx context.Context, id int64, at time.Time) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, fmtTime(at), id)
	if err != nil {
		return err
	}
	return requireOne(res)
}
