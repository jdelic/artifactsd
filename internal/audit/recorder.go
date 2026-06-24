package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// Audit actions (design/02 §3, design/06 §7).
const (
	ActionObjectAccess     = "object.access"
	ActionCollectionCreate = "collection.create"
	ActionCollectionDelete = "collection.delete"
	ActionFilePut          = "file.put"
	ActionFileDelete       = "file.delete"
	ActionUploadBegin      = "upload.begin"
	ActionUploadComplete   = "upload.complete"
	ActionShareCreate      = "share.create"
	ActionShareRevoke      = "share.revoke"
	ActionTokenCreate      = "token.create"
	ActionTokenRevoke      = "token.revoke"
	ActionExpiryDelete     = "expiry.delete"
	ActionMaintenanceRun   = "maintenance.cleanup"
)

// ActorJanitor is the actor recorded for janitor-driven deletions.
const ActorJanitor = "janitor"

// ActorUpload is the actor recorded when a large-upload PUT completes a
// session; the caller is identified only by the unguessable upload_id
// capability, never an authenticated principal (design/07 §3, design/14 §5).
const ActorUpload = "upload"

// Entry is one audit record. It is persisted in the audit_logs table and
// mirrored to stderr. It must never carry secrets: Details is caller-sanitized
// (the API returns hash-presence, never cleartext — design/06 §2).
type Entry struct {
	Timestamp time.Time
	Actor     string
	Action    string
	Target    string
	Details   json.RawMessage
	RequestID string
}

// Sink persists audit entries (implemented by the store; kept as an interface so
// the recorder stays decoupled and testable).
type Sink interface {
	AppendAudit(ctx context.Context, e *Entry) error
}

// Recorder writes audit entries to a Sink and mirrors them to the app logger
// (stderr). The DB is the system of record; the stderr mirror aids operators.
type Recorder struct {
	sink Sink
	log  *slog.Logger
	now  func() time.Time
}

// NewRecorder builds a Recorder. log may be nil to skip the stderr mirror.
func NewRecorder(sink Sink, log *slog.Logger) *Recorder {
	return &Recorder{sink: sink, log: log, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock (tests).
func (r *Recorder) WithClock(now func() time.Time) *Recorder {
	r.now = now
	return r
}

// Record persists and mirrors an audit entry. A missing timestamp/request-id is
// filled from the clock/context. The persist error is returned (and logged);
// the mirror is best-effort.
func (r *Recorder) Record(ctx context.Context, e *Entry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = r.now()
	}
	if e.RequestID == "" {
		e.RequestID = RequestIDFromContext(ctx)
	}

	err := r.sink.AppendAudit(ctx, e)
	if r.log != nil {
		attrs := []slog.Attr{
			slog.String("action", e.Action),
			slog.String("actor", e.Actor),
			slog.String("target", e.Target),
		}
		if e.RequestID != "" {
			attrs = append(attrs, slog.String("request_id", e.RequestID))
		}
		if len(e.Details) > 0 {
			attrs = append(attrs, slog.String("details", string(e.Details)))
		}
		if err != nil {
			attrs = append(attrs, slog.String("persist_error", err.Error()))
			r.log.LogAttrs(ctx, slog.LevelError, "audit", attrs...)
		} else {
			r.log.LogAttrs(ctx, slog.LevelInfo, "audit", attrs...)
		}
	}
	return err
}

// RecordAccess records successful authenticated access to a protected/
// auth-required object (decision N6). actor is the authenticated principal
// (OIDC sub/email, basic username, or bearer token prefix — never the token).
// Public/unauthenticated reads are NOT audited (they go to the stdout access
// log only).
func (r *Recorder) RecordAccess(ctx context.Context, actor, target string) error {
	return r.Record(ctx, &Entry{Action: ActionObjectAccess, Actor: actor, Target: target})
}

// RecordDeletion records a deletion (manual or janitor).
func (r *Recorder) RecordDeletion(ctx context.Context, actor, action, target string, details json.RawMessage) error {
	return r.Record(ctx, &Entry{Action: action, Actor: actor, Target: target, Details: details})
}
