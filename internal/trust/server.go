package trust

import (
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/attestations"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"sync"
)

// Server implements the trust API.
type Server struct {
	cfg       config.Config
	store     *store.Store
	artifacts *artifacts.Store
	queues    *queueClient
	attest    *attestations.KMSService
	ai        *ai.Service

	ensSignerOnce sync.Once
	ensSigner     ensGatewaySigner
	ensSignerErr  error
	ensCache      *ensGatewayCache
}

// NewServer constructs a new trust Server.
func NewServer(cfg config.Config, st *store.Store) *Server {
	return &Server{
		cfg:       cfg,
		store:     st,
		artifacts: artifacts.New(cfg.ArtifactBucketName),
		queues:    newQueueClient(cfg.PreviewQueueURL, cfg.SafetyQueueURL),
		attest:    attestations.NewKMSService(cfg.AttestationSigningKeyID, cfg.AttestationPublicKeyIDs),
		ai:        ai.NewService(st),
		ensCache:  &ensGatewayCache{},
	}
}

// RegisterRoutes registers HTTP routes for the trust API.
func (s *Server) RegisterRoutes(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	// ENS gateway (public, CCIP-Read).
	app.Get("/health", s.handleENSGatewayHealth)
	app.Get("/resolve", s.handleENSGatewayResolve)

	// Attestations (public, cacheable).
	app.Get("/.well-known/jwks.json", s.handleWellKnownJWKS)
	app.Get("/attestations", s.handleLookupAttestation)
	app.Get("/attestations/{id}", s.handleGetAttestation)

	// Render artifacts.
	app.Post("/api/v1/renders", s.handleCreateRender, apptheory.RequireAuth())
	app.Get("/api/v1/renders/{renderId}", s.handleGetRender, apptheory.RequireAuth())
	app.Get("/api/v1/renders/{renderId}/thumbnail", s.handleGetRenderThumbnail, apptheory.RequireAuth())
	app.Get("/api/v1/renders/{renderId}/snapshot", s.handleGetRenderSnapshot, apptheory.RequireAuth())

	// Link previews.
	app.Post("/api/v1/previews", s.handleLinkPreview, apptheory.RequireAuth())
	app.Get("/api/v1/previews/{id}", s.handleGetLinkPreview, apptheory.RequireAuth())
	app.Get("/api/v1/previews/images/{imageId}", s.handleGetLinkPreviewImage, apptheory.RequireAuth())

	// Publish-triggered jobs (link safety, etc).
	app.Post("/api/v1/publish/jobs", s.handlePublishJob, apptheory.RequireAuth())
	app.Get("/api/v1/publish/jobs/{jobId}", s.handleGetPublishJob, apptheory.RequireAuth())

	// AI tool evidence (cheap, cached).
	app.Post("/api/v1/ai/evidence/text", s.handleAIEvidenceText, apptheory.RequireAuth())
	app.Post("/api/v1/ai/evidence/image", s.handleAIEvidenceImage, apptheory.RequireAuth())
	app.Post("/api/v1/ai/moderation/text", s.handleAIModerationText, apptheory.RequireAuth())
	app.Post("/api/v1/ai/moderation/image", s.handleAIModerationImage, apptheory.RequireAuth())
	app.Post("/api/v1/ai/moderation/text/report", s.handleAIModerationTextReport, apptheory.RequireAuth())
	app.Post("/api/v1/ai/moderation/image/report", s.handleAIModerationImageReport, apptheory.RequireAuth())
	app.Post("/api/v1/ai/claims/verify", s.handleAIClaimVerify, apptheory.RequireAuth())
	app.Get("/api/v1/ai/jobs/{jobId}", s.handleGetAIJob, apptheory.RequireAuth())

	app.Post("/api/v1/budget/debit", s.handleBudgetDebit, apptheory.RequireAuth())
}
