// Package durations parses the artifactsd duration syntax: Go's time.Duration
// units (h/m/s/ms/us/ns) extended with 'd' (days) and 'w' (weeks), e.g. "7d",
// "30d", "2w", "1d12h", "48h". Used for metadata expiry directives and config
// (design/04 §1, design/03 §4).
package durations

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dayWeekToken matches a "<number>d" or "<number>w" component.
var dayWeekToken = regexp.MustCompile(`(\d+(?:\.\d+)?)([dw])`)

// Parse parses an extended duration string. Day/week tokens are expanded into
// hours and the result handed to time.ParseDuration.
func Parse(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("durations: empty duration")
	}
	expanded := dayWeekToken.ReplaceAllStringFunc(s, func(tok string) string {
		m := dayWeekToken.FindStringSubmatch(tok)
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return tok // leave it for ParseDuration to reject
		}
		hours := v * 24
		if m[2] == "w" {
			hours *= 7
		}
		return strconv.FormatFloat(hours, 'f', -1, 64) + "h"
	})
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("durations: invalid duration %q", s)
	}
	return d, nil
}
