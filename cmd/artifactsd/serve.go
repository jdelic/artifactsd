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
