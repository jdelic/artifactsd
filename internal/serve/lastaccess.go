package serve

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// accessKind distinguishes the object types whose last_access_at is tracked.
type accessKind uint8

const (
	accessNode accessKind = iota
	accessCollection
	accessShare
)

func (k accessKind) String() string {
	switch k {
	case accessCollection:
		return "collection"
	case accessShare:
		return "share"
	default:
		return "node"
	}
}

// accessKey identifies one tracked object.
type accessKey struct {
	kind accessKind
	id   int64
}

// lastAccessUpdater records last_access_at on serve for nodes, collections,
// and shares, coalesced so a given object is persisted at most once per
// window — the rest of the time a touch is just an in-memory map check
// (design/08 §3). A window of zero disables coalescing: every touch is
// persisted immediately.
type lastAccessUpdater struct {
	store             lastAccessStore
	window            time.Duration
	defaultVisibility string
	now               func() time.Time
	log               *slog.Logger

	mu   sync.Mutex
	seen map[accessKey]time.Time
}

// lastAccessStore is the slice of *store.Store the updater needs, kept narrow
// so it's trivially fakeable in tests.
type lastAccessStore interface {
	TouchLastAccess(ctx context.Context, id int64, at time.Time) error
	RecomputeNodeDenormalized(ctx context.Context, id int64) error
	TouchCollectionLastAccess(ctx context.Context, id int64, at time.Time) error
	RecomputeCollectionDenormalized(ctx context.Context, id int64, defaultVisibility string) error
	TouchShareLastAccess(ctx context.Context, id int64, at time.Time) error
	RecomputeShareDenormalized(ctx context.Context, id int64) error
}

func newLastAccessUpdater(s lastAccessStore, window time.Duration, defaultVisibility string, log *slog.Logger) *lastAccessUpdater {
	return &lastAccessUpdater{
		store:             s,
		window:            window,
		defaultVisibility: defaultVisibility,
		now:               func() time.Time { return time.Now().UTC() },
		log:               log,
		seen:              make(map[accessKey]time.Time),
	}
}

// withClock overrides the clock (for tests).
func (u *lastAccessUpdater) withClock(now func() time.Time) *lastAccessUpdater {
	u.now = now
	return u
}

// touch records nodeID's access (see touchKey).
func (u *lastAccessUpdater) touch(ctx context.Context, nodeID int64) {
	u.touchKey(ctx, accessKey{accessNode, nodeID})
}

// touchCollection records collectionID's access (see touchKey).
func (u *lastAccessUpdater) touchCollection(ctx context.Context, collectionID int64) {
	u.touchKey(ctx, accessKey{accessCollection, collectionID})
}

// touchShare records the share's access by its numeric row id (see touchKey).
func (u *lastAccessUpdater) touchShare(ctx context.Context, shareRowID int64) {
	u.touchKey(ctx, accessKey{accessShare, shareRowID})
}

// touchKey persists last_access_at for k and re-derives its denormalized
// expires_at (so delete_after_last_access tracks the new last_access_at,
// design/03 §4) — unless a touch already landed for k within the window, in
// which case it's a no-op.
func (u *lastAccessUpdater) touchKey(ctx context.Context, k accessKey) {
	now := u.now()
	if u.window > 0 {
		u.mu.Lock()
		if last, ok := u.seen[k]; ok && now.Sub(last) < u.window {
			u.mu.Unlock()
			return
		}
		u.seen[k] = now
		u.mu.Unlock()
	}

	var touchErr, recomputeErr error
	switch k.kind {
	case accessCollection:
		if touchErr = u.store.TouchCollectionLastAccess(ctx, k.id, now); touchErr == nil {
			recomputeErr = u.store.RecomputeCollectionDenormalized(ctx, k.id, u.defaultVisibility)
		}
	case accessShare:
		if touchErr = u.store.TouchShareLastAccess(ctx, k.id, now); touchErr == nil {
			recomputeErr = u.store.RecomputeShareDenormalized(ctx, k.id)
		}
	default:
		if touchErr = u.store.TouchLastAccess(ctx, k.id, now); touchErr == nil {
			recomputeErr = u.store.RecomputeNodeDenormalized(ctx, k.id)
		}
	}
	if touchErr != nil {
		u.warn("touch "+k.kind.String()+" last_access_at", touchErr)
	}
	if recomputeErr != nil {
		u.warn("recompute "+k.kind.String()+" denormalized after touch", recomputeErr)
	}
}

func (u *lastAccessUpdater) warn(what string, err error) {
	if u.log != nil {
		u.log.Warn("serve: "+what, "error", err)
	}
}
