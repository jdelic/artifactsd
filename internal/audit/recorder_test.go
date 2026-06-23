package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeSink struct {
	entries []*Entry
	err     error
}

func (f *fakeSink) AppendAudit(_ context.Context, e *Entry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

func TestRecorder_PersistsAndStamps(t *testing.T) {
	sink := &fakeSink{}
	fixed := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	r := NewRecorder(sink, nil).WithClock(func() time.Time { return fixed })

	ctx := WithRequestID(context.Background(), "req-1")
	if err := r.RecordAccess(ctx, "tok_abcd", "demo/secret.pdf"); err != nil {
		t.Fatalf("RecordAccess: %v", err)
	}
	if len(sink.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(sink.entries))
	}
	e := sink.entries[0]
	if e.Action != ActionObjectAccess || e.Actor != "tok_abcd" || e.Target != "demo/secret.pdf" {
		t.Errorf("entry = %+v", e)
	}
	if !e.Timestamp.Equal(fixed) {
		t.Errorf("timestamp = %v, want %v", e.Timestamp, fixed)
	}
	if e.RequestID != "req-1" {
		t.Errorf("request id = %q, want req-1 (from context)", e.RequestID)
	}
}

func TestRecorder_MirrorsToStderr(t *testing.T) {
	var buf strings.Builder
	log := NewAppLogger(&buf, "info")
	r := NewRecorder(&fakeSink{}, log)

	_ = r.RecordDeletion(context.Background(), ActorJanitor, ActionExpiryDelete, "demo/old.txt", nil)

	out := buf.String()
	for _, want := range []string{"audit", ActionExpiryDelete, ActorJanitor, "demo/old.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr mirror missing %q in %q", want, out)
		}
	}
}

func TestRecorder_ReturnsAndLogsPersistError(t *testing.T) {
	var buf strings.Builder
	log := NewAppLogger(&buf, "info")
	sink := &fakeSink{err: errors.New("db down")}
	r := NewRecorder(sink, log)

	err := r.RecordAccess(context.Background(), "user", "t")
	if err == nil {
		t.Fatal("expected persist error to propagate")
	}
	if !strings.Contains(buf.String(), "persist_error") {
		t.Errorf("persist error not mirrored to stderr: %q", buf.String())
	}
}
