package meta

import "time"

// ParseInstant parses an absolute RFC3339 timestamp (the expiry.expires_at form).
func ParseInstant(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
