package session

import "context"

type identityKey struct{}

// NewContext returns ctx carrying id, set once by the gate that authenticated
// the request (adminGate) so downstream middleware/handlers don't each need
// to re-derive it (and, for the basic-auth fallback, which carries no session
// cookie at all, re-deriving it independently per call could disagree).
func NewContext(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// FromContext returns the identity stashed by NewContext, or nil if none.
func FromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(identityKey{}).(*Identity)
	return id
}
