package serve

import (
	"context"
	"testing"
	"time"
)

type fakeLastAccessStore struct {
	touches    []time.Time
	recomputes int
	touchErr   error

	collTouches     []int64
	collRecomputeVi []string
	shareTouches    []int64
	shareRecomputes []int64
}

func (f *fakeLastAccessStore) TouchCollectionLastAccess(_ context.Context, id int64, _ time.Time) error {
	f.collTouches = append(f.collTouches, id)
	return nil
}

func (f *fakeLastAccessStore) RecomputeCollectionDenormalized(_ context.Context, _ int64, defaultVisibility string) error {
	f.collRecomputeVi = append(f.collRecomputeVi, defaultVisibility)
	return nil
}

func (f *fakeLastAccessStore) TouchShareLastAccess(_ context.Context, id int64, _ time.Time) error {
	f.shareTouches = append(f.shareTouches, id)
	return nil
}

func (f *fakeLastAccessStore) RecomputeShareDenormalized(_ context.Context, id int64) error {
	f.shareRecomputes = append(f.shareRecomputes, id)
	return nil
}

func (f *fakeLastAccessStore) TouchLastAccess(_ context.Context, _ int64, at time.Time) error {
	if f.touchErr != nil {
		return f.touchErr
	}
	f.touches = append(f.touches, at)
	return nil
}

func (f *fakeLastAccessStore) RecomputeNodeDenormalized(_ context.Context, _ int64) error {
	f.recomputes++
	return nil
}

func TestLastAccessUpdater_CoalescesWithinWindow(t *testing.T) {
	store := &fakeLastAccessStore{}
	clock := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	u := newLastAccessUpdater(store, time.Minute, "private", nil).withClock(func() time.Time { return clock })

	u.touch(context.Background(), 1)
	clock = clock.Add(30 * time.Second) // within window
	u.touch(context.Background(), 1)

	if len(store.touches) != 1 {
		t.Fatalf("touches = %d, want 1 (second call coalesced)", len(store.touches))
	}
	if store.recomputes != 1 {
		t.Fatalf("recomputes = %d, want 1", store.recomputes)
	}
}

func TestLastAccessUpdater_PersistsPastWindow(t *testing.T) {
	store := &fakeLastAccessStore{}
	clock := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	u := newLastAccessUpdater(store, time.Minute, "private", nil).withClock(func() time.Time { return clock })

	u.touch(context.Background(), 1)
	clock = clock.Add(2 * time.Minute) // past window
	u.touch(context.Background(), 1)

	if len(store.touches) != 2 {
		t.Fatalf("touches = %d, want 2", len(store.touches))
	}
}

func TestLastAccessUpdater_PerNodeIndependent(t *testing.T) {
	store := &fakeLastAccessStore{}
	clock := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	u := newLastAccessUpdater(store, time.Minute, "private", nil).withClock(func() time.Time { return clock })

	u.touch(context.Background(), 1)
	u.touch(context.Background(), 2) // different node, not coalesced with node 1

	if len(store.touches) != 2 {
		t.Fatalf("touches = %d, want 2 (independent nodes)", len(store.touches))
	}
}

func TestLastAccessUpdater_ZeroWindowAlwaysPersists(t *testing.T) {
	store := &fakeLastAccessStore{}
	clock := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	u := newLastAccessUpdater(store, 0, "private", nil).withClock(func() time.Time { return clock })

	u.touch(context.Background(), 1)
	u.touch(context.Background(), 1) // same instant, but window disabled

	if len(store.touches) != 2 {
		t.Fatalf("touches = %d, want 2 (window=0 disables coalescing)", len(store.touches))
	}
}

func TestLastAccessUpdater_TouchErrorSkipsRecompute(t *testing.T) {
	store := &fakeLastAccessStore{touchErr: context.DeadlineExceeded}
	u := newLastAccessUpdater(store, time.Minute, "private", nil)

	u.touch(context.Background(), 1)

	if store.recomputes != 0 {
		t.Fatalf("recomputes = %d, want 0 after a failed touch", store.recomputes)
	}
}

// Collections and shares are throttled independently of nodes: the same
// numeric id across kinds must not coalesce (artifactsd-6b4).
func TestLastAccessUpdater_KindsIndependent(t *testing.T) {
	store := &fakeLastAccessStore{}
	clock := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	u := newLastAccessUpdater(store, time.Minute, "unlisted", nil).withClock(func() time.Time { return clock })

	u.touch(context.Background(), 7)
	u.touchCollection(context.Background(), 7)
	u.touchShare(context.Background(), 7)
	// Repeats within the window coalesce per kind.
	u.touchCollection(context.Background(), 7)
	u.touchShare(context.Background(), 7)

	if len(store.touches) != 1 || len(store.collTouches) != 1 || len(store.shareTouches) != 1 {
		t.Fatalf("touches node=%d coll=%d share=%d, want 1 each",
			len(store.touches), len(store.collTouches), len(store.shareTouches))
	}
	if len(store.collRecomputeVi) != 1 || store.collRecomputeVi[0] != "unlisted" {
		t.Fatalf("collection recompute visibility = %v, want [unlisted]", store.collRecomputeVi)
	}
	if len(store.shareRecomputes) != 1 || store.shareRecomputes[0] != 7 {
		t.Fatalf("share recomputes = %v, want [7]", store.shareRecomputes)
	}
}

// TestLastAccessUpdater_PrunesStaleEntries: entries that no longer throttle
// anything are dropped, so the map does not grow with every object ever served
// (artifactsd-m9e.17).
func TestLastAccessUpdater_PrunesStaleEntries(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	u := newLastAccessUpdater(&fakeLastAccessStore{}, time.Minute, "private", nil).withClock(func() time.Time { return clock })
	for i := range 1000 {
		u.touch(context.Background(), int64(i))
	}
	if n := len(u.seen); n != 1000 {
		t.Fatalf("seen = %d after 1000 touches", n)
	}
	clock = clock.Add(11 * time.Minute)
	u.touch(context.Background(), 5000)
	if n := len(u.seen); n != 1 {
		t.Errorf("seen = %d after the prune interval, want 1", n)
	}
}
