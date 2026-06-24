package serve

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// lastAccessUpdater records a node's last_access_at on serve, coalesced so a
// given node is persisted at most once per window — the rest of the time a
// touch is just an in-memory map check (design/08 §3). A window of zero
// disables coalescing: every touch is persisted immediately.
type lastAccessUpdater struct {
	store  lastAccessStore
	window time.Duration
	now    func() time.Time
	log    *slog.Logger

	mu   sync.Mutex
	seen map[int64]time.Time
}

// lastAccessStore is the slice of *store.Store the updater needs, kept narrow
// so it's trivially fakeable in tests.
type lastAccessStore interface {
	TouchLastAccess(ctx context.Context, id int64, at time.Time) error
	RecomputeNodeDenormalized(ctx context.Context, id int64) error
}

func newLastAccessUpdater(s lastAccessStore, window time.Duration, log *slog.Logger) *lastAccessUpdater {
	return &lastAccessUpdater{
		store:  s,
		window: window,
		now:    func() time.Time { return time.Now().UTC() },
		log:    log,
		seen:   make(map[int64]time.Time),
	}
}

// withClock overrides the clock (for tests).
func (u *lastAccessUpdater) withClock(now func() time.Time) *lastAccessUpdater {
	u.now = now
	return u
}

// touch records nodeID's access, persisting last_access_at and re-deriving the
// node's denormalized expires_at (so delete_after_last_access tracks the new
// last_access_at, design/03 §4) — unless a touch already landed for this node
// within the window, in which case it's a no-op.
func (u *lastAccessUpdater) touch(ctx context.Context, nodeID int64) {
	now := u.now()
	if u.window > 0 {
		u.mu.Lock()
		if last, ok := u.seen[nodeID]; ok && now.Sub(last) < u.window {
			u.mu.Unlock()
			return
		}
		u.seen[nodeID] = now
		u.mu.Unlock()
	}

	if err := u.store.TouchLastAccess(ctx, nodeID, now); err != nil {
		u.warn("touch last_access_at", err)
		return
	}
	if err := u.store.RecomputeNodeDenormalized(ctx, nodeID); err != nil {
		u.warn("recompute denormalized after touch", err)
	}
}

func (u *lastAccessUpdater) warn(what string, err error) {
	if u.log != nil {
		u.log.Warn("serve: "+what, "error", err)
	}
}
