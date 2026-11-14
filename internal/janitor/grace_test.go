package janitor

import (
	"bytes"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/blob"
)

// TestReconcile_GracePeriodForFreshBlobs: a just-stored blob that no node
// references yet (an upload between blob.Put and its node write) must survive
// Reconcile; once older than the grace period it is collected
// (artifactsd-m9e.10).
func TestReconcile_GracePeriodForFreshBlobs(t *testing.T) {
	j, _, bs, ctx := newTestJanitor(t)
	ref, _, _, err := bs.Put(ctx, bytes.NewReader([]byte("in-flight upload")))
	if err != nil {
		t.Fatal(err)
	}

	sum, err := j.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.OrphanBlobsDeleted != 0 || len(sum.Errors) != 0 {
		t.Fatalf("fresh orphan: %+v, want kept without errors", sum)
	}
	if ok, _ := bs.Exists(ctx, ref); !ok {
		t.Fatal("fresh unreferenced blob was deleted")
	}

	later := time.Now().Add(j.blobGrace() + time.Minute)
	j.WithClock(func() time.Time { return later })
	if sum, _ := j.Reconcile(ctx); sum.OrphanBlobsDeleted != 1 {
		t.Errorf("old orphan: %+v, want deleted", sum)
	}
	if ok, _ := bs.Exists(ctx, ref); ok {
		t.Error("orphan older than the grace period survived")
	}
}

// TestRunOnce_ExpiryKeepsFreshBlob: expiring the last node that references a
// fresh blob does not unlink it in that pass, and that is not an error.
func TestRunOnce_ExpiryKeepsFreshBlob(t *testing.T) {
	j, st, bs, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", nil)
	n := mustFileNode(t, st, bs, ctx, c.ID, "gone.txt", "fresh content", past(t))
	res, err := j.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sum := res.(*Summary)
	if sum.NodesDeleted != 1 || sum.BlobsDeleted != 0 || len(sum.Errors) != 0 {
		t.Errorf("summary = %+v, want node deleted, blob kept, no errors", sum)
	}
	if ok, _ := bs.Exists(ctx, blob.Ref(*n.BlobID)); !ok {
		t.Error("fresh blob unlinked by the expiry pass")
	}
}

// pastGrace moves j's clock beyond the blob grace period, so tests of the
// orphan-collection paths see blobs as old enough to delete.
func pastGrace(j *Janitor) {
	t := time.Now().Add(j.blobGrace() + time.Minute)
	j.WithClock(func() time.Time { return t })
}
