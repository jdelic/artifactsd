package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/mcp"
)

// runMCP starts the MCP adapter: an HTTP server exposing the tools over
// Streamable HTTP (design/11 §1), calling the artifactsd management API through
// pkg/apiclient with each client's own token (artifactsd-m9e.18). It runs until
// SIGINT/SIGTERM triggers a graceful shutdown, mirroring runServe.
func runMCP(parent context.Context, m *config.MCP, stderr io.Writer) error {
	log := audit.NewAppLogger(stderr, m.LogLevel)

	srv := mcp.New(mcp.Config{BaseURL: m.BaseURL, Logger: log})
	if !m.ListenIsLoopback() {
		log.Warn("mcp adapter listens beyond loopback and has no TLS of its own: client API tokens travel in clear text; terminate TLS in front of it",
			"addr", m.Listen)
	}

	httpSrv := &http.Server{Addr: m.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("mcp adapter listening", "addr", m.Listen, "base_url", m.BaseURL)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Info("mcp adapter shutting down")
		return httpSrv.Shutdown(context.Background())
	}
}
