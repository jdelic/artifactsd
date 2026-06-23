package audit

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// AccessEvent is one structured access-log record (design/01 §7). It is emitted
// to stdout as a single JSON line per request and is never persisted.
type AccessEvent struct {
	Time       time.Time `json:"time"`
	RequestID  string    `json:"request_id,omitempty"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	Bytes      int64     `json:"bytes"`
	DurationMS float64   `json:"duration_ms"`
	Remote     string    `json:"remote,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Collection string    `json:"collection,omitempty"`
	// Auth is the authentication/authorization outcome, e.g. "anonymous",
	// "token:ci", "denied".
	Auth string `json:"auth,omitempty"`
}

// AccessLogger writes AccessEvents as newline-delimited JSON to an underlying
// writer (stdout in production). It is safe for concurrent use.
type AccessLogger struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewAccessLogger returns an AccessLogger that writes to w.
func NewAccessLogger(w io.Writer) *AccessLogger {
	return &AccessLogger{enc: json.NewEncoder(w)}
}

// Log writes a single access event. Duration is stored as milliseconds. The
// event is taken by pointer to avoid copying on the per-request hot path.
func (l *AccessLogger) Log(e *AccessEvent) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Encoder.Encode appends a newline; one JSON object per line.
	_ = l.enc.Encode(e)
}
