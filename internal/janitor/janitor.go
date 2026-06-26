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

// defaultUploadTTL matches config's default (design/12 §4) for callers that
// don't set Deps.UploadTTL explicitly.
const defaultUploadTTL = time.Hour

// reconcileEvery is how many RunOnce passes occur between Reconcile passes in
// Loop (design/08 §7's blob_gc_every; not yet exposed as its own config key).
const reconcileEvery = 30

// scrubEvery is how many RunOnce passes occur between integrity Scrub passes
// in Loop. 300 passes makes it 10× less frequent than Reconcile — a full
// re-hash of every blob is I/O-intensive and is only needed to surface bitrot
// (design/07 §1).
const scrubEvery = 300

// Deps are the janitor's dependencies.
type Deps struct {
	Store     *store.Store
	Blob      blob.Store
	Audit     *audit.Recorder
	Logger    *slog.Logger
	BatchSize int           // 0 uses defaultBatchSize
	UploadTTL time.Duration // 0 uses defaultUploadTTL; temp files older than this are swept
}

// Janitor performs expiry-driven deletion passes (design/08). It satisfies
// api.Cleaner (RunOnce) so it can be wired as the manual-cleanup backend.
type Janitor struct {
	store     *store.Store
	blob      blob.Store
	audit     *audit.Recorder
	log       *slog.Logger
	batchSize int
	uploadTTL time.Duration
	now       func() time.Time
}

// New builds a Janitor from Deps.
func New(d Deps) *Janitor { //nolint:gocritic // Deps is built once at construction
	bs := d.BatchSize
	if bs <= 0 {
		bs = defaultBatchSize
	}
	ttl := d.UploadTTL
	if ttl <= 0 {
		ttl = defaultUploadTTL
	}
	return &Janitor{
		store: d.Store, blob: d.Blob, audit: d.Audit, log: d.Logger, batchSize: bs, uploadTTL: ttl,
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
	NodesDeleted          int      `json:"nodes_deleted"`
	CollectionsDeleted    int      `json:"collections_deleted"`
	SharesDeleted         int      `json:"shares_deleted"`
	UploadSessionsExpired int      `json:"upload_sessions_expired"`
	BlobsDeleted          int      `json:"blobs_deleted"`
	Errors                []string `json:"errors,omitempty"`
}

// RunOnce performs a single expiry sweep: delete expired nodes, collections,
// and shares (earliest-expiry-in-the-tree-wins is already baked into the
// denormalized expires_at, design/03 §4), expire stale pending upload
// sessions, and unlink blobs that become unreferenced as a result, with an
// audit record per deletion (design/08 §4). Per-item failures are collected
// into Summary.Errors rather than aborting the pass (design/08 §8: failures
// are logged and retried next pass). The heavier full orphan-blob scan and
// temp-file sweep are not part of this fast pass; see Reconcile.
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
	if err := j.expireUploadSessions(ctx, now, sum); err != nil {
		sum.Errors = append(sum.Errors, err.Error())
	}

	if j.log != nil {
		j.log.Info("janitor: pass complete",
			"nodes_deleted", sum.NodesDeleted, "collections_deleted", sum.CollectionsDeleted,
			"shares_deleted", sum.SharesDeleted, "upload_sessions_expired", sum.UploadSessionsExpired,
			"blobs_deleted", sum.BlobsDeleted, "errors", len(sum.Errors))
	}
	return sum, nil
}

// ReconcileSummary reports one Reconcile pass's results (design/08 §5/§8).
type ReconcileSummary struct {
	OrphanBlobsDeleted int      `json:"orphan_blobs_deleted"`
	TempFilesRemoved   int      `json:"temp_files_removed"`
	Errors             []string `json:"errors,omitempty"`
}

// Reconcile performs the heavier, less-frequent sweep that RunOnce skips: a
// full scan of every stored blob ref, unlinking any with no referencing node
// (catches orphans from a crash between blob.Put and the node write, design/07
// §6), and removal of stale temp files. It is a no-op for a blob.Store backend
// that doesn't implement blob.Lister/blob.TempSweeper (design/08 §5).
func (j *Janitor) Reconcile(ctx context.Context) (*ReconcileSummary, error) {
	sum := &ReconcileSummary{}

	if lister, ok := j.blob.(blob.Lister); ok {
		refs, err := lister.ListRefs(ctx)
		if err != nil {
			sum.Errors = append(sum.Errors, fmt.Sprintf("janitor: list blob refs: %v", err))
		}
		for _, ref := range refs {
			n, err := j.store.BlobRefCount(ctx, string(ref))
			if err != nil {
				sum.Errors = append(sum.Errors, fmt.Sprintf("janitor: refcount blob %s: %v", ref, err))
				continue
			}
			if n > 0 {
				continue
			}
			if err := j.blob.Delete(ctx, ref); err != nil {
				sum.Errors = append(sum.Errors, fmt.Sprintf("janitor: unlink orphan blob %s: %v", ref, err))
				continue
			}
			sum.OrphanBlobsDeleted++
		}
	}

	if sweeper, ok := j.blob.(blob.TempSweeper); ok {
		removed, err := sweeper.SweepTemp(ctx, j.uploadTTL)
		if err != nil {
			sum.Errors = append(sum.Errors, fmt.Sprintf("janitor: sweep temp: %v", err))
		}
		sum.TempFilesRemoved = removed
	}

	if j.log != nil {
		j.log.Info("janitor: reconcile complete",
			"orphan_blobs_deleted", sum.OrphanBlobsDeleted, "temp_files_removed", sum.TempFilesRemoved,
			"errors", len(sum.Errors))
	}
	return sum, nil
}

// ScrubSummary reports one Scrub pass's results (design/07 §1).
type ScrubSummary struct {
	BlobsVerified  int      `json:"blobs_verified"`
	BlobsCorrupted int      `json:"blobs_corrupted"`
	Errors         []string `json:"errors,omitempty"`
}

// Scrub re-hashes every stored blob and reports any whose digest does not
// match the hash embedded in their ref (bitrot detection, design/07 §1). It is
// a no-op when the blob.Store does not implement both blob.Lister and
// blob.Scrubber (e.g. a future S3 backend that delegates integrity to the
// object store). Corruption is logged at ERROR level; the summary is returned
// for the caller to surface to operators.
func (j *Janitor) Scrub(ctx context.Context) (*ScrubSummary, error) {
	sum := &ScrubSummary{}
	lister, ok := j.blob.(blob.Lister)
	if !ok {
		return sum, nil
	}
	scrubber, ok := j.blob.(blob.Scrubber)
	if !ok {
		return sum, nil
	}

	refs, err := lister.ListRefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("janitor: scrub list refs: %w", err)
	}
	for _, ref := range refs {
		if err := scrubber.VerifyRef(ctx, ref); err != nil {
			if j.log != nil {
				j.log.Error("janitor: blob corruption detected", "ref", ref, "error", err)
			}
			sum.Errors = append(sum.Errors, err.Error())
			sum.BlobsCorrupted++
			continue
		}
		sum.BlobsVerified++
	}
	if j.log != nil {
		j.log.Info("janitor: scrub complete",
			"blobs_verified", sum.BlobsVerified, "blobs_corrupted", sum.BlobsCorrupted,
			"errors", len(sum.Errors))
	}
	return sum, nil
}

// Loop runs RunOnce on a ticker until ctx is canceled, interleaving a less
// frequent Reconcile pass every reconcileEvery ticks and an integrity Scrub
// every scrubEvery ticks (design/08 §1/§7, design/07 §1).
func (j *Janitor) Loop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	passes := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := j.RunOnce(ctx); err != nil && j.log != nil {
				j.log.Error("janitor: pass failed", "error", err)
			}
			passes++
			if passes%reconcileEvery == 0 {
				if _, err := j.Reconcile(ctx); err != nil && j.log != nil {
					j.log.Error("janitor: reconcile failed", "error", err)
				}
			}
			if passes%scrubEvery == 0 {
				if _, err := j.Scrub(ctx); err != nil && j.log != nil {
					j.log.Error("janitor: scrub failed", "error", err)
				}
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

// expireUploadSessions marks pending large-upload sessions past their TTL as
// expired (design/07 §6, design/08 §5). A pending session never has a
// referenced blob — Put only commits a blob once a PUT completes — so there is
// no temp blob to unlink here; stray temp files are handled by Reconcile.
func (j *Janitor) expireUploadSessions(ctx context.Context, now time.Time, sum *Summary) error {
	sessions, err := j.store.ListExpiredUploadSessions(ctx, now, j.batchSize)
	if err != nil {
		return fmt.Errorf("janitor: list expired upload sessions: %w", err)
	}
	for _, s := range sessions {
		if err := j.store.SetUploadStatus(ctx, s.UploadID, store.UploadExpired); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			sum.Errors = append(sum.Errors, fmt.Sprintf("janitor: expire upload session %s: %v", s.UploadID, err))
			continue
		}
		sum.UploadSessionsExpired++
		_ = j.audit.RecordDeletion(ctx, audit.ActorJanitor, audit.ActionExpiryDelete, "upload:"+s.UploadID, nil)
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
