package store

import (
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
)

// TestAuditSink_PersistsThroughRecorder records via the audit.Recorder and the
// store sink, then reads it back from the audit_logs table.
func TestAuditSink_PersistsThroughRecorder(t *testing.T) {
	s, ctx := newStore(t)
	rec := audit.NewRecorder(s.AuditSink(), nil).WithClock(func() time.Time { return fixedNow })

	if err := rec.RecordAccess(ctx, "tok_ci0012", "demo/report.pdf"); err != nil {
		t.Fatalf("RecordAccess: %v", err)
	}
	if err := rec.RecordDeletion(ctx, audit.ActorJanitor, audit.ActionExpiryDelete, "demo/old.txt", []byte(`{"reason":"expired"}`)); err != nil {
		t.Fatalf("RecordDeletion: %v", err)
	}

	logs, err := s.ListAudit(ctx, 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(logs))
	}

	// Newest first: the deletion, then the access.
	if logs[0].Action != audit.ActionExpiryDelete || logs[0].Actor == nil || *logs[0].Actor != audit.ActorJanitor {
		t.Errorf("deletion row = %+v", logs[0])
	}
	if logs[1].Action != audit.ActionObjectAccess || logs[1].Target == nil || *logs[1].Target != "demo/report.pdf" {
		t.Errorf("access row = %+v", logs[1])
	}
}
