package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

func TestMapError_Nil(t *testing.T) {
	if err := MapError(nil); err != nil {
		t.Errorf("MapError(nil) = %v, want nil", err)
	}
}

func TestMapError_APIError(t *testing.T) {
	err := MapError(&apiclient.Error{Status: 404, Code: "not_found", Message: "collection does not exist"})
	if err == nil {
		t.Fatal("MapError = nil, want an error")
	}
	msg := err.Error()
	for _, want := range []string{"collection does not exist", "404", "not_found"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestMapError_APIError_NoMessage(t *testing.T) {
	err := MapError(&apiclient.Error{Status: 500, Code: "internal"})
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "internal") {
		t.Errorf("message = %v, want it to mention 500 and internal", err)
	}
}

func TestMapError_Generic(t *testing.T) {
	cause := errors.New("connection refused")
	err := MapError(cause)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("message = %v, want it to wrap the cause", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("MapError should wrap the original error so errors.Is still matches")
	}
}
