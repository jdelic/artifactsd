package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type observingWriter struct {
	*httptest.ResponseRecorder
	beforeWrite func()
}

func (w *observingWriter) WriteHeader(code int) {
	w.beforeWrite()
	w.ResponseRecorder.WriteHeader(code)
}

func (w *observingWriter) Write(p []byte) (int, error) {
	w.beforeWrite()
	return w.ResponseRecorder.Write(p)
}

func (w *observingWriter) Flush() {
	w.beforeWrite()
	w.ResponseRecorder.Flush()
}

func TestAdapter_SessionReadyBeforeResponse(t *testing.T) {
	for _, mode := range []string{"header", "write", "flush"} {
		t.Run(mode, func(t *testing.T) {
			a := New(Config{})
			a.sdk = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Mcp-Session-Id", "session")
				switch mode {
				case "header":
					w.WriteHeader(http.StatusOK)
				case "write":
					_, _ = w.Write([]byte("response"))
				case "flush":
					w.(http.Flusher).Flush()
				}
			})
			observed := false
			w := &observingWriter{
				ResponseRecorder: httptest.NewRecorder(),
				beforeWrite: func() {
					observed = true
					if !a.sessionAllows("session", hashToken("owner")) {
						t.Error("owner cannot use the session when response becomes visible")
					}
					if a.sessionAllows("session", hashToken("other")) {
						t.Error("session accepts a different token when response becomes visible")
					}
				},
			}
			r := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
			r.Header.Set("Authorization", "Bearer owner")
			a.Handler().ServeHTTP(w, r)
			if !observed {
				t.Fatal("response was not observed")
			}
		})
	}
}

func TestAdapter_SessionCapacity(t *testing.T) {
	a := New(Config{})
	a.bind("old", hashToken("owner"))
	b := a.sessions["old"]
	b.lastSeen = time.Now().Add(-time.Minute)
	a.sessions["old"] = b
	for i := range maxSessions {
		a.bind(fmt.Sprintf("session-%d", i), hashToken("other"))
	}
	if len(a.sessions) != maxSessions {
		t.Fatalf("bindings = %d, want %d", len(a.sessions), maxSessions)
	}
	for _, sid := range []string{"old", "unknown"} {
		if a.sessionAllows(sid, hashToken("owner")) {
			t.Errorf("session %q accepted without a current binding", sid)
		}
	}
	if !a.sessionAllows("session-0", hashToken("other")) {
		t.Error("retained session rejected its owner")
	}
}
