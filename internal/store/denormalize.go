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
// delete_after_last_access makes expiresAt a moving target: collections,
// nodes, and shares all track last_access_at (via the throttled touch in
// internal/serve) and re-derive this column each time it advances (design/08
// §3). Falling back to createdAt is only correct because changing the idle
// rule also resets last_access_at to now (idleRuleChanged*SQL): a zero
// lastAccess therefore means the rule has been in place since creation.
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
	var lastAccess time.Time
	if c.LastAccessAt != nil {
		lastAccess = *c.LastAccessAt
	}
	vis, exp, err := DeriveDenormalized(c.Metadata, c.CreatedAt, lastAccess)
	if err != nil {
		return err
	}
	if vis == "" {
		vis = defaultVisibility
	}
	exp = s.applyDefaultExpiry(exp, c.CreatedAt)
	_, err = s.db.sql.ExecContext(ctx,
		`UPDATE collections SET visibility = ?, expires_at = ? WHERE id = ?`,
		vis, fmtTimePtr(exp), id)
	return err
}

// RecomputeNodeDenormalized re-derives and persists the denormalized columns
// for one node. A node with no own visibility keeps NULL (inherit).
func (s *Store) RecomputeNodeDenormalized(ctx context.Context, id int64) error {
	return s.recomputeNodeDenormalized(ctx, s.db.sql, id)
}

// recomputeNodeDenormalized is RecomputeNodeDenormalized on q.
func (s *Store) recomputeNodeDenormalized(ctx context.Context, q querier, id int64) error {
	n, err := getNodeByID(ctx, q, id)
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
	exp = s.applyDefaultExpiry(exp, n.CreatedAt)
	var visArg any
	if vis != "" {
		visArg = vis
	}
	_, err = q.ExecContext(ctx,
		`UPDATE nodes SET visibility = ?, expires_at = ? WHERE id = ?`,
		visArg, fmtTimePtr(exp), id)
	return err
}

// RecomputeAllDenormalized recomputes denormalized columns for every
// collection, node, and share. Used after a migration that changes derivation rules (design/02 §3).
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

		shares, err := s.ListSharesByCollection(ctx, c.ID)
		if err != nil {
			return count, err
		}
		for _, sh := range shares {
			if err := s.persistShareExpiry(ctx, sh); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

// The idleRuleChanged* expressions are SQL booleans that are true when a
// metadata write changes expiry.delete_after_last_access (including adding or
// removing it). Metadata writes use them to reset last_access_at to now, so a
// newly set or changed idle rule counts from the moment it was set rather than
// from creation or a long-past access — otherwise enabling "30d" on an old
// object makes it due at once (artifactsd-6b4). Each compares the row's stored
// metadata with the incoming value; ?1 is the new metadata parameter.
const (
	idleRulePath = `'$.expiry.delete_after_last_access'`

	idleRuleChangedCollectionSQL = `json_extract(collections.metadata, ` + idleRulePath + `) IS NOT json_extract(?1, ` + idleRulePath + `)`
	idleRuleChangedShareSQL      = `json_extract(shares.metadata, ` + idleRulePath + `) IS NOT json_extract(?1, ` + idleRulePath + `)`
	idleRuleChangedNodeSQL       = `json_extract(nodes.metadata, ` + idleRulePath + `) IS NOT json_extract(excluded.metadata, ` + idleRulePath + `)`
)

// flagRecomputeDenormalized is the maintenance_flags row a migration inserts
// when the derivation inputs changed and every denormalized column must be
// recomputed in Go (migration 0004).
const flagRecomputeDenormalized = "recompute_denormalized"

// RunPendingMaintenance performs deferred post-migration work recorded in
// maintenance_flags, clearing each flag once done. The server calls it at
// startup, before the janitor starts, so the janitor never acts on stale
// expires_at values. Returns the number of rows recomputed (0 when nothing was
// pending).
func (s *Store) RunPendingMaintenance(ctx context.Context, defaultVisibility string) (int, error) {
	var pending int
	if err := s.db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM maintenance_flags WHERE name = ?`, flagRecomputeDenormalized,
	).Scan(&pending); err != nil {
		return 0, fmt.Errorf("store: reading maintenance flags: %w", err)
	}
	if pending == 0 {
		return 0, nil
	}
	n, err := s.RecomputeAllDenormalized(ctx, defaultVisibility)
	if err != nil {
		return n, fmt.Errorf("store: recomputing denormalized columns: %w", err)
	}
	if _, err := s.db.sql.ExecContext(ctx,
		`DELETE FROM maintenance_flags WHERE name = ?`, flagRecomputeDenormalized); err != nil {
		return n, err
	}
	return n, nil
}
