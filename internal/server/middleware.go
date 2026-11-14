package server

import (
	"net/http"
	"net/netip"
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

// rateLimit throttles clients with too many recent auth failures (design/06
// §5). Only credentialed requests and login surfaces are affected: a key that
// is out of tokens gets 429 + Retry-After when the request carries an
// Authorization header or targets the admin UI or /auth/. Anonymous requests to
// anything else (public artifacts, shares, upload links, health checks) are
// never blocked, so a shared or spoofed client address cannot take the public
// site down. A failure is recorded only for a 401/403 that answered a request
// that presented credentials (see presentedCredentials).
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rateLimitKey(s.clientIP(r))
		if s.limitable(r) {
			if blocked, retry := s.limiter.RetryAfter(key); blocked {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
		}
		rr, ok := w.(*responseRecorder)
		if !ok {
			rr = &responseRecorder{ResponseWriter: w}
			w = rr
		}
		next.ServeHTTP(rr, r)
		if (rr.status == http.StatusUnauthorized || rr.status == http.StatusForbidden) && s.presentedCredentials(r) {
			s.limiter.RecordFailure(key)
		}
	})
}

// limitable reports whether the limiter may block r: it carries credentials or
// targets a login surface (the admin UI or /auth/).
func (s *Server) limitable(r *http.Request) bool {
	return r.Header.Get("Authorization") != "" || s.loginSurface(r.URL.Path)
}

// loginSurface reports whether path is the admin UI or an /auth/ endpoint.
func (s *Server) loginSurface(path string) bool {
	return s.isAdminPlane(path) || strings.HasPrefix(path, "/auth/")
}

// presentedCredentials reports whether a 401/403 for r is a credential failure
// worth counting: the request carried an Authorization header, or it hit a
// login surface with a cookie (an admin session that failed validation, a
// CSRF failure, or an OIDC flow cookie). A challenge to a request with no
// credentials at all is not a failure.
func (s *Server) presentedCredentials(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}
	return s.loginSurface(r.URL.Path) && len(r.Cookies()) > 0
}

// rateLimitKey maps a client address to its limiter key. IPv6 addresses are
// keyed by their /64, since one host usually controls a whole /64.
func rateLimitKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() || a.Is4In6() {
		return ip
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}

// clientIP returns the client IP for logging/rate-limiting. X-Forwarded-For
// is honored only from trusted proxies (design/06 §6, internal/clientip).
func (s *Server) clientIP(r *http.Request) string {
	if ip := s.proxies.ClientIPString(r); ip != "" {
		return ip
	}
	return r.RemoteAddr
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
