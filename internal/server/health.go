package server

import (
	"context"
	"net/http"
	"time"
)

// handleHealthz is a liveness probe: the process is up (design/01 §7).
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

// handleReadyz is a readiness probe: the database is reachable. The data dir was
// validated writable at startup (config.Validate).
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writePlain(w, http.StatusServiceUnavailable, "no database")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.SQL().PingContext(ctx); err != nil {
		s.log.Warn("readiness check failed", "error", err)
		writePlain(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	writePlain(w, http.StatusOK, "ready")
}

func writePlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
