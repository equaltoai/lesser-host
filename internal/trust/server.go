package trust

import (
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

type Server struct {
	cfg   config.Config
	store *store.Store
}

func NewServer(cfg config.Config, st *store.Store) *Server {
	return &Server{
		cfg:   cfg,
		store: st,
	}
}

func (s *Server) RegisterRoutes(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	app.Post("/api/v1/budget/debit", s.handleBudgetDebit, apptheory.RequireAuth())
}

