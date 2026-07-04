// Package units parses human-readable byte sizes shared by the CLI and the
// admin UI.
package units

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseByteSize parses a human-readable byte size ("10MB", "500KiB", "1024")
// into bytes. Per operator decision (2026-07-04), every unit is a power of
// 1024: KB and KiB both mean 1024 bytes, MB/MiB 1024², GB/GiB 1024³, TB/TiB
// 1024⁴. A bare number is bytes.
func ParseByteSize(s string) (int64, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("empty size")
	}

	split := len(trimmed)
	for i, r := range trimmed {
		if (r < '0' || r > '9') && r != '.' {
			split = i
			break
		}
	}
	numPart := trimmed[:split]
	unitPart := strings.TrimSpace(trimmed[split:])

	n, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}

	mult, err := unitMultiplier(unitPart)
	if err != nil {
		return 0, err
	}
	v := n * float64(mult)
	if v < 0 {
		return 0, fmt.Errorf("negative size %q", s)
	}
	return int64(v), nil
}

// unitMultiplier maps a unit suffix to its (1024-based) byte multiplier. The
// IEC "i" infix (KiB/MiB/...) is accepted as an alternate spelling of the
// same value.
func unitMultiplier(unit string) (int64, error) {
	u := strings.ToLower(unit)
	u = strings.TrimSuffix(u, "b")
	u = strings.TrimSuffix(u, "i")
	switch u {
	case "":
		return 1, nil
	case "k":
		return 1 << 10, nil
	case "m":
		return 1 << 20, nil
	case "g":
		return 1 << 30, nil
	case "t":
		return 1 << 40, nil
	default:
		return 0, fmt.Errorf("unknown size unit %q (use B, KB, MB, GB, TB or KiB, MiB, GiB, TiB)", unit)
	}
}
