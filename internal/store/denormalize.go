package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
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
		ExpiresAt           *time.Time `json:"expires_at"`
		DeleteAfterCreation string     `json:"delete_after_creation"`
	} `json:"expiry"`
}

// DeriveDenormalized computes the denormalized visibility and effective
// absolute expiry from an object's own metadata document and creation time.
//
//   - visibility is the metadata's access.visibility, or "" when unset (meaning
//     "inherit" for nodes; callers may substitute a default for collections).
//   - expiresAt is the earliest of an explicit expiry.expires_at and a
//     creation-relative expiry.delete_after_creation, or nil when neither is set.
//
// (delete_after_last_access yields a moving target and is handled by the
// janitor at access time, not baked into this absolute column — design/08.)
func DeriveDenormalized(meta json.RawMessage, createdAt time.Time) (visibility string, expiresAt *time.Time, err error) {
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
		d, perr := parseExtendedDuration(dac)
		if perr != nil {
			return "", nil, fmt.Errorf("store: expiry.delete_after_creation: %w", perr)
		}
		candidate := createdAt.Add(d).UTC()
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
	vis, exp, err := DeriveDenormalized(c.Metadata, c.CreatedAt)
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
	vis, exp, err := DeriveDenormalized(n.Metadata, n.CreatedAt)
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

// dayWeekToken matches a "<number>d" or "<number>w" component.
var dayWeekToken = regexp.MustCompile(`(\d+(?:\.\d+)?)([dw])`)

// parseExtendedDuration parses Go durations extended with 'd' (days) and 'w'
// (weeks), e.g. "7d", "30d", "2w", "1d12h". Bare time.ParseDuration units
// (h/m/s/ms/us/ns) also work. Day/week tokens are expanded into hours and the
// result handed to time.ParseDuration.
func parseExtendedDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	expanded := dayWeekToken.ReplaceAllStringFunc(s, func(tok string) string {
		m := dayWeekToken.FindStringSubmatch(tok)
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return tok // leave it for ParseDuration to reject
		}
		hours := v * 24
		if m[2] == "w" {
			hours *= 7
		}
		return strconv.FormatFloat(hours, 'f', -1, 64) + "h"
	})
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}
