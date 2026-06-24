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
	u := newLastAccessUpdater(store, time.Minute, nil).withClock(func() time.Time { return clock })

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
	u := newLastAccessUpdater(store, time.Minute, nil).withClock(func() time.Time { return clock })

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
	u := newLastAccessUpdater(store, time.Minute, nil).withClock(func() time.Time { return clock })

	u.touch(context.Background(), 1)
	u.touch(context.Background(), 2) // different node, not coalesced with node 1

	if len(store.touches) != 2 {
		t.Fatalf("touches = %d, want 2 (independent nodes)", len(store.touches))
	}
}

func TestLastAccessUpdater_ZeroWindowAlwaysPersists(t *testing.T) {
	store := &fakeLastAccessStore{}
	clock := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	u := newLastAccessUpdater(store, 0, nil).withClock(func() time.Time { return clock })

	u.touch(context.Background(), 1)
	u.touch(context.Background(), 1) // same instant, but window disabled

	if len(store.touches) != 2 {
		t.Fatalf("touches = %d, want 2 (window=0 disables coalescing)", len(store.touches))
	}
}

func TestLastAccessUpdater_TouchErrorSkipsRecompute(t *testing.T) {
	store := &fakeLastAccessStore{touchErr: context.DeadlineExceeded}
	u := newLastAccessUpdater(store, time.Minute, nil)

	u.touch(context.Background(), 1)

	if store.recomputes != 0 {
		t.Fatalf("recomputes = %d, want 0 after a failed touch", store.recomputes)
	}
}
