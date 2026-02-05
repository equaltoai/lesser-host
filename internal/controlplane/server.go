package controlplane

import (
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

// Server implements the control plane API.
type Server struct {
	cfg      config.Config
	store    *store.Store
	webAuthn webAuthnEngine
	queues   *queueClient
}

// NewServer constructs a new control plane Server.
func NewServer(cfg config.Config, st *store.Store) *Server {
	webAuthn, _ := newWebAuthnEngine(cfg)
	return &Server{
		cfg:      cfg,
		store:    st,
		webAuthn: webAuthn,
		queues:   newQueueClient(cfg.ProvisionQueueURL),
	}
}

// RegisterRoutes registers HTTP routes for the control plane API.
func (s *Server) RegisterRoutes(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	// Wallet authentication (public).
	app.Post("/auth/wallet/challenge", s.handleWalletChallenge)
	app.Post("/auth/wallet/login", s.handleWalletLogin)

	// WebAuthn (passkey) authentication.
	app.Post("/api/v1/auth/webauthn/register/begin", s.handleWebAuthnRegisterBegin, apptheory.RequireAuth())
	app.Post("/api/v1/auth/webauthn/register/finish", s.handleWebAuthnRegisterFinish, apptheory.RequireAuth())
	app.Post("/api/v1/auth/webauthn/login/begin", s.handleWebAuthnLoginBegin)
	app.Post("/api/v1/auth/webauthn/login/finish", s.handleWebAuthnLoginFinish)
	app.Get("/api/v1/auth/webauthn/credentials", s.handleWebAuthnCredentials, apptheory.RequireAuth())
	app.Delete("/api/v1/auth/webauthn/credentials/{credentialId}", s.handleWebAuthnDeleteCredential, apptheory.RequireAuth())
	app.Put("/api/v1/auth/webauthn/credentials/{credentialId}", s.handleWebAuthnUpdateCredential, apptheory.RequireAuth())

	// Setup (bootstrap-only) endpoints.
	app.Get("/setup/status", s.handleSetupStatus)
	app.Post("/setup/bootstrap/challenge", s.handleSetupBootstrapChallenge)
	app.Post("/setup/bootstrap/verify", s.handleSetupBootstrapVerify)
	app.Post("/setup/admin", s.handleSetupCreateAdmin)
	app.Post("/setup/finalize", s.handleSetupFinalize, apptheory.RequireAuth())

	// Operator identity helpers.
	app.Get("/api/v1/operators/me", s.handleOperatorMe, apptheory.RequireAuth())

	// Instance registry + billing primitives (admin-only).
	app.Post("/api/v1/instances", s.handleCreateInstance, apptheory.RequireAuth())
	app.Get("/api/v1/instances", s.handleListInstances, apptheory.RequireAuth())
	app.Put("/api/v1/instances/{slug}/config", s.handleUpdateInstanceConfig, apptheory.RequireAuth())
	app.Post("/api/v1/instances/{slug}/keys", s.handleCreateInstanceKey, apptheory.RequireAuth())
	app.Put("/api/v1/instances/{slug}/budgets/{month}", s.handleSetInstanceBudgetMonth, apptheory.RequireAuth())
	app.Get("/api/v1/instances/{slug}/usage/{month}", s.handleListInstanceUsage, apptheory.RequireAuth())
	app.Post("/api/v1/instances/{slug}/provision", s.handleStartInstanceProvisioning, apptheory.RequireAuth())
	app.Get("/api/v1/instances/{slug}/provision", s.handleGetInstanceProvisioning, apptheory.RequireAuth())

	// Domains (admin-only).
	app.Get("/api/v1/instances/{slug}/domains", s.handleListInstanceDomains, apptheory.RequireAuth())
	app.Post("/api/v1/instances/{slug}/domains", s.handleAddInstanceDomain, apptheory.RequireAuth())
	app.Post("/api/v1/instances/{slug}/domains/{domain}/verify", s.handleVerifyInstanceDomain, apptheory.RequireAuth())
	app.Delete("/api/v1/instances/{slug}/domains/{domain}", s.handleDeleteInstanceDomain, apptheory.RequireAuth())

	// Tip registry (public registration flow + admin reconciliation).
	app.Post("/api/v1/tip-registry/registrations/begin", s.handleTipHostRegistrationBegin)
	app.Post("/api/v1/tip-registry/registrations/{id}/verify", s.handleTipHostRegistrationVerify)
	app.Get("/api/v1/tip-registry/operations", s.handleListTipRegistryOperations, apptheory.RequireAuth())
	app.Get("/api/v1/tip-registry/operations/{id}", s.handleGetTipRegistryOperation, apptheory.RequireAuth())
	app.Post("/api/v1/tip-registry/operations/{id}/record-execution", s.handleRecordTipRegistryOperationExecution, apptheory.RequireAuth())
	app.Post("/api/v1/tip-registry/hosts/{domain}/active", s.handleSetTipRegistryHostActive, apptheory.RequireAuth())
	app.Post("/api/v1/tip-registry/hosts/{domain}/ensure", s.handleEnsureTipRegistryHost, apptheory.RequireAuth())
	app.Post("/api/v1/tip-registry/tokens/allowlist", s.handleSetTipRegistryTokenAllowed, apptheory.RequireAuth())
}
