package store

import (
	"testing"
	"time"
)

func TestListExpiredNodes_Boundary(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	past := fixedNow.Add(-time.Minute)
	exact := fixedNow
	future := fixedNow.Add(time.Hour)
	blob := "sha256:aaa"

	for _, tc := range []struct {
		path      string
		expiresAt *time.Time
	}{
		{"past.txt", &past},
		{"exact.txt", &exact},
		{"future.txt", &future},
		{"forever.txt", nil},
	} {
		n := &Node{
			CollectionID: c.ID, Path: tc.path, Name: tc.path,
			Kind: KindFile, BlobID: &blob, Metadata: []byte(`{}`),
			ExpiresAt: tc.expiresAt,
		}
		if err := s.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", tc.path, err)
		}
	}

	list, err := s.ListExpiredNodes(ctx, fixedNow, 10)
	if err != nil {
		t.Fatalf("ListExpiredNodes: %v", err)
	}
	paths := make(map[string]bool, len(list))
	for _, n := range list {
		paths[n.Path] = true
	}
	if !paths["past.txt"] {
		t.Error("past.txt (expired) missing from list")
	}
	if !paths["exact.txt"] {
		t.Error("exact.txt (== now, query is <=) missing from list")
	}
	if paths["future.txt"] {
		t.Error("future.txt should not be in expired list")
	}
	if paths["forever.txt"] {
		t.Error("forever.txt (no expiry) should not be in expired list")
	}
}

func TestListExpiredNodes_Limit(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	past := fixedNow.Add(-time.Minute)
	for _, path := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		n := &Node{
			CollectionID: c.ID, Path: path, Name: path,
			Kind: KindFile, Metadata: []byte(`{}`), ExpiresAt: &past,
		}
		if err := s.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", path, err)
		}
	}

	list, err := s.ListExpiredNodes(ctx, fixedNow, 3)
	if err != nil {
		t.Fatalf("ListExpiredNodes: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("ListExpiredNodes(limit=3) = %d results, want 3", len(list))
	}
}

func TestListExpiredCollections_Boundary(t *testing.T) {
	s, ctx := newStore(t)

	past := fixedNow.Add(-time.Minute)
	future := fixedNow.Add(time.Hour)

	for _, tc := range []struct {
		name      string
		expiresAt *time.Time
	}{
		{"old", &past},
		{"new", &future},
		{"forever", nil},
	} {
		c := &Collection{Name: tc.name, Visibility: "public", ExpiresAt: tc.expiresAt}
		if err := s.CreateCollection(ctx, c); err != nil {
			t.Fatalf("CreateCollection %s: %v", tc.name, err)
		}
	}

	list, err := s.ListExpiredCollections(ctx, fixedNow, 10)
	if err != nil {
		t.Fatalf("ListExpiredCollections: %v", err)
	}
	names := make(map[string]bool, len(list))
	for _, c := range list {
		names[c.Name] = true
	}
	if !names["old"] {
		t.Error("'old' collection (expired) missing from list")
	}
	if names["new"] {
		t.Error("'new' collection should not be in expired list")
	}
	if names["forever"] {
		t.Error("'forever' collection (no expiry) should not be in expired list")
	}
}

func shareMetaWithExpiry(t time.Time) []byte {
	return []byte(`{"expiry":{"expires_at":"` + t.UTC().Format(time.RFC3339) + `"}}`)
}

func TestListExpiredShares_Boundary(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	past := fixedNow.Add(-time.Minute)
	future := fixedNow.Add(time.Hour)

	for _, tc := range []struct {
		shareID  string
		metadata []byte
	}{
		// CreateShare derives expires_at from metadata via DeriveDenormalized;
		// setting sh.ExpiresAt directly has no effect.
		{"shr_expired1111", shareMetaWithExpiry(past)},
		{"shr_active11111", shareMetaWithExpiry(future)},
		{"shr_noexpiry111", []byte(`{}`)},
	} {
		sh := &Share{ShareID: tc.shareID, CollectionID: c.ID, TargetKind: "collection", Metadata: tc.metadata}
		if err := s.CreateShare(ctx, sh); err != nil {
			t.Fatalf("CreateShare %s: %v", tc.shareID, err)
		}
	}

	list, err := s.ListExpiredShares(ctx, fixedNow, 10)
	if err != nil {
		t.Fatalf("ListExpiredShares: %v", err)
	}
	ids := make(map[string]bool, len(list))
	for _, sh := range list {
		ids[sh.ShareID] = true
	}
	if !ids["shr_expired1111"] {
		t.Error("expired share missing from list")
	}
	if ids["shr_active11111"] {
		t.Error("active share should not be in expired list")
	}
	if ids["shr_noexpiry111"] {
		t.Error("no-expiry share should not be in expired list")
	}
}

func TestNodeBlobRefs_AllBlobs(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	blob1, blob2 := "sha256:111", "sha256:222"
	nodes := []*Node{
		{CollectionID: c.ID, Path: "a.txt", Name: "a.txt", Kind: KindFile, BlobID: &blob1, Metadata: []byte(`{}`)},
		{CollectionID: c.ID, Path: "b.txt", Name: "b.txt", Kind: KindFile, BlobID: &blob2, Metadata: []byte(`{}`)},
		{CollectionID: c.ID, Path: "dir", Name: "dir", Kind: KindFolder, Metadata: []byte(`{}`)},
	}
	for _, n := range nodes {
		if err := s.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Path, err)
		}
	}

	refs, err := s.NodeBlobRefs(ctx, c.ID, "")
	if err != nil {
		t.Fatalf("NodeBlobRefs(all): %v", err)
	}
	set := make(map[string]bool)
	for _, r := range refs {
		set[r] = true
	}
	if len(refs) != 2 || !set[blob1] || !set[blob2] {
		t.Errorf("NodeBlobRefs(all) = %v, want [%s %s]", refs, blob1, blob2)
	}
}

func TestNodeBlobRefs_PathPrefix(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	blobA, blobB := "sha256:aaa", "sha256:bbb"
	nodes := []*Node{
		{CollectionID: c.ID, Path: "sub/a.txt", Name: "a.txt", Kind: KindFile, BlobID: &blobA, Metadata: []byte(`{}`)},
		{CollectionID: c.ID, Path: "sub/b.txt", Name: "b.txt", Kind: KindFile, BlobID: &blobB, Metadata: []byte(`{}`)},
		// same blob as sub/a.txt but outside prefix — should not appear
		{CollectionID: c.ID, Path: "other.txt", Name: "other.txt", Kind: KindFile, BlobID: &blobA, Metadata: []byte(`{}`)},
	}
	for _, n := range nodes {
		if err := s.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Path, err)
		}
	}

	refs, err := s.NodeBlobRefs(ctx, c.ID, "sub")
	if err != nil {
		t.Fatalf("NodeBlobRefs(sub): %v", err)
	}
	if len(refs) != 2 {
		t.Errorf("NodeBlobRefs(sub) = %v, want 2 refs", refs)
	}
	set := make(map[string]bool)
	for _, r := range refs {
		set[r] = true
	}
	if !set[blobA] || !set[blobB] {
		t.Errorf("expected both %s and %s in prefix results, got %v", blobA, blobB, refs)
	}
}

// TestBlobRefCount_SharedBlobBoundary validates the janitor's "is it safe to
// unlink?" decision: two nodes sharing one blob_id must not drop the ref count
// to zero until the last referencing node is gone.
func TestBlobRefCount_SharedBlobBoundary(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	shared := "sha256:shared"
	n1 := &Node{CollectionID: c.ID, Path: "a.txt", Name: "a.txt", Kind: KindFile, BlobID: &shared, Metadata: []byte(`{}`)}
	n2 := &Node{CollectionID: c.ID, Path: "b.txt", Name: "b.txt", Kind: KindFile, BlobID: &shared, Metadata: []byte(`{}`)}
	for _, n := range []*Node{n1, n2} {
		if err := s.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Path, err)
		}
	}

	count, err := s.BlobRefCount(ctx, shared)
	if err != nil {
		t.Fatalf("BlobRefCount (initial): %v", err)
	}
	if count != 2 {
		t.Fatalf("initial ref count = %d, want 2", count)
	}

	// Delete one node: count must be 1, blob is still referenced.
	if err := s.DeleteNode(ctx, n1.ID); err != nil {
		t.Fatalf("DeleteNode n1: %v", err)
	}
	count, err = s.BlobRefCount(ctx, shared)
	if err != nil {
		t.Fatalf("BlobRefCount (after first delete): %v", err)
	}
	if count != 1 {
		t.Errorf("ref count after first delete = %d, want 1 (blob still in use)", count)
	}

	// Delete last referencing node: count is 0, safe to unlink.
	if err := s.DeleteNode(ctx, n2.ID); err != nil {
		t.Fatalf("DeleteNode n2: %v", err)
	}
	count, err = s.BlobRefCount(ctx, shared)
	if err != nil {
		t.Fatalf("BlobRefCount (after last delete): %v", err)
	}
	if count != 0 {
		t.Errorf("ref count after last delete = %d, want 0 (safe to unlink)", count)
	}
}

func TestBlobRefCount_Distinct(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	blobX, blobY := "sha256:xxx", "sha256:yyy"
	nodes := []*Node{
		{CollectionID: c.ID, Path: "x.txt", Name: "x.txt", Kind: KindFile, BlobID: &blobX, Metadata: []byte(`{}`)},
		{CollectionID: c.ID, Path: "y.txt", Name: "y.txt", Kind: KindFile, BlobID: &blobY, Metadata: []byte(`{}`)},
	}
	for _, n := range nodes {
		if err := s.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Path, err)
		}
	}

	cx, err := s.BlobRefCount(ctx, blobX)
	if err != nil {
		t.Fatalf("BlobRefCount(blobX): %v", err)
	}
	cy, err := s.BlobRefCount(ctx, blobY)
	if err != nil {
		t.Fatalf("BlobRefCount(blobY): %v", err)
	}
	if cx != 1 || cy != 1 {
		t.Errorf("counts = (%d, %d), want (1, 1)", cx, cy)
	}

	cz, err := s.BlobRefCount(ctx, "sha256:nonexistent")
	if err != nil {
		t.Fatalf("BlobRefCount(nonexistent): %v", err)
	}
	if cz != 0 {
		t.Errorf("nonexistent blob ref count = %d, want 0", cz)
	}
}
