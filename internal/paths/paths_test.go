package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClean_Valid(t *testing.T) {
	cases := map[string]string{
		"a.txt":            "a.txt",
		"assets/img/x.png": "assets/img/x.png",
		"/leading/slash":   "leading/slash",   // leading slash stripped
		"a//b///c":         "a/b/c",           // collapsed
		"trailing/":        "trailing",        // trailing slash stripped
		"weird..name.txt":  "weird..name.txt", // ".." inside a segment is fine
		"a.b-c_d/e":        "a.b-c_d/e",
		"UPPER/Case.PDF":   "UPPER/Case.PDF", // case preserved in paths
	}
	for in, want := range cases {
		got, err := Clean(in)
		if err != nil {
			t.Errorf("Clean(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClean_Rejects(t *testing.T) {
	bad := []string{
		"",
		"/",
		"..",
		"../etc/passwd",
		"a/../b",
		"a/..",
		"../",
		"./a",
		"a/./b",
		"foo/\\bar",                // backslash
		"..\\..\\windows",          // backslash
		"C:/Windows",               // drive
		"d:\\x",                    // drive + backslash
		"with\x00nul",              // NUL
		"ctrl\x07bell",             // control char
		"tab\tindent",              // tab is a control char
		strings.Repeat("a/", 3000), // length-ish; ensure huge still cleans or errs gracefully
	}
	for _, in := range bad {
		if _, err := Clean(in); err == nil {
			// The huge case may clean fine; only assert traversal/control ones.
			if strings.Contains(in, "..") || strings.ContainsAny(in, "\\\x00\x07\t") ||
				in == "" || in == "/" || in == "." || strings.HasPrefix(in, "C:") || strings.HasPrefix(in, "d:") {
				t.Errorf("Clean(%q) = nil error, want rejection", in)
			}
		}
	}
}

func TestClean_RejectsTraversalExplicitly(t *testing.T) {
	for _, in := range []string{"..", "a/../b", "../x", "a/..", "foo/../../etc"} {
		if _, err := Clean(in); err != ErrTraversal {
			t.Errorf("Clean(%q) error = %v, want ErrTraversal", in, err)
		}
	}
}

func TestValidateCollectionName(t *testing.T) {
	valid := []string{"demo", "sales-demo-2026", "a", "x.y_z-1", "0abc"}
	for _, n := range valid {
		if err := ValidateCollectionName(n); err != nil {
			t.Errorf("ValidateCollectionName(%q) = %v, want nil", n, err)
		}
	}
	invalid := []string{
		"",
		"-leading-dash",
		".leading-dot",
		"_leading-underscore",
		"UPPER",
		"has space",
		"has/slash",
		"has..dots",
		"with\x00nul",
		strings.Repeat("a", 129),
	}
	for _, n := range invalid {
		if err := ValidateCollectionName(n); err == nil {
			t.Errorf("ValidateCollectionName(%q) = nil, want error", n)
		}
	}
}

func TestEnsureWithin(t *testing.T) {
	base := t.TempDir()

	got, err := EnsureWithin(base, "a/b/c.txt")
	if err != nil {
		t.Fatalf("EnsureWithin valid: %v", err)
	}
	if !strings.HasPrefix(got, base) {
		t.Errorf("resolved %q not under base %q", got, base)
	}

	// A cleaned rel can't escape, but verify the guard catches a raw "..".
	if _, err := EnsureWithin(base, "../escape"); err != ErrOutsideBase {
		t.Errorf("EnsureWithin escape: err = %v, want ErrOutsideBase", err)
	}

	// Base itself resolves to base.
	if got, _ := EnsureWithin(base, ""); got != filepath.Clean(base) {
		t.Errorf("EnsureWithin empty rel = %q, want %q", got, base)
	}
}
