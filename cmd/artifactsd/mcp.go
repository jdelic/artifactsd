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
	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// runMCP starts the MCP adapter: an HTTP server exposing the configured tools
// over Streamable HTTP (design/11 §1), calling the artifactsd management API
// through pkg/apiclient. It runs until SIGINT/SIGTERM triggers a graceful
// shutdown, mirroring runServe.
func runMCP(parent context.Context, m *config.MCP, stderr io.Writer) error {
	log := audit.NewAppLogger(stderr, m.LogLevel)

	client := apiclient.New(apiclient.Config{BaseURL: m.BaseURL, Token: m.Token})
	srv := mcp.New(mcp.Config{Client: client, BaseURL: m.BaseURL})

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
