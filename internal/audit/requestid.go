package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// requestIDKey is the unexported context key for the per-request ID.
type requestIDKey struct{}

// NewRequestID returns a random 128-bit request ID as a 32-char hex string.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is fatal-grade; return a sentinel rather than panic
		// so logging never takes down a request.
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// WithRequestID returns a context carrying the given request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the request ID stored in ctx, or "" if none.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}
