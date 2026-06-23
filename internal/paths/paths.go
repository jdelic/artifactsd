// Package paths provides traversal-safe validation and normalization of
// collection names and collection-relative object paths (design/06 §3,
// design/02 §2). It is the single chokepoint every user-supplied path/name
// passes through before becoming a DB key, URL segment, or filesystem path.
package paths

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Validation errors. Callers may use errors.Is to branch, but most just surface
// the message.
var (
	ErrEmptyPath          = errors.New("paths: empty path")
	ErrTraversal          = errors.New("paths: path contains a traversal segment")
	ErrControlChar        = errors.New("paths: path contains a NUL or control character")
	ErrBackslash          = errors.New("paths: path contains a backslash")
	ErrDriveOrUNC         = errors.New("paths: path looks like a Windows drive or UNC form")
	ErrInvalidCollection  = errors.New("paths: invalid collection name")
	ErrOutsideBase        = errors.New("paths: resolved path escapes its base directory")
	maxPathLen            = 4096
	maxCollectionNameSize = 128
)

// collectionNameRe is the slug regex from design/02 §2: lower-case start,
// then lower-case alnum plus . _ - up to 128 chars total.
var (
	collectionNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	driveRe          = regexp.MustCompile(`^[A-Za-z]:`)
)

// Clean normalizes a collection-relative path and rejects traversal and unsafe
// forms. It returns the canonical path: no leading slash, single slashes, no
// "." / ".." / empty segments. A leading slash is tolerated and stripped (the
// path is always collection-root-relative).
func Clean(p string) (string, error) {
	if p == "" {
		return "", ErrEmptyPath
	}
	if len(p) > maxPathLen {
		return "", fmt.Errorf("paths: path exceeds %d bytes", maxPathLen)
	}
	for _, r := range p {
		switch {
		case r == 0x00 || r < 0x20 || r == 0x7f:
			return "", ErrControlChar
		case r == '\\':
			return "", ErrBackslash
		}
	}
	if driveRe.MatchString(p) {
		return "", ErrDriveOrUNC
	}

	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		switch s {
		case "":
			// Collapse // and strip leading/trailing slashes.
			continue
		case ".", "..":
			return "", ErrTraversal
		default:
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return "", ErrEmptyPath
	}
	return strings.Join(out, "/"), nil
}

// ValidateCollectionName reports whether name is a valid collection slug.
func ValidateCollectionName(name string) error {
	if name == "" || len(name) > maxCollectionNameSize {
		return fmt.Errorf("%w: must be 1-%d chars", ErrInvalidCollection, maxCollectionNameSize)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("%w: must not contain '..'", ErrInvalidCollection)
	}
	if !collectionNameRe.MatchString(name) {
		return fmt.Errorf("%w: %q does not match %s", ErrInvalidCollection, name, collectionNameRe.String())
	}
	return nil
}

// EnsureWithin verifies that joining rel onto base resolves to a path still
// inside base (defense-in-depth against symlink/edge traversal, design/06 §3).
// base and rel should already be cleaned; rel is treated as relative.
func EnsureWithin(base, rel string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(absBase, filepath.FromSlash(rel))
	// filepath.Join cleans the result; verify it is base or a descendant.
	if joined != absBase && !strings.HasPrefix(joined, absBase+string(filepath.Separator)) {
		return "", ErrOutsideBase
	}
	return joined, nil
}
