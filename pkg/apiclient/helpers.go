package apiclient

import "net/url"

// encodeQuery renders query values as a "?..." suffix, or "" when empty.
func encodeQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}
