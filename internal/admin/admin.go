// Package admin implements the server-rendered admin web UI under /admin
// (design/09): html/template + htmx over the same service layer the
// management API uses, with templates and static assets embedded via
// embed.FS so the binary stays self-contained.
package admin

import (
	"context"
	"html/template"
	"log/slog"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// Cleaner runs a single on-demand maintenance/cleanup pass. Decoupled from
// the janitor package the same way internal/api.Cleaner is, so this package
// doesn't import internal/janitor directly.
type Cleaner interface {
	RunOnce(ctx context.Context) (any, error)
}

// Deps are the handler's dependencies.
type Deps struct {
	Store   *store.Store
	Config  *config.Config
	Audit   *audit.Recorder
	Cleaner Cleaner
	Session *session.Manager
	Logger  *slog.Logger
}

// Handler serves the admin web UI.
type Handler struct {
	store   *store.Store
	cfg     *config.Config
	audit   *audit.Recorder
	cleaner Cleaner
	session *session.Manager
	log     *slog.Logger
	tmpl    *template.Template
}

// New builds the admin UI handler, parsing the embedded templates once.
func New(d Deps) *Handler { //nolint:gocritic // Deps is built once at construction
	return &Handler{
		store:   d.Store,
		cfg:     d.Config,
		audit:   d.Audit,
		cleaner: d.Cleaner,
		session: d.Session,
		log:     d.Logger,
		tmpl:    parseTemplates(),
	}
}
