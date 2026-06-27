package server

import (
	"bytes"
	"testing"

	"github.com/jdelic/artifactsd/internal/session"
)

// TestIsAdmin_GroupModes covers the three ARTIFACTSD_ADMIN_GROUP modes
// (design/14 §7): unset = fail closed, "*" = any authenticated user, any other
// value = require that exact group.
func TestIsAdmin_GroupModes(t *testing.T) {
	id := &session.Identity{Subject: "u1", Groups: []string{"eng", "staff"}}
	cases := []struct {
		name  string
		group string
		want  bool
	}{
		{"unset fails closed", "", false},
		{"star admits any authenticated", "*", true},
		{"matching group", "staff", true},
		{"non-matching group", "admins", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			env := map[string]string{}
			if tc.group != "" {
				env["ARTIFACTSD_ADMIN_GROUP"] = tc.group
			}
			s := serverWithEnv(t, env, &buf)
			if got := s.isAdmin(id); got != tc.want {
				t.Errorf("isAdmin(ADMIN_GROUP=%q) = %v, want %v", tc.group, got, tc.want)
			}
			s.logAdminMode() // smoke: must not panic in any mode
		})
	}

	// "*" admits an authenticated identity that carries no groups at all.
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{"ARTIFACTSD_ADMIN_GROUP": "*"}, &buf)
	if !s.isAdmin(&session.Identity{Subject: "u2"}) {
		t.Error("ADMIN_GROUP=* should admit an authenticated user with no groups")
	}
}
