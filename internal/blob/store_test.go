package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *FSStore {
	t.Helper()
	s, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	return s
}

func TestPutOpen_RoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	content := []byte("hello artifactsd")

	ref, size, sum, err := s.Put(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("size = %d, want %d", size, len(content))
	}
	wantSum := sha256.Sum256(content)
	if sum != hex.EncodeToString(wantSum[:]) {
		t.Errorf("sum = %s, want %s", sum, hex.EncodeToString(wantSum[:]))
	}
	if ref != RefFor(sum) {
		t.Errorf("ref = %q, want %q", ref, RefFor(sum))
	}

	rc, err := s.Open(ctx, ref)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q", got)
	}
}

func TestOpen_Seekable(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ref, _, _, err := s.Put(ctx, strings.NewReader("0123456789"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := s.Open(ctx, ref)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if _, err := rc.Seek(5, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	got, _ := io.ReadAll(rc)
	if string(got) != "56789" {
		t.Errorf("after seek got %q, want 56789", got)
	}
}

func TestPut_Dedupe(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	content := []byte("dedupe me")

	ref1, _, _, err := s.Put(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	ref2, _, _, err := s.Put(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if ref1 != ref2 {
		t.Errorf("dedupe failed: %q != %q", ref1, ref2)
	}

	// Exactly one blob file on disk under the sha256 tree.
	var files int
	_ = filepath.WalkDir(s.blobsDir, func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			files++
		}
		return nil
	})
	if files != 1 {
		t.Errorf("blob files on disk = %d, want 1 (dedupe)", files)
	}
}

func TestPut_ShardedPathAndNoTempLeak(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ref, _, sum, err := s.Put(ctx, strings.NewReader("shard"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := filepath.Join(s.blobsDir, "sha256", sum[0:2], sum[2:4], sum)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected blob at sharded path %s: %v", want, err)
	}
	_ = ref

	// Temp dir must be empty (no leaked temp files).
	entries, _ := os.ReadDir(s.tmpDir)
	if len(entries) != 0 {
		t.Errorf("tmp dir not empty: %d entries", len(entries))
	}
}

func TestExistsAndDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ref, _, _, err := s.Put(ctx, strings.NewReader("transient"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if ok, _ := s.Exists(ctx, ref); !ok {
		t.Fatal("Exists = false after Put")
	}
	if err := s.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, _ := s.Exists(ctx, ref); ok {
		t.Error("Exists = true after Delete")
	}
	// Delete is idempotent.
	if err := s.Delete(ctx, ref); err != nil {
		t.Errorf("second Delete should be no-op, got %v", err)
	}
}

func TestRef_Validation(t *testing.T) {
	valid := RefFor(strings.Repeat("a", hexLen))
	if _, err := valid.Sum(); err != nil {
		t.Errorf("valid ref rejected: %v", err)
	}
	bad := []Ref{
		"",
		"sha256:",
		"sha256:tooshort",
		Ref("md5:" + strings.Repeat("a", hexLen)),
		Ref("sha256:" + strings.Repeat("g", hexLen)), // non-hex
	}
	for _, r := range bad {
		if _, err := r.Sum(); err == nil {
			t.Errorf("expected invalid ref error for %q", r)
		}
	}
}

func TestOpen_MissingRef(t *testing.T) {
	s := newStore(t)
	if _, err := s.Open(context.Background(), "sha256:bad"); err == nil {
		t.Error("expected error opening invalid ref")
	}
}
