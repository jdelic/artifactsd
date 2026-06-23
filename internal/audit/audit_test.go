package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"bogus":   slog.LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAppLogger_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := NewAppLogger(&buf, "warn")
	log.Info("should be filtered")
	log.Warn("should appear")
	out := buf.String()
	if strings.Contains(out, "should be filtered") {
		t.Errorf("info line leaked at warn level: %q", out)
	}
	if !strings.Contains(out, "should appear") {
		t.Errorf("warn line missing: %q", out)
	}
}

func TestRequestID_Format(t *testing.T) {
	id := NewRequestID()
	if len(id) != 32 {
		t.Errorf("request id len = %d, want 32 (%q)", len(id), id)
	}
	if id == NewRequestID() {
		t.Error("two request IDs collided")
	}
}

func TestRequestID_ContextRoundTrip(t *testing.T) {
	ctx := WithRequestID(context.Background(), "abc123")
	if got := RequestIDFromContext(ctx); got != "abc123" {
		t.Errorf("RequestIDFromContext = %q, want abc123", got)
	}
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("empty context returned %q, want empty", got)
	}
}

func TestAccessLogger_EmitsJSONLine(t *testing.T) {
	var buf bytes.Buffer
	l := NewAccessLogger(&buf)
	l.Log(&AccessEvent{
		Method:     "GET",
		Path:       "/c/demo/report.pdf",
		Status:     200,
		Bytes:      1024,
		DurationMS: 1.5,
		RequestID:  "rid",
		Collection: "demo",
		Auth:       "anonymous",
	})

	line := strings.TrimSpace(buf.String())
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("expected single line, got %q", line)
	}
	var ev AccessEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, line)
	}
	if ev.Method != "GET" || ev.Status != 200 || ev.Collection != "demo" {
		t.Errorf("round-tripped event wrong: %+v", ev)
	}
	if ev.Time.IsZero() {
		t.Error("Time was not defaulted")
	}
}

func TestAccessLogger_PreservesExplicitTime(t *testing.T) {
	var buf bytes.Buffer
	l := NewAccessLogger(&buf)
	ts := time.Date(2026, 6, 23, 1, 2, 3, 0, time.UTC)
	l.Log(&AccessEvent{Time: ts, Method: "HEAD", Path: "/", Status: 204})

	var ev AccessEvent
	if err := json.Unmarshal(buf.Bytes(), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !ev.Time.Equal(ts) {
		t.Errorf("Time = %v, want %v", ev.Time, ts)
	}
}
