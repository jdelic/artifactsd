package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/jdelic/artifactsd/internal/buildinfo"
	"github.com/jdelic/artifactsd/internal/config"
)

// bannerArt is the artifactsd wordmark (FIGlet "ANSI Shadow").
const bannerArt = ` █████╗ ██████╗ ████████╗██╗███████╗ █████╗  ██████╗████████╗███████╗██████╗
██╔══██╗██╔══██╗╚══██╔══╝██║██╔════╝██╔══██╗██╔════╝╚══██╔══╝██╔════╝██╔══██╗
███████║██████╔╝   ██║   ██║█████╗  ███████║██║        ██║   ███████╗██║  ██║
██╔══██║██╔══██╗   ██║   ██║██╔══╝  ██╔══██║██║        ██║   ╚════██║██║  ██║
██║  ██║██║  ██║   ██║   ██║██║     ██║  ██║╚██████╗   ██║   ███████║██████╔╝
╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝   ╚═╝╚═╝     ╚═╝  ╚═╝ ╚═════╝   ╚═╝   ╚══════╝╚═════╝`

// bannerGradient is the per-line 256-color foreground ramp (cyan → blue) the
// art is painted with, top to bottom.
var bannerGradient = []int{51, 45, 39, 33, 27, 21}

// printBanner writes the startup banner to w. ANSI color is emitted only when w
// is a terminal and NO_COLOR is unset (https://no-color.org); otherwise a plain
// banner is written so piped/journald logs stay clean.
func printBanner(w io.Writer, cfg *config.Config) {
	color := bannerUsesColor(w)
	var b strings.Builder
	b.WriteByte('\n')

	for i, line := range strings.Split(bannerArt, "\n") {
		if color {
			fmt.Fprintf(&b, "\x1b[1;38;5;%dm%s\x1b[0m\n", bannerGradient[i%len(bannerGradient)], line)
		} else {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	tagline := "  self-contained artifact store & server  ·  " + buildinfo.String()
	info := fmt.Sprintf("  listen %s  ·  base %s  ·  tls %s  ·  data %s  ·  %s",
		cfg.Server.Listen, dashIfEmpty(cfg.Server.BaseURL), cfg.TLS.Mode, cfg.Server.DataDir, runtime.Version())
	if color {
		fmt.Fprintf(&b, "\x1b[1;36m%s\x1b[0m\n", tagline)
		fmt.Fprintf(&b, "\x1b[2m%s\x1b[0m\n\n", info)
	} else {
		b.WriteString(tagline + "\n")
		b.WriteString(info + "\n\n")
	}

	_, _ = io.WriteString(w, b.String())
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// bannerUsesColor reports whether ANSI color should be emitted to w: only when
// NO_COLOR is unset and w is a character device (an attached terminal).
func bannerUsesColor(w io.Writer) bool {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
