package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/server"
	"github.com/jdelic/artifactsd/internal/store"
)

// runServe opens storage, applies pending migrations (when auto-migrate is on),
// and runs the HTTP server until SIGINT/SIGTERM triggers graceful shutdown.
func runServe(parent context.Context, cfg *config.Config, stderr io.Writer) error {
	printBanner(stderr, cfg)
	log := audit.NewAppLogger(stderr, cfg.Server.LogLevel)
	access := audit.NewAccessLogger(os.Stdout)

	db, err := store.OpenInDir(parent, cfg.Server.DataDir)
	if err != nil {
		return err
	}
	// Ownership of db passes to the server, which closes it on shutdown.

	if cfg.Server.AutoMigrate {
		version, merr := db.Migrate(context.WithoutCancel(parent))
		if merr != nil {
			_ = db.Close()
			return merr
		}
		log.Info("migrations applied", "schema_version", version)
	}

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st := store.New(db).WithDefaultDeleteAfterCreation(cfg.Policy.DeleteAfterCreation)
	// Deferred post-migration work (e.g. recomputing expires_at after a change
	// to how it is derived) must finish before the janitor's first pass. Like
	// Migrate above it runs to completion even if a shutdown signal arrives
	// meanwhile: it is the tail of a migration, and a signal during startup is
	// a clean shutdown, not an error.
	if n, err := st.RunPendingMaintenance(context.WithoutCancel(parent), string(cfg.Policy.Visibility)); err != nil {
		_ = db.Close()
		return err
	} else if n > 0 {
		log.Info("recomputed denormalized columns", "rows", n)
	}
	if err := seedBootstrapToken(ctx, st, cfg.Bootstrap.Token, log); err != nil {
		_ = db.Close()
		return err
	}

	blobStore, err := blob.NewFSStore(cfg.Server.DataDir)
	if err != nil {
		_ = db.Close()
		return err
	}

	srv := server.New(server.Options{
		Config: cfg,
		Logger: log,
		Access: access,
		DB:     db,
		Store:  st,
		Blob:   blobStore,
	})
	return srv.Run(ctx)
}
