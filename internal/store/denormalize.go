package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/durations"
)

// The denormalized `visibility` and `expires_at` columns are a derived cache of
// each object's metadata, so the janitor (WHERE expires_at <= now), sitemap
// (WHERE visibility = 'public'), and listings can run as simple indexed queries
// without re-resolving JSON per row (design/02 §3).
//
// This file derives those columns from an object's OWN metadata document. Full
// inheritance-aware resolution (server defaults → collection → folders → file →
// share) lives in the meta engine (design/03 §2); when it lands it supplies the
// effective values and these helpers persist them. Until then the denormalized
// columns reflect each object's directly-set directives, which is what the
// janitor needs for per-object expiry.

// metaForDenormalize is the minimal slice of meta.Document needed to derive the
// denormalized columns.
type metaForDenormalize struct {
	Access struct {
		Visibility string `json:"visibility"`
	} `json:"access"`
	Expiry struct {
		ExpiresAt             *time.Time `json:"expires_at"`
		DeleteAfterCreation   string     `json:"delete_after_creation"`
		DeleteAfterLastAccess string     `json:"delete_after_last_access"`
	} `json:"expiry"`
}

// DeriveDenormalized computes the denormalized visibility and effective
// absolute expiry from an object's own metadata document, creation time, and
// last-access time (design/03 §4).
//
//   - visibility is the metadata's access.visibility, or "" when unset (meaning
//     "inherit" for nodes; callers may substitute a default for collections).
//   - expiresAt is the earliest of an explicit expiry.expires_at, a
//     creation-relative expiry.delete_after_creation, and an access-relative
//     expiry.delete_after_last_access (relative to lastAccess, or createdAt if
//     lastAccess is zero — never yet accessed), or nil when none apply.
//
// delete_after_last_access makes expiresAt a moving target: callers that track
// access (currently nodes, via the throttled touch in internal/serve) must
// re-derive and persist this column each time lastAccess advances (design/08
// §3); callers with no access signal pass a zero lastAccess, which is
// equivalent to never having been accessed.
func DeriveDenormalized(meta json.RawMessage, createdAt, lastAccess time.Time) (visibility string, expiresAt *time.Time, err error) {
	if len(meta) == 0 {
		return "", nil, nil
	}
	var doc metaForDenormalize
	if err := json.Unmarshal(meta, &doc); err != nil {
		return "", nil, fmt.Errorf("store: parsing metadata for denormalization: %w", err)
	}

	visibility = doc.Access.Visibility

	if doc.Expiry.ExpiresAt != nil {
		t := doc.Expiry.ExpiresAt.UTC()
		expiresAt = &t
	}
	if dac := strings.TrimSpace(doc.Expiry.DeleteAfterCreation); dac != "" {
		d, perr := durations.Parse(dac)
		if perr != nil {
			return "", nil, fmt.Errorf("store: expiry.delete_after_creation: %w", perr)
		}
		candidate := createdAt.Add(d).UTC()
		if expiresAt == nil || candidate.Before(*expiresAt) {
			expiresAt = &candidate
		}
	}
	if dala := strings.TrimSpace(doc.Expiry.DeleteAfterLastAccess); dala != "" {
		d, perr := durations.Parse(dala)
		if perr != nil {
			return "", nil, fmt.Errorf("store: expiry.delete_after_last_access: %w", perr)
		}
		base := lastAccess
		if base.IsZero() {
			base = createdAt
		}
		candidate := base.Add(d).UTC()
		if expiresAt == nil || candidate.Before(*expiresAt) {
			expiresAt = &candidate
		}
	}
	return visibility, expiresAt, nil
}

// RecomputeCollectionDenormalized re-derives and persists the denormalized
// columns for one collection. defaultVisibility is used when the collection's
// metadata does not set one.
func (s *Store) RecomputeCollectionDenormalized(ctx context.Context, id int64, defaultVisibility string) error {
	c, err := s.GetCollection(ctx, id)
	if err != nil {
		return err
	}
	vis, exp, err := DeriveDenormalized(c.Metadata, c.CreatedAt, time.Time{})
	if err != nil {
		return err
	}
	if vis == "" {
		vis = defaultVisibility
	}
	_, err = s.db.sql.ExecContext(ctx,
		`UPDATE collections SET visibility = ?, expires_at = ? WHERE id = ?`,
		vis, fmtTimePtr(exp), id)
	return err
}

// RecomputeNodeDenormalized re-derives and persists the denormalized columns
// for one node. A node with no own visibility keeps NULL (inherit).
func (s *Store) RecomputeNodeDenormalized(ctx context.Context, id int64) error {
	n, err := s.GetNodeByID(ctx, id)
	if err != nil {
		return err
	}
	var lastAccess time.Time
	if n.LastAccessAt != nil {
		lastAccess = *n.LastAccessAt
	}
	vis, exp, err := DeriveDenormalized(n.Metadata, n.CreatedAt, lastAccess)
	if err != nil {
		return err
	}
	var visArg any
	if vis != "" {
		visArg = vis
	}
	_, err = s.db.sql.ExecContext(ctx,
		`UPDATE nodes SET visibility = ?, expires_at = ? WHERE id = ?`,
		visArg, fmtTimePtr(exp), id)
	return err
}

// RecomputeAllDenormalized recomputes denormalized columns for every collection
// and node. Used after a migration that changes derivation rules (design/02 §3).
// Returns the number of rows recomputed.
func (s *Store) RecomputeAllDenormalized(ctx context.Context, defaultVisibility string) (int, error) {
	cols, err := s.ListCollections(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, c := range cols {
		if err := s.RecomputeCollectionDenormalized(ctx, c.ID, defaultVisibility); err != nil {
			return count, err
		}
		count++

		nodes, err := s.ListCollectionNodes(ctx, c.ID)
		if err != nil {
			return count, err
		}
		for _, n := range nodes {
			if err := s.RecomputeNodeDenormalized(ctx, n.ID); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}
