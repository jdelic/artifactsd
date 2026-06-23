// Package blob implements artifactsd's content-addressed filesystem blob store
// (design/07 §1). Content is keyed by its sha256 hash, which dedupes identical
// uploads and makes writes atomic (temp file + rename) and integrity-checkable.
//
// The Store interface hides the backend so a future object-storage backend
// (S3) can replace the filesystem one without touching callers (SPEC §2).
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Algo is the only content-addressing algorithm supported in v1.
const Algo = "sha256"

// hexLen is the length of a sha256 hex digest.
const hexLen = sha256.Size * 2

// Ref identifies a blob by content hash, formatted "sha256:<hex>".
type Ref string

// ErrInvalidRef is returned when a Ref is malformed.
var ErrInvalidRef = errors.New("blob: invalid ref")

// RefFor returns the Ref for a sha256 hex digest.
func RefFor(sum string) Ref { return Ref(Algo + ":" + sum) }

// Sum returns the hex digest portion of the ref, validating its format.
func (r Ref) Sum() (string, error) {
	algo, sum, ok := strings.Cut(string(r), ":")
	if !ok || algo != Algo || len(sum) != hexLen {
		return "", fmt.Errorf("%w: %q", ErrInvalidRef, r)
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidRef, r)
	}
	return sum, nil
}

// Store is the byte-storage abstraction.
type Store interface {
	// Put streams r to storage, returning its content ref, byte size, and
	// sha256 hex sum. Identical content dedupes to the same ref.
	Put(ctx context.Context, r io.Reader) (ref Ref, size int64, sum string, err error)
	// Open returns a seekable reader for the blob (for range requests).
	Open(ctx context.Context, ref Ref) (io.ReadSeekCloser, error)
	// Delete unlinks the blob. It is idempotent (deleting a missing blob is a
	// no-op). Reference counting is the caller's responsibility: only call
	// Delete once no node references the ref (enforced by the janitor).
	Delete(ctx context.Context, ref Ref) error
	// Exists reports whether the blob is present.
	Exists(ctx context.Context, ref Ref) (bool, error)
}

// FSStore is a filesystem-backed Store rooted at a data directory.
type FSStore struct {
	blobsDir string // <data>/blobs
	tmpDir   string // <data>/tmp
}

var _ Store = (*FSStore)(nil)

// NewFSStore creates an FSStore under dataDir, creating blobs/ and tmp/.
func NewFSStore(dataDir string) (*FSStore, error) {
	s := &FSStore{
		blobsDir: filepath.Join(dataDir, "blobs"),
		tmpDir:   filepath.Join(dataDir, "tmp"),
	}
	for _, d := range []string{s.blobsDir, s.tmpDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("blob: creating %s: %w", d, err)
		}
	}
	return s, nil
}

// path returns the sharded on-disk path for a ref: blobs/sha256/ab/cd/<hex>.
func (s *FSStore) path(ref Ref) (string, error) {
	sum, err := ref.Sum()
	if err != nil {
		return "", err
	}
	return filepath.Join(s.blobsDir, Algo, sum[0:2], sum[2:4], sum), nil
}

// Put streams r to a temp file while hashing, fsyncs, then atomically renames
// into the sharded content path. If the content already exists, the temp file
// is discarded and the existing ref returned (dedupe).
func (s *FSStore) Put(ctx context.Context, r io.Reader) (Ref, int64, string, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, "", err
	}

	tmp, err := os.CreateTemp(s.tmpDir, "blob-*")
	if err != nil {
		return "", 0, "", fmt.Errorf("blob: temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup; on the success path the file is already renamed away.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hasher), r)
	if err != nil {
		return "", 0, "", fmt.Errorf("blob: writing content: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return "", 0, "", fmt.Errorf("blob: fsync: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", 0, "", fmt.Errorf("blob: closing temp: %w", err)
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	ref := RefFor(sum)
	final, err := s.path(ref)
	if err != nil {
		return "", 0, "", err
	}

	if _, statErr := os.Stat(final); statErr == nil {
		// Already stored — dedupe; temp removed by deferred cleanup.
		return ref, size, sum, nil
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return "", 0, "", fmt.Errorf("blob: creating shard dir: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return "", 0, "", fmt.Errorf("blob: rename into place: %w", err)
	}
	return ref, size, sum, nil
}

// Open returns a seekable reader for the blob.
func (s *FSStore) Open(_ context.Context, ref Ref) (io.ReadSeekCloser, error) {
	p, err := s.path(ref)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p) //nolint:gosec // p is derived from a validated content hash, not user input
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Exists reports whether the blob is present.
func (s *FSStore) Exists(_ context.Context, ref Ref) (bool, error) {
	p, err := s.path(ref)
	if err != nil {
		return false, err
	}
	switch _, err := os.Stat(p); {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// Delete unlinks the blob; missing blobs are not an error.
func (s *FSStore) Delete(_ context.Context, ref Ref) error {
	p, err := s.path(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("blob: delete: %w", err)
	}
	return nil
}
