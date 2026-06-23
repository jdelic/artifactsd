package store

import (
	"context"

	"github.com/jdelic/artifactsd/internal/audit"
)

// AuditSink adapts the store to audit.Sink so the audit recorder can persist
// entries into the audit_logs table.
type AuditSink struct {
	s *Store
}

// AuditSink returns an audit.Sink backed by this store.
func (s *Store) AuditSink() *AuditSink {
	return &AuditSink{s: s}
}

// AppendAudit implements audit.Sink, mapping the audit.Entry to a stored row.
// Empty actor/target/request-id are stored as NULL.
func (a *AuditSink) AppendAudit(ctx context.Context, e *audit.Entry) error {
	row := &AuditLog{
		Timestamp: e.Timestamp,
		Action:    e.Action,
		Actor:     emptyToNil(e.Actor),
		Target:    emptyToNil(e.Target),
		RequestID: emptyToNil(e.RequestID),
		Details:   e.Details,
	}
	return a.s.AppendAudit(ctx, row)
}

func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
