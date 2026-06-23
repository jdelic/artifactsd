package store

import (
	"context"
	"database/sql"
)

const auditCols = `id, ts, actor, action, target, details, request_id`

func scanAudit(row interface{ Scan(...any) error }) (*AuditLog, error) {
	var (
		a         AuditLog
		ts        string
		actor     sql.NullString
		target    sql.NullString
		details   sql.NullString
		requestID sql.NullString
	)
	if err := row.Scan(&a.ID, &ts, &actor, &a.Action, &target, &details, &requestID); err != nil {
		return nil, err
	}
	a.Actor = strPtr(actor)
	a.Target = strPtr(target)
	a.RequestID = strPtr(requestID)
	if details.Valid {
		a.Details = []byte(details.String)
	}
	var err error
	if a.Timestamp, err = parseTime(ts); err != nil {
		return nil, err
	}
	return &a, nil
}

// AppendAudit records an audit log entry (admin actions + deletions, SPEC §17).
// There is no access_logs table — access logs stream to stdout (decision F5).
func (s *Store) AppendAudit(ctx context.Context, a *AuditLog) error {
	if a.Timestamp.IsZero() {
		a.Timestamp = s.now()
	}
	var details any
	if len(a.Details) > 0 {
		details = string(a.Details)
	}
	res, err := s.db.sql.ExecContext(
		ctx,
		`INSERT INTO audit_logs (ts, actor, action, target, details, request_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fmtTime(a.Timestamp), a.Actor, a.Action, a.Target, details, a.RequestID,
	)
	if err != nil {
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}

// ListAudit returns the most recent audit entries, newest first, up to limit.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]*AuditLog, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+auditCols+` FROM audit_logs ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*AuditLog
	for rows.Next() {
		a, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
