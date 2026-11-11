package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// effTree builds, at fixedNow:
//
//	coll "c"            delete_after_creation 10d
//	  docs/             delete_after_last_access 5d   (folder)
//	    deep/           -                              (folder)
//	      a.txt         expires_at now+20d
//	    c.txt           delete_after_creation 1d
//	  b.txt             -
//	coll "z"            -
//	  free.txt          -
func effTree(t *testing.T) (*Store, context.Context, *Collection) {
	t.Helper()
	s, ctx := newStore(t)
	c := &Collection{Name: "c", Metadata: []byte(`{"expiry":{"delete_after_creation":"10d"}}`)}
	if err := s.CreateCollection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.RecomputeCollectionDenormalized(ctx, c.ID, "private"); err != nil {
		t.Fatal(err)
	}
	z := mustCollection(t, s, ctx, "z")

	put := func(coll *Collection, p, kind, meta string) {
		t.Helper()
		parent, err := s.EnsureParentFolders(ctx, coll.ID, p)
		if err != nil {
			t.Fatal(err)
		}
		name := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			name = p[i+1:]
		}
		n, err := s.GetNode(ctx, coll.ID, p)
		if err != nil {
			n = &Node{CollectionID: coll.ID, ParentID: parent, Path: p, Name: name, Kind: kind}
		}
		n.Metadata = []byte(meta)
		if err := s.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		if err := s.RecomputeNodeDenormalized(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	put(c, "docs/deep/a.txt", KindFile, `{"expiry":{"expires_at":"`+fixedNow.Add(20*day).Format(time.RFC3339)+`"}}`)
	put(c, "docs", KindFolder, `{"expiry":{"delete_after_last_access":"5d"}}`)
	put(c, "docs/c.txt", KindFile, `{"expiry":{"delete_after_creation":"1d"}}`)
	put(c, "b.txt", KindFile, `{}`)
	put(z, "free.txt", KindFile, `{}`)
	c, _ = s.GetCollection(ctx, c.ID)
	return s, ctx, c
}

func wantEff(t *testing.T, what string, got EffectiveExpiry, at *time.Time, kind, path, rule string) {
	t.Helper()
	switch {
	case at == nil && got.At != nil, at != nil && (got.At == nil || !got.At.Equal(*at)):
		t.Errorf("%s: at = %v, want %v", what, got.At, at)
	case got.Source.Kind != kind || got.Source.Path != path || got.Source.Rule != rule:
		t.Errorf("%s: source = %+v, want {%s %s %s}", what, got.Source, kind, path, rule)
	}
}

func tp(t time.Time) *time.Time { return &t }

func TestDirAndChildExpiry(t *testing.T) {
	s, ctx, c := effTree(t)

	root, err := s.DirExpiry(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	wantEff(t, "root", root, tp(fixedNow.Add(10*day)), ExpiryCollection, "", "10d after creation")

	deep, err := s.DirExpiry(ctx, c, "docs/deep")
	if err != nil {
		t.Fatal(err)
	}
	wantEff(t, "docs/deep", deep, tp(fixedNow.Add(5*day)), ExpiryFolder, "docs", "5d after last access")

	a, _ := s.GetNode(ctx, c.ID, "docs/deep/a.txt")
	wantEff(t, "a.txt", s.ChildExpiry(deep, a), tp(fixedNow.Add(5*day)), ExpiryFolder, "docs", "5d after last access")

	docs, _ := s.DirExpiry(ctx, c, "docs")
	cf, _ := s.GetNode(ctx, c.ID, "docs/c.txt")
	wantEff(t, "c.txt", s.ChildExpiry(docs, cf), tp(fixedNow.Add(1*day)), ExpiryOwn, "docs/c.txt", "1d after creation")

	b, _ := s.GetNode(ctx, c.ID, "b.txt")
	wantEff(t, "b.txt", s.ChildExpiry(root, b), tp(fixedNow.Add(10*day)), ExpiryCollection, "", "10d after creation")
}

func TestListAllFiles_EffectiveExpiry(t *testing.T) {
	s, ctx, _ := effTree(t)
	files, err := s.ListAllFiles(ctx, FileSortExpiry, 0)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	byPath := map[string]*FileWithCollection{}
	for _, f := range files {
		order = append(order, f.CollectionName+"/"+f.Path)
		byPath[f.CollectionName+"/"+f.Path] = f
	}
	want := []string{"c/docs/c.txt", "c/docs/deep/a.txt", "c/b.txt", "z/free.txt"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	wantEff(t, "c.txt", byPath["c/docs/c.txt"].Effective, tp(fixedNow.Add(1*day)), ExpiryOwn, "docs/c.txt", "1d after creation")
	wantEff(t, "a.txt", byPath["c/docs/deep/a.txt"].Effective, tp(fixedNow.Add(5*day)), ExpiryFolder, "docs", "5d after last access")
	wantEff(t, "b.txt", byPath["c/b.txt"].Effective, tp(fixedNow.Add(10*day)), ExpiryCollection, "", "10d after creation")
	wantEff(t, "free.txt", byPath["z/free.txt"].Effective, nil, "", "", "")
	if a := byPath["c/docs/deep/a.txt"]; a.ExpiresAt == nil || !a.ExpiresAt.Equal(fixedNow.Add(20*day)) {
		t.Errorf("a.txt own expires_at = %v, want now+20d", a.ExpiresAt)
	}
}

// When a container and the object expire at the same instant, the container
// is credited: its deletion is what removes the object.
func TestEffectiveExpiry_TieGoesToContainer(t *testing.T) {
	s, ctx := newStore(t)
	c := &Collection{Name: "c", Metadata: []byte(`{"expiry":{"delete_after_creation":"3d"}}`)}
	if err := s.CreateCollection(ctx, c); err != nil {
		t.Fatal(err)
	}
	_ = s.RecomputeCollectionDenormalized(ctx, c.ID, "private")
	n := &Node{CollectionID: c.ID, Path: "f", Name: "f", Kind: KindFile, Metadata: []byte(`{"expiry":{"delete_after_creation":"3d"}}`)}
	if err := s.UpsertNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	_ = s.RecomputeNodeDenormalized(ctx, n.ID)
	c, _ = s.GetCollection(ctx, c.ID)
	n, _ = s.GetNode(ctx, c.ID, "f")

	root, _ := s.DirExpiry(ctx, c, "")
	wantEff(t, "explorer", s.ChildExpiry(root, n), tp(fixedNow.Add(3*day)), ExpiryCollection, "", "3d after creation")
	files, _ := s.ListAllFiles(ctx, FileSortExpiry, 0)
	wantEff(t, "files view", files[0].Effective, tp(fixedNow.Add(3*day)), ExpiryCollection, "", "3d after creation")
}

func TestExpiryRule_ServerDefault(t *testing.T) {
	s, ctx := newStore(t)
	s.WithDefaultDeleteAfterCreation(14 * day)
	c := mustCollection(t, s, ctx, "c")
	_ = s.RecomputeCollectionDenormalized(ctx, c.ID, "private")
	c, _ = s.GetCollection(ctx, c.ID)
	root, _ := s.DirExpiry(ctx, c, "")
	wantEff(t, "default", root, tp(fixedNow.Add(14*day)), ExpiryCollection, "", "server default: 14d after creation")
}
