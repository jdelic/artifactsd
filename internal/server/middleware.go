package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
)

// Middleware wraps the handler in the server middleware stack, outermost first:
// recovery → request-id → access log → body limit → rate limit (design/01 §7,
// design/06 §5/§8). Recovery is outermost so it catches panics in everything;
// the access log wraps the rate limiter so a 429 is still logged.
func (s *Server) Middleware(next http.Handler) http.Handler {
	return s.recover(
		s.requestID(
			s.accessLog(
				s.bodyLimit(
					s.rateLimit(next),
				),
			),
		),
	)
}

// responseRecorder captures the status code and bytes written.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	if rr.status == 0 {
		rr.status = http.StatusOK
	}
	n, err := rr.ResponseWriter.Write(b)
	rr.bytes += int64(n)
	return n, err
}

// recover turns a panic into a 500 without leaking a stack trace to the client.
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic recovered",
					"error", rec, "path", r.URL.Path, "request_id", audit.RequestIDFromContext(r.Context()))
				if !headerWritten(w) {
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestID assigns a request ID (honoring an inbound X-Request-Id) and exposes
// it in the response and context.
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = audit.NewRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(audit.WithRequestID(r.Context(), id)))
	})
}

// accessLog emits one structured access event per request to stdout (decision F5).
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rr, r)
		if rr.status == 0 {
			rr.status = http.StatusOK
		}
		if s.access != nil {
			s.access.Log(&audit.AccessEvent{
				RequestID:  audit.RequestIDFromContext(r.Context()),
				Method:     r.Method,
				Path:       r.URL.Path,
				Status:     rr.status,
				Bytes:      rr.bytes,
				DurationMS: float64(time.Since(start).Microseconds()) / 1000.0,
				Remote:     s.clientIP(r),
				UserAgent:  r.UserAgent(),
				Collection: collectionFromPath(r.URL.Path),
			})
		}
	})
}

// bodyLimit caps request bodies as a safety net (design/06 §8). Upload handlers
// may impose their own (smaller) limits on top.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	maxBytes := s.cfg.Limits.MaxUploadSize
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && maxBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimit blocks clients with too many recent auth failures (design/06 §5):
// a blocked key gets 429 + Retry-After; a 401/403 outcome records a failure.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := s.clientIP(r)
		if blocked, retry := s.limiter.RetryAfter(key); blocked {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		rr, ok := w.(*responseRecorder)
		if !ok {
			rr = &responseRecorder{ResponseWriter: w}
			w = rr
		}
		next.ServeHTTP(rr, r)
		if rr.status == http.StatusUnauthorized || rr.status == http.StatusForbidden {
			s.limiter.RecordFailure(key)
		}
	})
}

// clientIP returns the client IP for logging/rate-limiting. Proxy headers are
// honored only when trust_proxy is set (artifactsd-7bf refines this).
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.Server.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, found := strings.Cut(xff, ","); found || first != "" {
				return strings.TrimSpace(first)
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// collectionFromPath extracts the collection name from a /c/{collection}/... path.
func collectionFromPath(p string) string {
	const prefix = "/c/"
	if !strings.HasPrefix(p, prefix) {
		return ""
	}
	rest := p[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// headerWritten reports whether the response status was already set (so recovery
// avoids a superfluous WriteHeader).
func headerWritten(w http.ResponseWriter) bool {
	rr, ok := w.(*responseRecorder)
	return ok && rr.status != 0
}
