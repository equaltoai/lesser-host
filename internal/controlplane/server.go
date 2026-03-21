package controlplane

import (
	"context"
	"log"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

type soulPackStore interface {
	PutObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error
	GetObject(ctx context.Context, key string, maxBytes int64) ([]byte, string, string, error)
}

// Server implements the control plane API.
type Server struct {
	cfg       config.Config
	store     *store.Store
	webAuthn  webAuthnEngine
	queues    *queueClient
	r53       *route53Client
	soulPacks soulPackStore
	dialEVM   ethRPCDialer

	ssmGetParameter   func(ctx context.Context, name string) (string, error)
	ssmPutSecureValue func(ctx context.Context, name string, value string, overwrite bool) error
	migaduCreateEmail func(ctx context.Context, localPart string, name string, password string) error
	migaduForwarding  func(ctx context.Context, localPart string, address string) error
	migaduDeleteEmail func(ctx context.Context, localPart string) error
	migaduSendSMTP    func(ctx context.Context, username string, password string, from string, recipients []string, data []byte) error
	telnyxSearchNums  func(ctx context.Context, countryCode string, limit int) ([]string, error)
	telnyxOrderNumber func(ctx context.Context, phoneNumber string) (string, error)
	telnyxRelease     func(ctx context.Context, phoneNumber string) error
	telnyxSendSMS     func(ctx context.Context, from string, to string, text string) (string, error)
	telnyxCallVoice   func(ctx context.Context, from string, to string, texmlURL string, statusCallbackURL string) (string, error)

	enqueueCommMessage func(ctx context.Context, msg commworker.QueueMessage) error
}

// NewServer constructs a new control plane Server.
func NewServer(cfg config.Config, st *store.Store) *Server {
	webAuthn, err := newWebAuthnEngine(cfg)
	if err != nil {
		log.Printf("controlplane: webauthn disabled: %v", err)
	}
	srv := &Server{
		cfg:       cfg,
		store:     st,
		webAuthn:  webAuthn,
		queues:    newQueueClient(cfg.ProvisionQueueURL, cfg.CommQueueURL),
		r53:       newRoute53Client(),
		soulPacks: artifacts.New(cfg.SoulPackBucketName),
		dialEVM: func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
			return dialEthClient(ctx, rpcURL)
		},
		ssmGetParameter:   defaultSSMGetParameter,
		ssmPutSecureValue: defaultSSMPutSecureString,
		migaduCreateEmail: defaultMigaduCreateMailbox,
		migaduForwarding:  defaultMigaduCreateForwarding,
		migaduDeleteEmail: defaultMigaduDeleteMailbox,
		migaduSendSMTP:    defaultMigaduSendSMTP,
		telnyxSearchNums:  defaultTelnyxSearchAvailablePhoneNumbers,
		telnyxOrderNumber: defaultTelnyxOrderPhoneNumber,
		telnyxRelease:     defaultTelnyxReleasePhoneNumber,
		telnyxSendSMS:     defaultTelnyxSendSMS,
		telnyxCallVoice:   defaultTelnyxCreateVoiceCall,
	}
	srv.enqueueCommMessage = srv.queues.enqueueCommMessage
	return srv
}

// RegisterRoutes registers HTTP routes for the control plane API.
func (s *Server) RegisterRoutes(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	// Wallet authentication (public).
	app.Post("/auth/wallet/challenge", s.handleWalletChallenge)
	app.Post("/auth/wallet/login", s.handleWalletLogin)

	// Portal wallet authentication (public, self-serve).
	app.Post("/api/v1/portal/auth/wallet/challenge", s.handlePortalWalletChallenge)
	app.Post("/api/v1/portal/auth/wallet/login", s.handlePortalWalletLogin)

	// WebAuthn (passkey) authentication.
	app.Post("/api/v1/auth/webauthn/register/begin", s.handleWebAuthnRegisterBegin, apptheory.RequireAuth())
	app.Post("/api/v1/auth/webauthn/register/finish", s.handleWebAuthnRegisterFinish, apptheory.RequireAuth())
	app.Post("/api/v1/auth/webauthn/login/begin", s.handleWebAuthnLoginBegin)
	app.Post("/api/v1/auth/webauthn/login/finish", s.handleWebAuthnLoginFinish)
	app.Get("/api/v1/auth/webauthn/credentials", s.handleWebAuthnCredentials, apptheory.RequireAuth())
	app.Delete("/api/v1/auth/webauthn/credentials/{credentialId}", s.handleWebAuthnDeleteCredential, apptheory.RequireAuth())
	app.Put("/api/v1/auth/webauthn/credentials/{credentialId}", s.handleWebAuthnUpdateCredential, apptheory.RequireAuth())
	app.Post("/api/v1/auth/logout", s.handleAuthLogout, apptheory.RequireAuth())

	// Setup (bootstrap-only) endpoints.
	app.Get("/setup/status", s.handleSetupStatus)
	app.Post("/setup/bootstrap/challenge", s.handleSetupBootstrapChallenge)
	app.Post("/setup/bootstrap/verify", s.handleSetupBootstrapVerify)
	app.Post("/setup/admin", s.handleSetupCreateAdmin)
	app.Post("/setup/finalize", s.handleSetupFinalize, apptheory.RequireAuth())

	// Operator identity helpers.
	app.Get("/api/v1/operators/me", s.handleOperatorMe, apptheory.RequireAuth())

	// Operator console (approvals/review).
	app.Get("/api/v1/operators/vanity-domain-requests", s.handleListVanityDomainRequests, apptheory.RequireAuth())
	app.Post("/api/v1/operators/vanity-domain-requests/{domain}/approve", s.handleApproveVanityDomainRequest, apptheory.RequireAuth())
	app.Post("/api/v1/operators/vanity-domain-requests/{domain}/reject", s.handleRejectVanityDomainRequest, apptheory.RequireAuth())
	app.Get("/api/v1/operators/external-instances/registrations", s.handleListExternalInstanceRegistrations, apptheory.RequireAuth())
	app.Post("/api/v1/operators/external-instances/registrations/{username}/{id}/approve", s.handleApproveExternalInstanceRegistration, apptheory.RequireAuth())
	app.Post("/api/v1/operators/external-instances/registrations/{username}/{id}/reject", s.handleRejectExternalInstanceRegistration, apptheory.RequireAuth())
	app.Get("/api/v1/operators/portal-users", s.handleListPortalUserApprovals, apptheory.RequireAuth())
	app.Post("/api/v1/operators/portal-users/{username}/approve", s.handleApprovePortalUser, apptheory.RequireAuth())
	app.Post("/api/v1/operators/portal-users/{username}/reject", s.handleRejectPortalUser, apptheory.RequireAuth())
	app.Get("/api/v1/operators/provisioning/jobs", s.handleListOperatorProvisionJobs, apptheory.RequireAuth())
	app.Get("/api/v1/operators/provisioning/jobs/{id}", s.handleGetOperatorProvisionJob, apptheory.RequireAuth())
	app.Post("/api/v1/operators/provisioning/jobs/{id}/retry", s.handleRetryOperatorProvisionJob, apptheory.RequireAuth())
	app.Post("/api/v1/operators/provisioning/jobs/{id}/adopt", s.handleAdoptOperatorProvisionJobAccount, apptheory.RequireAuth())
	app.Post("/api/v1/operators/provisioning/jobs/{id}/note", s.handleAppendOperatorProvisionJobNote, apptheory.RequireAuth())
	app.Get("/api/v1/operators/audit", s.handleListOperatorAuditLog, apptheory.RequireAuth())

	// Portal identity helpers.
	app.Get("/api/v1/portal/me", s.handlePortalMe, apptheory.RequireAuth())

	// Portal instance management (owner-scoped).
	app.Post("/api/v1/portal/instances", s.handlePortalCreateInstance, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances", s.handlePortalListInstances, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances/{slug}", s.handlePortalGetInstance, apptheory.RequireAuth())
	app.Put("/api/v1/portal/instances/{slug}/config", s.handlePortalUpdateInstanceConfig, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/provision/consent/challenge", s.handlePortalProvisionConsentChallenge, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/provision", s.handlePortalStartInstanceProvisioning, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances/{slug}/provision", s.handlePortalGetInstanceProvisioning, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/updates", s.handlePortalCreateInstanceUpdateJob, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances/{slug}/updates", s.handlePortalListInstanceUpdateJobs, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances/{slug}/budgets", s.handlePortalListInstanceBudgets, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances/{slug}/budgets/{month}", s.handlePortalGetInstanceBudgetMonth, apptheory.RequireAuth())
	app.Put("/api/v1/portal/instances/{slug}/budgets/{month}", s.handlePortalSetInstanceBudgetMonth, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances/{slug}/usage/{month}", s.handlePortalListInstanceUsage, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances/{slug}/usage/{month}/summary", s.handlePortalGetInstanceUsageSummary, apptheory.RequireAuth())

	// Portal domains (owner-scoped).
	app.Get("/api/v1/portal/instances/{slug}/domains", s.handlePortalListInstanceDomains, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/domains", s.handlePortalAddInstanceDomain, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/domains/{domain}/verify", s.handlePortalVerifyInstanceDomain, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/domains/{domain}/dns/route53", s.handlePortalUpsertDomainVerificationRoute53, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/domains/{domain}/rotate", s.handlePortalRotateInstanceDomain, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/domains/{domain}/disable", s.handlePortalDisableInstanceDomain, apptheory.RequireAuth())
	app.Delete("/api/v1/portal/instances/{slug}/domains/{domain}", s.handlePortalDeleteInstanceDomain, apptheory.RequireAuth())
	app.Get("/api/v1/portal/instances/{slug}/keys", s.handlePortalListInstanceKeys, apptheory.RequireAuth())
	app.Post("/api/v1/portal/instances/{slug}/keys", s.handlePortalCreateInstanceKey, apptheory.RequireAuth())
	app.Delete("/api/v1/portal/instances/{slug}/keys/{keyId}", s.handlePortalRevokeInstanceKey, apptheory.RequireAuth())

	// Portal external instance registrations.
	app.Post("/api/v1/portal/external-instances/registrations", s.handlePortalCreateExternalInstanceRegistration, apptheory.RequireAuth())
	app.Get("/api/v1/portal/external-instances/registrations", s.handlePortalListExternalInstanceRegistrations, apptheory.RequireAuth())

	// Portal billing.
	app.Post("/api/v1/portal/billing/credits/checkout", s.handlePortalCreateCreditsCheckout, apptheory.RequireAuth())
	app.Get("/api/v1/portal/billing/credits/purchases", s.handlePortalListCreditPurchases, apptheory.RequireAuth())
	app.Post("/api/v1/portal/billing/payment-method/checkout", s.handlePortalCreatePaymentMethodCheckout, apptheory.RequireAuth())
	app.Get("/api/v1/portal/billing/payment-methods", s.handlePortalListPaymentMethods, apptheory.RequireAuth())

	// Payments webhooks (public).
	app.Post("/api/v1/payments/stripe/webhook", s.handleStripeWebhook)

	// Communication provider webhooks (public).
	app.Post("/webhooks/comm/email/inbound", s.handleCommEmailInboundWebhook)
	app.Post("/webhooks/comm/sms/inbound", s.handleCommSMSInboundWebhook)
	app.Post("/webhooks/comm/voice/inbound", s.handleCommVoiceInboundWebhook)
	app.Post("/webhooks/comm/voice/status", s.handleCommVoiceStatusWebhook)
	app.Post("/webhooks/comm/voice/status/{messageId}", s.handleCommVoiceStatusWebhook)
	app.Get("/webhooks/comm/voice/gather/{messageId}", s.handleCommVoiceGatherWebhook)
	app.Post("/webhooks/comm/voice/gather/{messageId}", s.handleCommVoiceGatherWebhook)
	app.Get("/webhooks/comm/voice/texml/{messageId}", s.handleCommVoiceTeXML)
	app.Post("/webhooks/comm/voice/texml/{messageId}", s.handleCommVoiceTeXML)

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
	app.Get("/api/v1/tip-registry/config", s.handleTipRegistryConfig)
	app.Post("/api/v1/tip-registry/registrations/begin", s.handleTipHostRegistrationBegin)
	app.Post("/api/v1/tip-registry/registrations/{id}/verify", s.handleTipHostRegistrationVerify)
	app.Get("/api/v1/tip-registry/operations", s.handleListTipRegistryOperations, apptheory.RequireAuth())
	app.Get("/api/v1/tip-registry/operations/{id}", s.handleGetTipRegistryOperation, apptheory.RequireAuth())
	app.Post("/api/v1/tip-registry/operations/{id}/record-execution", s.handleRecordTipRegistryOperationExecution, apptheory.RequireAuth())
	app.Post("/api/v1/tip-registry/hosts/{domain}/active", s.handleSetTipRegistryHostActive, apptheory.RequireAuth())
	app.Post("/api/v1/tip-registry/hosts/{domain}/ensure", s.handleEnsureTipRegistryHost, apptheory.RequireAuth())
	app.Post("/api/v1/tip-registry/tokens/allowlist", s.handleSetTipRegistryTokenAllowed, apptheory.RequireAuth())

	// Soul registry (public config + portal registration flow).
	app.Get("/api/v1/soul/config", s.handleSoulConfig)
	app.Get("/api/v1/soul/agents/mine", s.handleSoulListMyAgents, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}", s.handleSoulPublicGetAgent)
	app.Get("/api/v1/soul/agents/{agentId}/registration", s.handleSoulPublicGetRegistration)
	app.Get("/api/v1/soul/agents/{agentId}/channels", s.handleSoulPublicGetAgentChannels)
	app.Get("/api/v1/soul/agents/{agentId}/channels/preferences", s.handleSoulPublicGetAgentChannelPreferences)
	app.Put("/api/v1/soul/agents/{agentId}/channels/preferences", s.handleSoulUpdateAgentChannelPreferences, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/reputation", s.handleSoulPublicGetReputation)
	app.Get("/api/v1/soul/agents/{agentId}/validations", s.handleSoulPublicGetValidations)
	app.Post("/api/v1/soul/agents/{agentId}/validations/challenges", s.handleSoulIssueValidationChallenge, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/validations/challenges/{challengeId}/response", s.handleSoulRecordValidationResponse, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/validations/challenges/{challengeId}/evaluate", s.handleSoulEvaluateValidationChallenge, apptheory.RequireAuth())
	app.Get("/api/v1/soul/resolve/ens/{ensName}", s.handleSoulPublicResolveENSName)
	app.Get("/api/v1/soul/resolve/email/{emailAddress}", s.handleSoulPublicResolveEmail)
	app.Get("/api/v1/soul/resolve/phone/{phoneNumber}", s.handleSoulPublicResolvePhone)
	app.Get("/api/v1/soul/search", s.handleSoulPublicSearch)
	app.Post("/api/v1/soul/comm/send", s.handleSoulCommSend)
	app.Get("/api/v1/soul/comm/status/{messageId}", s.handleSoulCommStatus)
	app.Get("/api/v1/soul/agents/{agentId}/comm/activity", s.handleSoulAgentCommActivity, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/comm/queue", s.handleSoulAgentCommQueue, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/comm/status/{messageId}", s.handleSoulAgentCommStatus, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/register/begin", s.handleSoulAgentRegistrationBegin, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/register/{id}/verify", s.handleSoulAgentRegistrationVerify, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/rotate-wallet/begin", s.handleSoulAgentRotateWalletBegin, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/rotate-wallet/confirm", s.handleSoulAgentRotateWalletConfirm, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/rotate-wallet/operation", s.handleSoulAgentGetRotationOperation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/rotate-wallet/operation/record-execution", s.handleSoulAgentRecordRotationOperationExecution, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/update-registration", s.handleSoulAgentUpdateRegistration, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/mint-operation", s.handleSoulAgentGetMintOperation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/mint-operation/record-execution", s.handleSoulAgentRecordMintOperationExecution, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/channels/email/provision/begin", s.handleSoulBeginProvisionEmailChannel, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/channels/email/provision", s.handleSoulProvisionEmailChannel, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/channels/phone/provision/begin", s.handleSoulBeginProvisionPhoneChannel, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/channels/phone/provision", s.handleSoulProvisionPhoneChannel, apptheory.RequireAuth())
	app.Delete("/api/v1/soul/agents/{agentId}/channels/phone", s.handleSoulDeprovisionPhoneChannel, apptheory.RequireAuth())
	app.Get("/api/v1/soul/operations", s.handleListSoulOperations, apptheory.RequireAuth())
	app.Get("/api/v1/soul/operations/{id}", s.handleGetSoulOperation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/operations/{id}/record-execution", s.handleRecordSoulOperationExecution, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/capabilities", s.handleSoulPublicGetCapabilities)
	app.Get("/api/v1/soul/agents/{agentId}/boundaries", s.handleSoulPublicGetBoundaries)
	app.Post("/api/v1/soul/agents/{agentId}/boundaries/begin", s.handleSoulBeginAppendBoundary, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/boundaries", s.handleSoulAppendBoundary, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/suspend", s.handleSuspendSoulAgent, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/reinstate", s.handleReinstateSoulAgent, apptheory.RequireAuth())

	// v2: Continuity journal + version history (public read + portal write).
	app.Get("/api/v1/soul/agents/{agentId}/continuity", s.handleSoulPublicGetContinuity)
	app.Post("/api/v1/soul/agents/{agentId}/continuity", s.handleSoulAppendContinuity, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/versions", s.handleSoulPublicGetVersions)

	// v2: Sovereignty (self-suspend, self-reinstate, validation opt-in, disputes).
	app.Get("/api/v1/soul/agents/{agentId}/disputes", s.handleSoulPublicGetDisputes)
	app.Get("/api/v1/soul/agents/{agentId}/disputes/{disputeId}", s.handleSoulPublicGetDispute)
	app.Post("/api/v1/soul/agents/{agentId}/self-suspend", s.handleSoulSelfSuspend, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/self-reinstate", s.handleSoulSelfReinstate, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/validations/challenges/{challengeId}/opt-in", s.handleSoulValidationOptIn, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/dispute", s.handleSoulCreateDispute, apptheory.RequireAuth())

	// v2: Relationships (expanded model + trust queries).
	app.Get("/api/v1/soul/agents/{agentId}/relationships", s.handleSoulPublicGetRelationships)
	app.Post("/api/v1/soul/agents/{agentId}/relationships", s.handleSoulCreateRelationship, apptheory.RequireAuth())

	// v2: Lifecycle (archive + succession).
	app.Post("/api/v1/soul/agents/{agentId}/archive/begin", s.handleSoulArchiveAgentBegin, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/archive", s.handleSoulArchiveAgent, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/successor/begin", s.handleSoulDesignateSuccessorBegin, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/successor", s.handleSoulDesignateSuccessor, apptheory.RequireAuth())

	// v2: Minting conversation (LLM-assisted registration).
	app.Post("/api/v1/soul/agents/register/{id}/mint-conversation", s.handleSoulMintConversation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/complete", s.handleSoulCompleteMintConversation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize/begin", s.handleSoulBeginFinalizeMintConversation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize", s.handleSoulFinalizeMintConversation, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}", s.handleSoulGetMintConversation, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/mint-conversations", s.handleSoulAgentListMintConversations, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/mint-conversation", s.handleSoulAgentMintConversation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/complete", s.handleSoulAgentCompleteMintConversation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize/begin", s.handleSoulAgentBeginFinalizeMintConversation, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize", s.handleSoulAgentFinalizeMintConversation, apptheory.RequireAuth())
	app.Get("/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}", s.handleSoulAgentGetMintConversation, apptheory.RequireAuth())

	// v2: Transparency + Failures.
	app.Get("/api/v1/soul/agents/{agentId}/transparency", s.handleSoulPublicGetTransparency)
	app.Get("/api/v1/soul/agents/{agentId}/failures", s.handleSoulPublicGetFailures)
	app.Post("/api/v1/soul/agents/{agentId}/failures", s.handleSoulRecordFailure, apptheory.RequireAuth())
	app.Post("/api/v1/soul/agents/{agentId}/failures/recover", s.handleSoulRecordRecovery, apptheory.RequireAuth())

	app.Post("/api/v1/soul/reputation/publish", s.handleSoulPublishReputationRoot, apptheory.RequireAuth())
	app.Post("/api/v1/soul/validation/publish", s.handleSoulPublishValidationRoot, apptheory.RequireAuth())
}
