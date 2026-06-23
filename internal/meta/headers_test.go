package meta

import (
	"reflect"
	"strings"
	"testing"
)

func TestIsUnsafeHeader(t *testing.T) {
	unsafe := []string{"Connection", "connection", "Transfer-Encoding", "content-length", "Host", "TE", "Proxy-Authorization"}
	for _, h := range unsafe {
		if !IsUnsafeHeader(h) {
			t.Errorf("IsUnsafeHeader(%q) = false, want true", h)
		}
	}
	safe := []string{"Cache-Control", "X-Custom", "Access-Control-Allow-Origin"}
	for _, h := range safe {
		if IsUnsafeHeader(h) {
			t.Errorf("IsUnsafeHeader(%q) = true, want false", h)
		}
	}
}

func TestRejectedHeaders(t *testing.T) {
	got := RejectedHeaders(map[string]string{
		"Cache-Control":  "no-store",
		"connection":     "keep-alive",
		"content-length": "10",
		"content-type":   "text/plain", // not rejected, just ignored
	})
	want := []string{"Connection", "Content-Length"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RejectedHeaders = %v, want %v", got, want)
	}
}

func TestFilterHeaders(t *testing.T) {
	got := FilterHeaders(map[string]string{
		"Cache-Control":     "no-store",
		"X-Custom":          "v",
		"Connection":        "close",
		"content-type":      "text/plain",
		"Transfer-Encoding": "chunked",
	})
	want := map[string]string{"Cache-Control": "no-store", "X-Custom": "v"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterHeaders = %v, want %v", got, want)
	}

	if FilterHeaders(nil) != nil {
		t.Error("FilterHeaders(nil) should be nil")
	}
	if FilterHeaders(map[string]string{"Connection": "x"}) != nil {
		t.Error("FilterHeaders of only-unsafe should be nil")
	}
}

func TestValidate_RejectsUnsafeHeaders(t *testing.T) {
	d, _ := FromJSON([]byte(`{"content":{"headers":{"Connection":"keep-alive","Cache-Control":"no-store"}}}`))
	err := d.Validate()
	if err == nil {
		t.Fatal("expected validation error for unsafe header")
	}
	if !strings.Contains(err.Error(), "Connection") {
		t.Errorf("error missing Connection: %v", err)
	}
}
