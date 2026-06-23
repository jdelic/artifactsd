package durations

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := map[string]time.Duration{
		"7d":    7 * 24 * time.Hour,
		"30d":   30 * 24 * time.Hour,
		"2w":    2 * 7 * 24 * time.Hour,
		"1d12h": 36 * time.Hour,
		"90m":   90 * time.Minute,
		"1h30m": 90 * time.Minute,
		"45s":   45 * time.Second,
		"48h":   48 * time.Hour,
		"1.5d":  36 * time.Hour,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParse_Rejects(t *testing.T) {
	for _, bad := range []string{"", "abc", "7", "d7", "  ", "12x"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) = nil error, want rejection", bad)
		}
	}
}
