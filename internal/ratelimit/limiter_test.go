package ratelimit

import (
	"strconv"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(maxFailures int, window time.Duration) (*Limiter, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC)}
	l := New(maxFailures, window).WithClock(clk.now)
	return l, clk
}

func TestLimiter_AllowsUpToMaxThenBlocks(t *testing.T) {
	l, _ := newTestLimiter(3, time.Minute)
	const key = "1.2.3.4"

	for i := 0; i < 3; i++ {
		if blocked, _ := l.RetryAfter(key); blocked {
			t.Fatalf("blocked early after %d failures", i)
		}
		l.RecordFailure(key)
	}
	blocked, retry := l.RetryAfter(key)
	if !blocked {
		t.Fatal("expected block after 3 failures")
	}
	if retry <= 0 {
		t.Errorf("retry-after = %v, want > 0", retry)
	}
}

func TestLimiter_RefillsOverTime(t *testing.T) {
	l, clk := newTestLimiter(2, time.Minute)
	const key = "k"

	l.RecordFailure(key)
	l.RecordFailure(key)
	if blocked, _ := l.RetryAfter(key); !blocked {
		t.Fatal("expected block after exhausting bucket")
	}

	// After a full window, the bucket refills to capacity.
	clk.advance(time.Minute)
	if blocked, _ := l.RetryAfter(key); blocked {
		t.Error("still blocked after a full window of refill")
	}
}

func TestLimiter_RetryAfterShrinksAsItRefills(t *testing.T) {
	l, clk := newTestLimiter(10, time.Minute) // refill 10/min => 1 token / 6s
	const key = "k"
	for i := 0; i < 10; i++ {
		l.RecordFailure(key)
	}
	blocked, r1 := l.RetryAfter(key)
	if !blocked {
		t.Fatal("expected block")
	}
	clk.advance(3 * time.Second)
	_, r2 := l.RetryAfter(key)
	if r2 > r1 {
		t.Errorf("retry-after grew over time: %v -> %v", r1, r2)
	}
}

func TestLimiter_PerKeyIsolation(t *testing.T) {
	l, _ := newTestLimiter(1, time.Minute)
	l.RecordFailure("a")
	if blocked, _ := l.RetryAfter("a"); !blocked {
		t.Error("key a should be blocked")
	}
	if blocked, _ := l.RetryAfter("b"); blocked {
		t.Error("key b must be unaffected by key a")
	}
}

func TestLimiter_GCDropsIdleKeys(t *testing.T) {
	l, clk := newTestLimiter(2, time.Minute)
	l.RecordFailure("transient")
	if l.Len() == 0 {
		t.Fatal("key not tracked")
	}
	// Advance well past the GC interval; a RecordFailure on another key triggers
	// GC, which drops the now-fully-refilled idle key.
	clk.advance(11 * time.Minute)
	l.RecordFailure("other")
	if _, ok := l.buckets["transient"]; ok {
		t.Error("idle key was not garbage-collected")
	}
}

func TestLimiter_Defaults(t *testing.T) {
	// Degenerate args are clamped rather than panicking (div-by-zero etc.).
	l := New(0, 0)
	l.RecordFailure("k")
	if blocked, _ := l.RetryAfter("k"); !blocked {
		t.Error("max<1 should clamp to 1 and block after one failure")
	}
}

func TestLimiter_RetryAfterDoesNotTrackUnknownKeys(t *testing.T) {
	l, _ := newTestLimiter(2, time.Minute)
	for i := range 100 {
		if blocked, _ := l.RetryAfter("k" + strconv.Itoa(i)); blocked {
			t.Fatal("unknown key reported blocked")
		}
	}
	if n := l.Len(); n != 0 {
		t.Errorf("Len after RetryAfter on unseen keys = %d, want 0", n)
	}
}

func TestLimiter_GCRunsFromRetryAfter(t *testing.T) {
	l, clk := newTestLimiter(2, time.Minute)
	l.RecordFailure("a")
	l.RecordFailure("b")
	clk.advance(11 * time.Minute)
	// No new failure: a RetryAfter alone must collect the refilled buckets.
	l.RetryAfter("c")
	if n := l.Len(); n != 0 {
		t.Errorf("Len after GC via RetryAfter = %d, want 0", n)
	}
}

func TestLimiter_CapEvictsLeastPenalized(t *testing.T) {
	l, _ := newTestLimiter(3, time.Minute)
	l.maxKeys = 3
	// "bad" is blocked (3 failures); the others have one failure each.
	for range 3 {
		l.RecordFailure("bad")
	}
	l.RecordFailure("x")
	l.RecordFailure("y")
	l.RecordFailure("y")
	// Map is full; a new key evicts the least-penalized bucket ("x").
	l.RecordFailure("new")
	if n := l.Len(); n != 3 {
		t.Fatalf("Len = %d, want cap 3", n)
	}
	if _, ok := l.buckets["x"]; ok {
		t.Error("least-penalized key x was not evicted")
	}
	if blocked, _ := l.RetryAfter("bad"); !blocked {
		t.Error("blocked key was evicted; it must stay blocked")
	}
}
