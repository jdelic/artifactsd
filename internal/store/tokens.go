package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

// tokenCols is a SQL column list, not a credential.
const tokenCols = `id, name, token_hash, token_prefix, scopes, created_at,
	last_used_at, expires_at, revoked_at, restricted`

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
		&lastUsed, &expires, &revoked, &t.Restricted,
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
	return s.CreateTokenWithGrants(ctx, t, nil)
}

// CreateTokenWithGrants inserts a new API token and its collection grants in
// one transaction (design/16 §5). Grants need CollectionID and Scopes.
func (s *Store) CreateTokenWithGrants(ctx context.Context, t *APIToken, grants []*TokenGrant) error {
	if len(grants) > 0 && !t.Restricted {
		return errors.New("store: grants on an unrestricted token")
	}
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	t.CreatedAt = s.now()
	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO api_tokens (name, token_hash, token_prefix, scopes, created_at, expires_at, restricted)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.Name, t.TokenHash, t.TokenPrefix, defaultJSONArray(t.Scopes),
		fmtTime(t.CreatedAt), fmtTimePtr(t.ExpiresAt), t.Restricted,
	)
	if err != nil {
		return err
	}
	if t.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	for _, g := range grants {
		g.TokenID = t.ID
		if err := s.setTokenGrant(ctx, tx, g); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetToken looks up a token by id.
func (s *Store) GetToken(ctx context.Context, id int64) (*APIToken, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+tokenCols+` FROM api_tokens WHERE id = ?`, id)
	t, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// SetTokenGrant creates or replaces one grant (design/16 §5). The token must
// be restricted.
func (s *Store) SetTokenGrant(ctx context.Context, g *TokenGrant) error {
	return s.setTokenGrant(ctx, s.db.sql, g)
}

func (s *Store) setTokenGrant(ctx context.Context, q querier, g *TokenGrant) error {
	g.CreatedAt = s.now()
	res, err := q.ExecContext(ctx,
		`INSERT INTO token_grants (token_id, collection_id, scopes, created_at)
		 SELECT ?1, ?2, ?3, ?4 FROM api_tokens WHERE id = ?1 AND restricted
		 ON CONFLICT (token_id, collection_id) DO UPDATE SET scopes = excluded.scopes`,
		g.TokenID, g.CollectionID, defaultJSONArray(g.Scopes), fmtTime(g.CreatedAt))
	if err != nil {
		return err
	}
	return requireOne(res)
}

// DeleteTokenGrant removes one grant; ErrNotFound if there was none.
func (s *Store) DeleteTokenGrant(ctx context.Context, tokenID, collectionID int64) error {
	res, err := s.db.sql.ExecContext(ctx,
		`DELETE FROM token_grants WHERE token_id = ? AND collection_id = ?`, tokenID, collectionID)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// ListTokenGrants returns all grants keyed by token id, each with its
// collection name, ordered by collection name.
func (s *Store) ListTokenGrants(ctx context.Context) (map[int64][]*TokenGrant, error) {
	return s.queryTokenGrants(ctx, 0, 0)
}

// CollectionGrants returns the grants on one collection, ordered by token
// name (for the admin UI's per-collection view).
func (s *Store) CollectionGrants(ctx context.Context, collectionID int64) ([]*TokenGrant, error) {
	m, err := s.queryTokenGrants(ctx, 0, collectionID)
	if err != nil {
		return nil, err
	}
	var out []*TokenGrant
	for _, gs := range m {
		out = append(out, gs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TokenName < out[j].TokenName })
	return out, nil
}

// TokenGrants returns one token's grants, ordered by collection name.
func (s *Store) TokenGrants(ctx context.Context, tokenID int64) ([]*TokenGrant, error) {
	m, err := s.queryTokenGrants(ctx, tokenID, 0)
	if err != nil {
		return nil, err
	}
	return m[tokenID], nil
}

// queryTokenGrants lists grants keyed by token id, filtered to one token
// and/or one collection (0 = no filter).
func (s *Store) queryTokenGrants(ctx context.Context, tokenID, collectionID int64) (map[int64][]*TokenGrant, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT g.token_id, g.collection_id, c.name, t.name, t.revoked_at IS NOT NULL, g.scopes, g.created_at
		 FROM token_grants g
		 JOIN collections c ON c.id = g.collection_id
		 JOIN api_tokens t ON t.id = g.token_id
		 WHERE (?1 = 0 OR g.token_id = ?1) AND (?2 = 0 OR g.collection_id = ?2)
		 ORDER BY c.name`, tokenID, collectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[int64][]*TokenGrant{}
	for rows.Next() {
		var (
			g       TokenGrant
			scopes  string
			created string
		)
		if err := rows.Scan(&g.TokenID, &g.CollectionID, &g.CollectionName, &g.TokenName, &g.TokenRevoked, &scopes, &created); err != nil {
			return nil, err
		}
		g.Scopes = []byte(scopes)
		if g.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out[g.TokenID] = append(out[g.TokenID], &g)
	}
	return out, rows.Err()
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
