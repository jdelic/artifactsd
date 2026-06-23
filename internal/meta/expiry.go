package meta

import (
	"time"

	"github.com/jdelic/artifactsd/internal/durations"
)

// ResolveOwnExpiry computes the earliest absolute expiry implied by an object's
// OWN expiry directives (design/03 §4), or nil if none apply:
//
//   - expires_at: the absolute instant.
//   - delete_after_creation: createdAt + duration.
//   - delete_after_last_access: lastAccess (or createdAt if never accessed) +
//     duration — a moving target recomputed as last access advances.
//
// The earliest of the present directives wins.
func ResolveOwnExpiry(d *Document, createdAt, lastAccess time.Time) (*time.Time, error) {
	if d == nil || d.Expiry == nil {
		return nil, nil
	}
	var earliest *time.Time
	consider := func(t time.Time) {
		t = t.UTC()
		if earliest == nil || t.Before(*earliest) {
			tt := t
			earliest = &tt
		}
	}

	e := d.Expiry
	if e.ExpiresAt != nil {
		t, err := ParseInstant(*e.ExpiresAt)
		if err != nil {
			return nil, err
		}
		consider(t)
	}
	if e.DeleteAfterCreation != nil && *e.DeleteAfterCreation != "" {
		dur, err := durations.Parse(*e.DeleteAfterCreation)
		if err != nil {
			return nil, err
		}
		consider(createdAt.Add(dur))
	}
	if e.DeleteAfterLastAccess != nil && *e.DeleteAfterLastAccess != "" {
		dur, err := durations.Parse(*e.DeleteAfterLastAccess)
		if err != nil {
			return nil, err
		}
		base := lastAccess
		if base.IsZero() {
			base = createdAt
		}
		consider(base.Add(dur))
	}
	return earliest, nil
}

// EffectiveExpiry computes an object's effective absolute expiry as the earlier
// of its own resolved expiry and its parent's already-resolved effective expiry
// (design/03 §4). Applied node-by-node from the root down, this yields the
// "earliest-in-tree always wins" rule transitively: a child can never outlive
// its container.
func EffectiveExpiry(d *Document, createdAt, lastAccess time.Time, parentEffective *time.Time) (*time.Time, error) {
	own, err := ResolveOwnExpiry(d, createdAt, lastAccess)
	if err != nil {
		return nil, err
	}
	return Earliest(own, parentEffective), nil
}

// Earliest returns the earlier of two nullable instants (nil = no bound).
func Earliest(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.Before(*b):
		return a
	default:
		return b
	}
}
