package main

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/mattn/go-isatty"
)

// uploadProgress renders a bubbles progress bar (design/10 §0: CLI polish via
// charmbracelet, operator decision E2) as put-tree uploads files, redrawing in
// place. It degrades to doing nothing when stderr isn't a terminal or
// --quiet/--output json is set, so scripted/non-interactive use sees plain
// output only (the existing per-file --verbose lines and final summary).
type uploadProgress struct {
	enabled bool
	w       io.Writer
	bar     progress.Model
	total   int
	done    int
}

// newUploadProgress builds a progress reporter for total files, enabled only
// when w is an interactive terminal and the caller hasn't asked for quiet or
// machine-readable output.
func newUploadProgress(w io.Writer, total int, quiet, jsonOutput bool) *uploadProgress {
	enabled := total > 0 && !quiet && !jsonOutput && isTerminal(w)
	p := &uploadProgress{enabled: enabled, w: w, total: total}
	if enabled {
		p.bar = progress.New(progress.WithDefaultGradient())
	}
	return p
}

// isTerminal reports whether w is an *os.File connected to a terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}

// advance marks one more file complete and redraws the bar in place.
func (p *uploadProgress) advance(label string) {
	if !p.enabled {
		return
	}
	p.done++
	pct := float64(p.done) / float64(p.total)
	// \x1b[K clears to end of line so a shorter label doesn't leave stray
	// characters from the previous, longer one.
	_, _ = fmt.Fprintf(p.w, "\r%s %d/%d %s\x1b[K", p.bar.ViewAs(pct), p.done, p.total, label)
}

// finish clears the progress line, leaving a clean terminal for the summary.
func (p *uploadProgress) finish() {
	if !p.enabled {
		return
	}
	_, _ = fmt.Fprint(p.w, "\r\x1b[K")
}
