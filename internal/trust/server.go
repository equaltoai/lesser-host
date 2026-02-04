package trust

import (
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

type Server struct {
	cfg       config.Config
	store     *store.Store
	artifacts *artifactStore
}

func NewServer(cfg config.Config, st *store.Store) *Server {
	return &Server{
		cfg:       cfg,
		store:     st,
		artifacts: newArtifactStore(cfg.ArtifactBucketName),
	}
}

func (s *Server) RegisterRoutes(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	// Link previews.
	app.Post("/api/v1/previews", s.handleLinkPreview, apptheory.RequireAuth())
	app.Get("/api/v1/previews/{id}", s.handleGetLinkPreview, apptheory.RequireAuth())
	app.Get("/api/v1/previews/images/{imageId}", s.handleGetLinkPreviewImage)

	app.Post("/api/v1/budget/debit", s.handleBudgetDebit, apptheory.RequireAuth())
}
