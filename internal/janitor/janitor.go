// Package janitor runs the expiry sweep and upload-session reaper as a background goroutine (design/08).
package janitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/store"
)

// defaultBatchSize matches config's default (design/12 §4) for callers that
// don't set Deps.BatchSize explicitly (e.g. ad-hoc tests).
const defaultBatchSize = 500

// Deps are the janitor's dependencies.
type Deps struct {
	Store     *store.Store
	Blob      blob.Store
	Audit     *audit.Recorder
	Logger    *slog.Logger
	BatchSize int // 0 uses defaultBatchSize
}

// Janitor performs expiry-driven deletion passes (design/08). It satisfies
// api.Cleaner (RunOnce) so it can be wired as the manual-cleanup backend.
type Janitor struct {
	store     *store.Store
	blob      blob.Store
	audit     *audit.Recorder
	log       *slog.Logger
	batchSize int
	now       func() time.Time
}

// New builds a Janitor from Deps.
func New(d Deps) *Janitor { //nolint:gocritic // Deps is built once at construction
	bs := d.BatchSize
	if bs <= 0 {
		bs = defaultBatchSize
	}
	return &Janitor{
		store: d.Store, blob: d.Blob, audit: d.Audit, log: d.Logger, batchSize: bs,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// WithClock overrides the clock (tests).
func (j *Janitor) WithClock(now func() time.Time) *Janitor {
	j.now = now
	return j
}

// Summary reports one pass's results (design/08 §8); it is also the JSON body
// returned by POST /api/v1/maintenance/cleanup.
type Summary struct {
	NodesDeleted       int      `json:"nodes_deleted"`
	CollectionsDeleted int      `json:"collections_deleted"`
	SharesDeleted      int      `json:"shares_deleted"`
	BlobsDeleted       int      `json:"blobs_deleted"`
	Errors             []string `json:"errors,omitempty"`
}

// RunOnce performs a single expiry sweep: delete expired nodes, collections,
// and shares (earliest-expiry-in-the-tree-wins is already baked into the
// denormalized expires_at, design/03 §4), unlinking blobs that become
// unreferenced, with an audit record per deletion (design/08 §4). Per-item
// failures are collected into Summary.Errors rather than aborting the pass
// (design/08 §8: failures are logged and retried next pass).
func (j *Janitor) RunOnce(ctx context.Context) (any, error) {
	now := j.now()
	sum := &Summary{}

	if err := j.expireNodes(ctx, now, sum); err != nil {
		sum.Errors = append(sum.Errors, err.Error())
	}
	if err := j.expireCollections(ctx, now, sum); err != nil {
		sum.Errors = append(sum.Errors, err.Error())
	}
	if err := j.expireShares(ctx, now, sum); err != nil {
		sum.Errors = append(sum.Errors, err.Error())
	}

	if j.log != nil {
		j.log.Info("janitor: pass complete",
			"nodes_deleted", sum.NodesDeleted, "collections_deleted", sum.CollectionsDeleted,
			"shares_deleted", sum.SharesDeleted, "blobs_deleted", sum.BlobsDeleted, "errors", len(sum.Errors))
	}
	return sum, nil
}

// Loop runs RunOnce on a ticker until ctx is canceled (design/08 §1/§7).
func (j *Janitor) Loop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := j.RunOnce(ctx); err != nil && j.log != nil {
				j.log.Error("janitor: pass failed", "error", err)
			}
		}
	}
}

func (j *Janitor) expireNodes(ctx context.Context, now time.Time, sum *Summary) error {
	nodes, err := j.store.ListExpiredNodes(ctx, now, j.batchSize)
	if err != nil {
		return fmt.Errorf("janitor: list expired nodes: %w", err)
	}
	names := map[int64]string{}
	for _, n := range nodes {
		if err := j.deleteNode(ctx, n, names, sum); err != nil {
			sum.Errors = append(sum.Errors, err.Error())
		}
	}
	return nil
}

// deleteNode removes one expired node (file or folder). For a folder this
// cascades its descendants (design/02 §1), which may include other rows this
// pass already queued — a resulting ErrNotFound means it was already removed,
// not a failure.
func (j *Janitor) deleteNode(ctx context.Context, n *store.Node, names map[int64]string, sum *Summary) error {
	refs, err := j.store.NodeBlobRefs(ctx, n.CollectionID, n.Path)
	if err != nil {
		return fmt.Errorf("janitor: collect blob refs for node %d/%s: %w", n.CollectionID, n.Path, err)
	}

	if err := j.store.DeleteNode(ctx, n.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("janitor: delete node %d/%s: %w", n.CollectionID, n.Path, err)
	}
	sum.NodesDeleted++
	j.gcBlobs(ctx, refs, sum)

	target := j.collectionName(ctx, n.CollectionID, names) + "/" + n.Path
	_ = j.audit.RecordDeletion(ctx, audit.ActorJanitor, audit.ActionExpiryDelete, target, nil)
	return nil
}

func (j *Janitor) expireCollections(ctx context.Context, now time.Time, sum *Summary) error {
	cols, err := j.store.ListExpiredCollections(ctx, now, j.batchSize)
	if err != nil {
		return fmt.Errorf("janitor: list expired collections: %w", err)
	}
	for _, c := range cols {
		if err := j.deleteCollection(ctx, c, sum); err != nil {
			sum.Errors = append(sum.Errors, err.Error())
		}
	}
	return nil
}

// deleteCollection removes one expired collection, cascading its nodes,
// shares, and upload sessions (design/02 §1, design/08 §4).
func (j *Janitor) deleteCollection(ctx context.Context, c *store.Collection, sum *Summary) error {
	refs, err := j.store.NodeBlobRefs(ctx, c.ID, "")
	if err != nil {
		return fmt.Errorf("janitor: collect blob refs for collection %s: %w", c.Name, err)
	}

	if err := j.store.DeleteCollection(ctx, c.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("janitor: delete collection %s: %w", c.Name, err)
	}
	sum.CollectionsDeleted++
	j.gcBlobs(ctx, refs, sum)

	_ = j.audit.RecordDeletion(ctx, audit.ActorJanitor, audit.ActionExpiryDelete, c.Name, nil)
	return nil
}

func (j *Janitor) expireShares(ctx context.Context, now time.Time, sum *Summary) error {
	shares, err := j.store.ListExpiredShares(ctx, now, j.batchSize)
	if err != nil {
		return fmt.Errorf("janitor: list expired shares: %w", err)
	}
	for _, sh := range shares {
		if err := j.store.DeleteShare(ctx, sh.ShareID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			sum.Errors = append(sum.Errors, fmt.Sprintf("janitor: delete share %s: %v", sh.ShareID, err))
			continue
		}
		sum.SharesDeleted++
		_ = j.audit.RecordDeletion(ctx, audit.ActorJanitor, audit.ActionExpiryDelete, sh.ShareID, nil)
	}
	return nil
}

// gcBlobs unlinks blob refs that no node references anymore (design/08 §4
// step 4/§5). Blobs are content-addressed and may be shared across
// collections via dedup (design/07 §1), so this re-checks the refcount rather
// than assuming the caller's deletion was the last reference.
func (j *Janitor) gcBlobs(ctx context.Context, refs []string, sum *Summary) {
	for _, ref := range refs {
		n, err := j.store.BlobRefCount(ctx, ref)
		if err != nil {
			sum.Errors = append(sum.Errors, fmt.Sprintf("janitor: refcount blob %s: %v", ref, err))
			continue
		}
		if n > 0 {
			continue
		}
		if err := j.blob.Delete(ctx, blob.Ref(ref)); err != nil {
			if j.log != nil {
				j.log.Warn("janitor: blob unlink failed; will retry next pass", "ref", ref, "error", err)
			}
			sum.Errors = append(sum.Errors, fmt.Sprintf("janitor: unlink blob %s: %v", ref, err))
			continue
		}
		sum.BlobsDeleted++
	}
}

// collectionName resolves and caches a collection's display name for audit
// targets, tolerating a collection that vanished mid-pass (cascade-deleted by
// expireCollections running first).
func (j *Janitor) collectionName(ctx context.Context, id int64, cache map[int64]string) string {
	if name, ok := cache[id]; ok {
		return name
	}
	c, err := j.store.GetCollection(ctx, id)
	name := ""
	if err == nil {
		name = c.Name
	}
	cache[id] = name
	return name
}
