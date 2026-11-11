package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/durations"
)

// Effective expiry for display (artifactsd-3wy). Each object stores only its
// own expires_at (design/03 §4: the janitor deletes a container together with
// everything below it), so the instant an object actually disappears is the
// earliest of its own, every ancestor folder's, and its collection's
// expires_at. The serve path resolves the same minimum per request
// (internal/serve resolveChain); these helpers compute it for admin listings
// without a query per row.

// Expiry source kinds.
const (
	ExpiryOwn        = "own"
	ExpiryFolder     = "folder"
	ExpiryCollection = "collection"
)

// ExpirySource says where an effective expiry comes from.
type ExpirySource struct {
	Kind string // ExpiryOwn, ExpiryFolder, or ExpiryCollection
	Path string // the folder's path, for ExpiryFolder
	// Rule names the directive that produced the instant: "fixed date",
	// "<dur> after creation", "<dur> after last access", or
	// "server default: <dur> after creation". Empty when unknown.
	Rule string
}

// EffectiveExpiry is the instant an object will be deleted (nil = never) and
// the object whose rule decides it.
type EffectiveExpiry struct {
	At     *time.Time
	Source ExpirySource
}

// earlierOf returns the earlier of a and b; a wins ties. Callers fold from the
// outermost container inwards so a tie is attributed to the container, whose
// deletion is what actually removes the object.
func earlierOf(a, b EffectiveExpiry) EffectiveExpiry {
	switch {
	case b.At == nil:
		return a
	case a.At == nil, b.At.Before(*a.At):
		return b
	default:
		return a
	}
}

// ownExpiry wraps one object's stored expires_at as a candidate, labeled with
// the rule that produced it.
func (s *Store) ownExpiry(kind, path string, at *time.Time, metadata json.RawMessage, created time.Time, lastAccess *time.Time) EffectiveExpiry {
	if at == nil {
		return EffectiveExpiry{}
	}
	return EffectiveExpiry{At: at, Source: ExpirySource{
		Kind: kind, Path: path, Rule: s.expiryRule(metadata, created, lastAccess, *at),
	}}
}

// expiryRule names which of an object's directives yields at, mirroring
// DeriveDenormalized and applyDefaultExpiry.
func (s *Store) expiryRule(metadata json.RawMessage, created time.Time, lastAccess *time.Time, at time.Time) string {
	var doc metaForDenormalize
	if len(metadata) > 0 {
		_ = json.Unmarshal(metadata, &doc)
	}
	if e := doc.Expiry.ExpiresAt; e != nil && e.Equal(at) {
		return "fixed date"
	}
	if dac := strings.TrimSpace(doc.Expiry.DeleteAfterCreation); dac != "" {
		if d, err := durations.Parse(dac); err == nil && created.Add(d).Equal(at) {
			return dac + " after creation"
		}
	}
	if dala := strings.TrimSpace(doc.Expiry.DeleteAfterLastAccess); dala != "" {
		if d, err := durations.Parse(dala); err == nil {
			base := created
			if lastAccess != nil {
				base = *lastAccess
			}
			if base.Add(d).Equal(at) {
				return dala + " after last access"
			}
		}
	}
	if s.defaultDeleteAfterCreation > 0 && created.Add(s.defaultDeleteAfterCreation).Equal(at) {
		return "server default: " + formatDuration(s.defaultDeleteAfterCreation) + " after creation"
	}
	return ""
}

// CollectionExpiry is a collection's own expiry as an effective-expiry
// candidate.
func (s *Store) CollectionExpiry(c *Collection) EffectiveExpiry {
	return s.ownExpiry(ExpiryCollection, "", c.ExpiresAt, c.Metadata, c.CreatedAt, c.LastAccessAt)
}

// DirExpiry returns the effective expiry inherited by everything directly in
// dir (a folder path, "" = collection root): the earliest of the collection's
// and every folder's from the root down to dir itself. It costs one query for
// the folders, however deep dir is.
func (s *Store) DirExpiry(ctx context.Context, c *Collection, dir string) (EffectiveExpiry, error) {
	eff := s.CollectionExpiry(c)
	if dir == "" {
		return eff, nil
	}
	segs := strings.Split(dir, "/")
	prefixes := make([]string, len(segs))
	for i := range segs {
		prefixes[i] = strings.Join(segs[:i+1], "/")
	}
	folders, err := s.getNodesByPaths(ctx, c.ID, prefixes)
	if err != nil {
		return EffectiveExpiry{}, err
	}
	for _, p := range prefixes { // outermost first, so ties go to the container
		if n, ok := folders[p]; ok {
			eff = earlierOf(eff, s.ownExpiry(ExpiryFolder, n.Path, n.ExpiresAt, n.Metadata, n.CreatedAt, n.LastAccessAt))
		}
	}
	return eff, nil
}

// ChildExpiry combines the expiry inherited from the containing directory
// (see DirExpiry) with a child node's own.
func (s *Store) ChildExpiry(inherited EffectiveExpiry, n *Node) EffectiveExpiry {
	return earlierOf(inherited, s.ownExpiry(ExpiryOwn, n.Path, n.ExpiresAt, n.Metadata, n.CreatedAt, n.LastAccessAt))
}

// formatDuration renders d the way operators write it: whole days as "30d",
// anything else in Go's notation.
func formatDuration(d time.Duration) string {
	const day = 24 * time.Hour
	if d%day == 0 {
		return strconv.FormatInt(int64(d/day), 10) + "d"
	}
	return d.String()
}

const nodesByPathsQuery = `SELECT ` + nodeCols + ` FROM nodes
	WHERE collection_id = ? AND path IN (SELECT value FROM json_each(?))`

// getNodesByPaths loads the nodes at the given paths in one collection, keyed
// by path; missing paths are simply absent.
func (s *Store) getNodesByPaths(ctx context.Context, collectionID int64, ps []string) (map[string]*Node, error) {
	out := make(map[string]*Node, len(ps))
	if len(ps) == 0 {
		return out, nil
	}
	// The paths travel as one JSON-array parameter, so the SQL text is
	// constant whatever their number.
	list, err := json.Marshal(ps)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.sql.QueryContext(ctx, nodesByPathsQuery, collectionID, string(list))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out[n.Path] = n
	}
	return out, rows.Err()
}
