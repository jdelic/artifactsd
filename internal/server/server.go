// Package server wires the artifactsd HTTP server: the http.Server, the route
// mux, the middleware stack, TLS, and graceful shutdown (design/01 §2-3).
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/ratelimit"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// shutdownGrace is how long in-flight requests get to drain on shutdown.
const shutdownGrace = 25 * time.Second

// Options configures a Server.
type Options struct {
	Config *config.Config
	Logger *slog.Logger
	Access *audit.AccessLogger
	DB     *store.DB
	// Handler overrides the default route mux (mainly for tests). When nil, the
	// default handler (health endpoints + route-group stubs) wrapped in the
	// middleware stack is used.
	Handler http.Handler
	// OIDCCache overrides the OIDC provider cache (tests inject one with a mock
	// issuer's HTTP client). When nil a default cache is created.
	OIDCCache *auth.OIDCProviderCache
}

// Server is the artifactsd HTTP server.
type Server struct {
	cfg     *config.Config
	log     *slog.Logger
	access  *audit.AccessLogger
	db      *store.DB
	limiter *ratelimit.Limiter
	http    *http.Server

	oidcCache *auth.OIDCProviderCache
	session   *session.Manager
	// adminBasicHash is the argon2id hash for the basic admin fallback (hashed
	// from a cleartext password at startup if only the cleartext was provided).
	adminBasicHash string

	oidcLoginOnce sync.Once
	oidcLogin     *auth.OIDCLogin
	oidcLoginErr  error
}

// New builds a Server. The HTTP handler is the provided Handler, or the default
// mux wrapped in the middleware stack.
func New(opts Options) *Server {
	s := &Server{
		cfg:       opts.Config,
		log:       opts.Logger,
		access:    opts.Access,
		db:        opts.DB,
		limiter:   ratelimit.New(opts.Config.Limits.AuthFailMax, opts.Config.Limits.AuthFailWindow),
		oidcCache: opts.OIDCCache,
	}
	if s.oidcCache == nil {
		s.oidcCache = auth.NewOIDCProviderCache(nil)
	}
	s.initAdminAuth()
	handler := opts.Handler
	if handler == nil {
		handler = s.Middleware(s.routes())
	}
	s.http = &http.Server{
		Addr:              opts.Config.Server.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return s
}

// Run starts the listener and blocks until ctx is canceled (e.g. SIGINT/
// SIGTERM), then gracefully drains in-flight requests. The TLS configuration is
// applied per config (design/06 §6).
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening",
			"addr", s.http.Addr, "tls", string(s.cfg.TLS.Mode), "base_url", s.cfg.Server.BaseURL)
		errCh <- s.listenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return s.shutdown()
	}
}

// shutdown drains in-flight requests within the grace period, then closes the DB.
func (s *Server) shutdown() error {
	s.log.Info("shutting down", "grace", shutdownGrace.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	err := s.http.Shutdown(shutdownCtx)
	if err != nil {
		s.log.Error("graceful shutdown timed out; forcing close", "error", err)
		_ = s.http.Close()
	}
	if s.db != nil {
		if cerr := s.db.Close(); cerr != nil {
			s.log.Error("closing database", "error", cerr)
		}
	}
	s.log.Info("shutdown complete")
	return err
}

// Addr returns the configured listen address.
func (s *Server) Addr() string { return s.http.Addr }
