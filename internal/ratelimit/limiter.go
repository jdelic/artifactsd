// Package ratelimit provides an in-memory token-bucket limiter for auth
// failures (design/06 §5). It throttles repeated 401/403 outcomes per key
// (per-IP and/or per-token-prefix) and answers with a Retry-After hint. A
// single-process deployment makes an in-memory limiter sufficient.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// bucket is one key's token state.
type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a concurrency-safe per-key token-bucket limiter. A key starts with
// a full bucket of `max` tokens; each recorded failure consumes one, and tokens
// refill at max/window. When a key has fewer than one token it is blocked.
type Limiter struct {
	capacity   float64
	refillRate float64 // tokens per second
	now        func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
	gcEvery time.Duration
	// maxKeys bounds the map. When a new key arrives at the cap, refilled
	// buckets are collected first, then the least-penalized bucket (most
	// tokens) is evicted, so blocked keys stay blocked.
	maxKeys int
}

// DefaultMaxKeys is the default bound on tracked keys.
const DefaultMaxKeys = 100_000

// New returns a limiter allowing up to max failures per window before blocking.
func New(maxFailures int, window time.Duration) *Limiter {
	if maxFailures < 1 {
		maxFailures = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &Limiter{
		capacity:   float64(maxFailures),
		refillRate: float64(maxFailures) / window.Seconds(),
		now:        time.Now,
		buckets:    make(map[string]*bucket),
		gcEvery:    10 * window,
		maxKeys:    DefaultMaxKeys,
	}
}

// WithClock overrides the clock (tests).
func (l *Limiter) WithClock(now func() time.Time) *Limiter {
	l.now = now
	return l
}

// refill brings b up to date at now. Caller must hold l.mu.
func (l *Limiter) refill(b *bucket, now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.capacity, b.tokens+elapsed*l.refillRate)
		b.last = now
	}
}

// getBucket returns the (refilled) bucket for key, creating a full one if new.
// Caller must hold l.mu.
func (l *Limiter) getBucket(key string, now time.Time) *bucket {
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.makeRoom(now)
		}
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
		return b
	}
	l.refill(b, now)
	return b
}

// makeRoom frees at least one slot: it collects refilled buckets and, if the
// map is still full, evicts the bucket with the most tokens (the least
// penalized). This scan is O(n) and only runs when the map is at its cap.
// Caller must hold l.mu.
func (l *Limiter) makeRoom(now time.Time) {
	l.gc(now)
	if len(l.buckets) < l.maxKeys {
		return
	}
	var victim string
	best := math.Inf(-1)
	for key, b := range l.buckets {
		l.refill(b, now)
		if b.tokens > best {
			best, victim = b.tokens, key
		}
	}
	delete(l.buckets, victim)
}

// RecordFailure consumes a token for key (driving it toward, and a bounded
// amount past, the block threshold so sustained abuse extends Retry-After).
func (l *Limiter) RecordFailure(key string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.maybeGC(now)
	b := l.getBucket(key, now)
	b.tokens--
	if b.tokens < -l.capacity {
		b.tokens = -l.capacity
	}
}

// RetryAfter reports whether key is currently blocked and, if so, how long
// until a token is available (rounded up to whole seconds, minimum 1s). An
// unknown key is not blocked and is not tracked.
func (l *Limiter) RetryAfter(key string) (blocked bool, retry time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.maybeGC(now)
	b, ok := l.buckets[key]
	if !ok {
		return false, 0
	}
	l.refill(b, now)
	if b.tokens >= 1 {
		return false, 0
	}
	seconds := (1 - b.tokens) / l.refillRate
	d := time.Duration(math.Ceil(seconds)) * time.Second
	if d < time.Second {
		d = time.Second
	}
	return true, d
}

// maybeGC drops fully-refilled (idle) buckets at most every gcEvery. Caller
// holds l.mu.
func (l *Limiter) maybeGC(now time.Time) {
	if l.lastGC.IsZero() {
		l.lastGC = now
		return
	}
	if now.Sub(l.lastGC) < l.gcEvery {
		return
	}
	l.gc(now)
}

// gc drops every fully-refilled bucket. Caller holds l.mu.
func (l *Limiter) gc(now time.Time) {
	l.lastGC = now
	for key, b := range l.buckets {
		elapsed := now.Sub(b.last).Seconds()
		refilled := math.Min(l.capacity, b.tokens+elapsed*l.refillRate)
		if refilled >= l.capacity {
			delete(l.buckets, key)
		}
	}
}

// Len returns the number of tracked keys (for tests/metrics).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
