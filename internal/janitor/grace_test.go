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

// TestReconcile_DedupeDuringCollection: an upload that re-uses an orphan blob
// while the janitor is collecting it must end with the blob present and
// referenced — the upload either refreshes the blob first or stores a fresh
// copy after the unlink, never references a deleted file (artifactsd-7tl.5).
func TestReconcile_DedupeDuringCollection(t *testing.T) {
	j, st, bs, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", nil)
	ref, _, _, err := bs.Put(ctx, bytes.NewReader([]byte("shared bytes")))
	if err != nil {
		t.Fatal(err)
	}
	pastGrace(j) // the orphan is old enough to collect

	done := make(chan struct{})
	collectHook = func(r blob.Ref) {
		if r != ref {
			return
		}
		collectHook = nil
		// The janitor has seen an old, unreferenced blob and is about to
		// unlink it. A concurrent upload of the same bytes starts now.
		go func() {
			defer close(done)
			mustFileNode(t, st, bs, ctx, c.ID, "reuse.txt", "shared bytes", nil)
		}()
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
		}
	}
	defer func() { collectHook = nil }()

	if _, err := j.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	n, err := st.BlobRefCount(ctx, string(ref))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := bs.Exists(ctx, ref); n != 1 || !ok {
		t.Errorf("after racing upload: refcount=%d exists=%v, want 1 and true", n, ok)
	}
}

// TestRunOnce_DedupeDuringExpiryGC: the same race on the post-expiry GC path.
func TestRunOnce_DedupeDuringExpiryGC(t *testing.T) {
	j, st, bs, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", nil)
	old := mustFileNode(t, st, bs, ctx, c.ID, "old.txt", "shared bytes", past(t))
	ref := blob.Ref(*old.BlobID)
	pastGrace(j)

	done := make(chan struct{})
	collectHook = func(r blob.Ref) {
		if r != ref {
			return
		}
		collectHook = nil
		go func() {
			defer close(done)
			mustFileNode(t, st, bs, ctx, c.ID, "new.txt", "shared bytes", nil)
		}()
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
		}
	}
	defer func() { collectHook = nil }()

	if _, err := j.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	n, _ := st.BlobRefCount(ctx, string(ref))
	if ok, _ := bs.Exists(ctx, ref); n != 1 || !ok {
		t.Errorf("after racing upload: refcount=%d exists=%v, want 1 and true", n, ok)
	}
}
