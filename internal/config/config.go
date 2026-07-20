package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	AppName string
	Stage   string

	StateTableName string

	// PublicBaseURL is the externally reachable origin (scheme + host) for this deployment,
	// used when generating absolute URLs in API responses. If empty, handlers should prefer
	// relative URLs or derive a best-effort base from request headers.
	PublicBaseURL string

	// SoulEmailInboundDomain is the SES-backed bridge domain that receives inbound mail
	// for soul email channels before it is normalized into comm-worker notifications.
	SoulEmailInboundDomain string

	// InboundEmailBucketName stores raw SES-received messages for the email ingress Lambda.
	InboundEmailBucketName string

	// InboundEmailS3Prefix is the prefix under InboundEmailBucketName where SES stores raw mail.
	InboundEmailS3Prefix string

	// SoulCommMailboxBucketName stores bounded canonical mailbox content for soul comm deliveries.
	SoulCommMailboxBucketName string

	// SoulCommMailboxRetentionDays is the content/object lifecycle window for mailbox bodies.
	SoulCommMailboxRetentionDays int64

	// CommWebhookSharedSecretSSMParam stores the HMAC shared secret for
	// first-party comm webhook adapters that cannot use provider-native
	// signatures.
	CommWebhookSharedSecretSSMParam string

	// TelnyxWebhookPublicKey is Telnyx's Ed25519 webhook verification public
	// key. It is public key material, not a secret; deployments may also store it
	// in the `/lesser-host/telnyx` JSON parameter as `webhook_public_key`.
	TelnyxWebhookPublicKey string

	// ENS gateway (CCIP-Read) configuration. These fields are independent from
	// legacy SoulRegistry chain/contract configuration.
	ENSGatewayEnabled             bool
	ENSGatewayChainID             int64
	ENSGatewayChainName           string
	ENSGatewayRootName            string
	ENSGatewayResolverAddress     string
	ENSGatewaySigningKeyID        string
	ENSGatewaySigningPrivateKey   string
	ENSGatewaySignatureTTLSeconds int64

	ArtifactBucketName    string
	PreviewQueueURL       string
	SafetyQueueURL        string
	HostedGenesisQueueURL string
	ProvisionQueueURL     string
	CommQueueURL          string

	BootstrapWalletAddress string

	AttestationSigningKeyID string
	AttestationPublicKeyIDs []string

	WebAuthnRPID    string
	WebAuthnOrigins []string

	// Tip registry (EVM).
	TipEnabled                  bool
	TipChainID                  int64
	TipRPCURL                   string
	TipRPCURLSSMParam           string
	TipContractAddress          string
	TipAdminSafeAddress         string
	TipDefaultHostWalletAddress string
	TipDefaultHostFeeBps        uint16
	TipTxMode                   string // safe|direct

	// Soul registry (EVM).
	SoulEnabled                              bool
	SoulChainID                              int64
	SoulRPCURL                               string
	SoulRPCURLSSMParam                       string
	SoulRegistryContractAddress              string
	SoulReputationAttestationContractAddress string
	SoulValidationAttestationContractAddress string
	SoulAdminSafeAddress                     string
	SoulTxMode                               string // safe|direct
	SoulSupportedCapabilities                []string
	SoulPackBucketName                       string
	SoulPackBucketNameSSMParam               string // optional override; default is /soul/<stage>/packBucketName
	SoulMintSignerKeySSMParam                string
	SoulMintSignerKey                        string
	SoulPublicCORSOrigins                    []string
	SoulPublicOnChainAvatarEnabled           bool // opt-in public tokenURI enrichment; default off to avoid EVM fanout on reads
	SoulV2StrictIntegrity                    bool // harden signature + artifact integrity checks

	// Soul reputation (v0).
	SoulReputationTipStartBlock       uint64
	SoulReputationTipBlockChunkSize   uint64
	SoulReputationTipScale            float64
	SoulReputationWeightEconomic      float64
	SoulReputationWeightSocial        float64
	SoulReputationWeightValidation    float64
	SoulReputationWeightTrust         float64
	SoulReputationWeightIntegrity     float64
	SoulReputationWeightCommunication float64

	// Soul validation (v0).
	SoulValidationDecayEpochHours int64
	SoulValidationDecayRate       float64

	// Managed hosting (M9 provisioning).
	ManagedProvisioningEnabled        bool
	ManagedOrgVendingRoleARN          string // optional; assume this role for Organizations + instance-account role assumptions
	ManagedParentDomain               string // e.g. greater.website
	ManagedParentHostedZoneID         string // Route53 hosted zone id for greater.website (central account)
	ManagedInstanceRoleName           string // role to assume into instance accounts
	ManagedTargetOrganizationalUnitID string // optional OU id for instance accounts
	ManagedAccountEmailTemplate       string // e.g. "lesser+{slug}@example.com"
	ManagedAccountNamePrefix          string // e.g. "lesser-"
	ManagedDefaultRegion              string // e.g. us-east-1
	ManagedLesserDefaultVersion       string // release tag or "latest", optional
	ManagedProvisionRunnerProjectName string // CodeBuild project name used to run lesser up
	ManagedProvisionRunnerRoleARN     string // CodeBuild service role allowed in per-instance assume-role trust
	ManagedLesserGitHubOwner          string // GitHub org/user for the lesser repo
	ManagedLesserGitHubRepo           string // GitHub repo name for lesser
	ManagedLesserGitHubTokenSSMParam  string // optional SSM param name for a GitHub token (CodeBuild)
	ManagedLesserBodyDefaultVersion   string // release tag or "latest", optional
	ManagedLesserBodyGitHubOwner      string // GitHub org/user for the lesser-body repo
	ManagedLesserBodyGitHubRepo       string // GitHub repo name for lesser-body

	// ManagedProvisionConsentEncryptionKeyHex is a hex-encoded 32-byte
	// AES-256 key used to encrypt provisioning consent payloads at rest
	// in the DynamoDB ProvisionJob record (CSR-017).
	ManagedProvisionConsentEncryptionKeyHex string

	// Payments (M10).
	PaymentsProvider            string // stripe|mock|none
	PaymentsCheckoutSuccessURL  string // redirect target after checkout completion
	PaymentsCheckoutCancelURL   string // redirect target after checkout cancel
	PaymentsCentsPer1000Credits int64  // pricing policy: cents per 1000 credits

	// HostedGenesisMicroVM configures the production AppTheory M16 MicroVM
	// dispatch path wired into controlplane.NewServer (P52 H1.5). When enabled
	// and complete, NewServer constructs an HTTPControllerDispatcher that drives
	// the governed AppTheoryMicrovmController HTTP API (POST /microvms to run,
	// GET /microvms/{session_id} to reconcile) and wires it onto the Server so
	// the hosted genesis accept path dispatches the controller run command and
	// returns 202 without a synchronous control-plane LLM call. When disabled or
	// incomplete, NewServer leaves the dispatcher unwired and the accept path
	// fails closed and loudly with a typed 503 microvm_unavailable (no sync LLM
	// fallback). The controller Lambda is the single governed surface; the
	// control plane never makes raw AWS RunMicrovm/GetMicrovm SDK calls or
	// touches the session registry directly — it only speaks HTTP to the
	// controller endpoint with an authorizer bearer token. The auth token is a
	// credential loaded at runtime from SSM (its parameter name from env), never
	// committed or logged; the endpoint + image/network refs are non-secret
	// CDK-provided env vars.
	HostedGenesisMicroVM HostedGenesisMicroVMConfig
}

const (
	// HostedGenesisMicroVMConfigJSONEnv is the compact, CDK-owned dispatcher
	// config envelope used on deployment Lambdas. It keeps the Lambda
	// environment below AWS's 4KB limit while still carrying the same AppTheory
	// run inputs (controller endpoint, auth-token source, image ref, connector
	// refs, MaximumDurationSeconds, and IdlePolicy). Legacy individual env vars
	// remain supported for local tests/tools.
	HostedGenesisMicroVMConfigJSONEnv = "HOSTED_GENESIS_MICROVM_CONFIG_JSON"

	// HostedGenesisMicroVMDefaultMaximumDurationSeconds caps one active
	// AppTheory MicroVM run for the longest provider turn plus in-VM
	// declaration extraction. Human wait time is handled by the explicit
	// ProviderIdlePolicy below, not by stretching the active-run ceiling.
	HostedGenesisMicroVMDefaultMaximumDurationSeconds int32 = 300
	// HostedGenesisMicroVMDefaultIdleMaxSeconds is the ready-idle interval
	// before the provider may suspend the conversation actor. It is long enough
	// for normal poll/retry jitter and short enough to force the lab human-gap
	// canary through the AppTheory suspend/resume/relaunch contract.
	HostedGenesisMicroVMDefaultIdleMaxSeconds int32 = 300
	// HostedGenesisMicroVMDefaultIdleSuspendedSeconds is deliberately shorter
	// than Host's one-hour AppTheory registry cache TTL so a suspended provider
	// session is not treated as durable business truth after Host's
	// HostedGenesisSession/reconstruction boundary would have to revalidate it.
	HostedGenesisMicroVMDefaultIdleSuspendedSeconds int32 = 1800
)

// HostedGenesisMicroVMIdlePolicyConfig is Host's env-friendly representation of
// AppTheory runtime/microvm.ProviderIdlePolicy. The dispatcher converts it at
// the controller boundary so config never carries raw provider SDK state.
type HostedGenesisMicroVMIdlePolicyConfig struct {
	AutoResumeEnabled        bool
	MaxIdleDurationSeconds   int32
	SuspendedDurationSeconds int32
}

// Complete reports whether the idle policy can be passed to AppTheory's
// ProviderIdlePolicy. AppTheory v1.17.0 rejects partial idle policies; Host
// treats a missing policy as incomplete deployed MicroVM config.
func (p HostedGenesisMicroVMIdlePolicyConfig) Complete() bool {
	return p.MaxIdleDurationSeconds > 0 && p.SuspendedDurationSeconds > 0
}

// HostedGenesisMicroVMConfig groups the AppTheory M16 MicroVM controller HTTP
// transport inputs controlplane.NewServer uses to construct the production
// dispatcher. ControllerEndpoint is the governed AppTheoryMicrovmController
// /microvms base URL. AuthTokenSSMParam names the SSM SecureString holding the
// authorizer bearer token (the authorizer's identity source); the raw token is
// loaded at runtime, never committed. ImageRef / NetworkConnectorRef /
// IngressConnectorRefs / EgressConnectorRefs are non-secret refs the control
// plane sends in the POST /microvms run body (the HTTP route handler does not
// fill them from env the way the in-process runtime did). MaximumDurationSeconds
// caps each dispatched run session duration. IdlePolicy is the explicit
// AppTheory human-gap policy for ready/suspended lifetime; Host does not
// emulate it with a local scheduler or step machine.
type HostedGenesisMicroVMConfig struct {
	Enabled                bool
	ControllerEndpoint     string
	AuthTokenSSMParam      string
	ImageRef               string
	NetworkConnectorRef    string
	IngressConnectorRefs   []string
	EgressConnectorRefs    []string
	MaximumDurationSeconds int32
	IdlePolicy             HostedGenesisMicroVMIdlePolicyConfig
}

// Complete reports whether the MicroVM dispatch config has the minimum required
// fields to construct an HTTPControllerDispatcher. NewServer uses this to decide
// between wiring the HTTP dispatcher and failing closed (never a silent sync LLM
// fallback). The auth token itself is fetched at construction time from the SSM
// parameter named here; Complete only requires the parameter name. A missing or
// empty token at fetch time fails the dispatcher construction loudly.
func (c HostedGenesisMicroVMConfig) Complete() bool {
	return c.Enabled && c.ControllerEndpoint != "" && c.AuthTokenSSMParam != "" && c.ImageRef != "" && c.NetworkConnectorRef != "" && c.IdlePolicy.Complete()
}

// Load reads environment variables and returns a Config with defaults applied.
func Load() Config {
	stage := envStringDefault("STAGE", "lab")
	stateTableName := envString("STATE_TABLE_NAME")
	publicBaseURL := envString("PUBLIC_BASE_URL")

	origins := parseCSV(envString("WEBAUTHN_ORIGINS"))
	publicKeyIDs := parseCSV(envString("ATTESTATION_PUBLIC_KEY_IDS"))

	tipsOn := envBoolOn("TIP_ENABLED")
	tipChainID := envInt64Positive("TIP_CHAIN_ID", 0)
	tipDefaultHostFeeBps := envUint16Max("TIP_DEFAULT_HOST_FEE_BPS", 0, 500)
	tipTxMode := envLowerStringDefault("TIP_TX_MODE", "safe")

	soulOn := envBoolOn("SOUL_ENABLED")
	soulChainID := envInt64Positive("SOUL_CHAIN_ID", 0)
	soulTxMode := envLowerStringDefault("SOUL_TX_MODE", "safe")
	soulCaps := parseCSV(envString("SOUL_SUPPORTED_CAPABILITIES"))
	soulPackBucketName := envString("SOUL_PACK_BUCKET_NAME")
	soulPublicCORSOrigins := parseCSV(envString("SOUL_PUBLIC_CORS_ORIGINS"))
	soulPublicOnChainAvatarEnabled := envBoolOn("SOUL_PUBLIC_ONCHAIN_AVATAR_ENABLED")
	soulV2StrictIntegrity := envBoolOn("SOUL_V2_STRICT_INTEGRITY")

	defaultENSGatewayChainID, defaultENSGatewayChainName := defaultENSGatewayChain(stage)
	ensGatewayEnabled := envBoolOn("ENS_GATEWAY_ENABLED")
	if envString("ENS_GATEWAY_ENABLED") == "" {
		// Backward-compatible migration: old deployments used SOUL_ENABLED to
		// expose the ENS gateway. CDK now emits ENS_GATEWAY_ENABLED explicitly,
		// so this fallback is only for hand-rolled/local environments that have
		// not been updated yet.
		ensGatewayEnabled = soulOn
	}
	ensGatewayTTL := envInt64Bounded("ENS_GATEWAY_TTL_SECONDS", 300, 30, 24*60*60)
	ensGatewayRootName := envLowerStringDefault("ENS_GATEWAY_ROOT_NAME", "lessersoul.eth")

	soulRepTipStartBlock := envUint64("SOUL_REPUTATION_TIP_START_BLOCK", 0)
	soulRepTipChunkSize := envUint64Positive("SOUL_REPUTATION_TIP_BLOCK_CHUNK_SIZE", 5000)
	soulRepTipScale := envFloat64Bounded("SOUL_REPUTATION_TIP_SCALE", 10, 0.000001, 1_000_000)
	soulRepWeightEconomic := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_ECONOMIC", 1, 0, 1000)
	soulRepWeightSocial := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_SOCIAL", 0, 0, 1000)
	soulRepWeightValidation := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_VALIDATION", 0, 0, 1000)
	soulRepWeightTrust := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_TRUST", 0, 0, 1000)
	soulRepWeightIntegrity := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_INTEGRITY", 0, 0, 1000)
	soulRepWeightCommunication := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_COMMUNICATION", 0.1, 0, 1000)

	soulValEpochHours := envInt64Bounded("SOUL_VALIDATION_DECAY_EPOCH_HOURS", 168, 1, 24*365)
	soulValDecayRate := envFloat64Bounded("SOUL_VALIDATION_DECAY_RATE", 0.01, 0, 1)

	managedOn := envBoolOn("MANAGED_PROVISIONING_ENABLED")
	managedParentDomain := envLowerStringDefault("MANAGED_PARENT_DOMAIN", "greater.website")
	managedInstanceRoleName := envStringDefault("MANAGED_INSTANCE_ROLE_NAME", "OrganizationAccountAccessRole")
	managedAccountNamePrefix := envStringDefault("MANAGED_ACCOUNT_NAME_PREFIX", "lesser-")
	managedDefaultRegion := envStringDefault("MANAGED_DEFAULT_REGION", envStringDefault("AWS_REGION", "us-east-1"))
	managedProvisionRunnerProjectName := envStringDefault(
		"MANAGED_PROVISION_RUNNER_PROJECT_NAME",
		fmt.Sprintf("lesser-host-%s-provision-runner", stage),
	)
	managedLesserGitHubOwner := envStringDefault("MANAGED_LESSER_GITHUB_OWNER", "equaltoai")
	managedLesserGitHubRepo := envStringDefault("MANAGED_LESSER_GITHUB_REPO", "lesser")
	managedLesserBodyGitHubOwner := envStringDefault("MANAGED_LESSER_BODY_GITHUB_OWNER", "equaltoai")
	managedLesserBodyGitHubRepo := envStringDefault("MANAGED_LESSER_BODY_GITHUB_REPO", "lesser-body")

	paymentsProvider := envLowerStringDefault("PAYMENTS_PROVIDER", "none")
	centsPer1000Credits := envInt64Bounded("PAYMENTS_CENTS_PER_1000_CREDITS", 100, 1, 1_000_000)

	microvmCfg := loadHostedGenesisMicroVMConfig()

	portalHost := envStringDefault("WEBAUTHN_RP_ID", "lesser.host")
	checkoutSuccessURL := envStringDefault(
		"PAYMENTS_CHECKOUT_SUCCESS_URL",
		fmt.Sprintf("https://%s/portal/billing?success=1", portalHost),
	)
	checkoutCancelURL := envStringDefault(
		"PAYMENTS_CHECKOUT_CANCEL_URL",
		fmt.Sprintf("https://%s/portal/billing?canceled=1", portalHost),
	)

	return Config{
		AppName: "lesser-host",
		Stage:   stage,

		StateTableName:               stateTableName,
		PublicBaseURL:                publicBaseURL,
		SoulEmailInboundDomain:       envString("SOUL_EMAIL_INBOUND_DOMAIN"),
		InboundEmailBucketName:       envString("INBOUND_EMAIL_BUCKET_NAME"),
		InboundEmailS3Prefix:         envStringDefault("INBOUND_EMAIL_S3_PREFIX", "ses/inbound/"),
		SoulCommMailboxBucketName:    envString("SOUL_COMM_MAILBOX_BUCKET_NAME"),
		SoulCommMailboxRetentionDays: envInt64Bounded("SOUL_COMM_MAILBOX_RETENTION_DAYS", 90, 1, 365),
		CommWebhookSharedSecretSSMParam: envStringDefault(
			"COMM_WEBHOOK_SHARED_SECRET_SSM_PARAM",
			fmt.Sprintf("/lesser-host/comm/%s/webhook", stage),
		),
		TelnyxWebhookPublicKey: envString("TELNYX_WEBHOOK_PUBLIC_KEY"),

		ENSGatewayEnabled:             ensGatewayEnabled,
		ENSGatewayChainID:             envInt64Positive("ENS_GATEWAY_CHAIN_ID", defaultENSGatewayChainID),
		ENSGatewayChainName:           envLowerStringDefault("ENS_GATEWAY_CHAIN_NAME", defaultENSGatewayChainName),
		ENSGatewayRootName:            ensGatewayRootName,
		ENSGatewayResolverAddress:     envString("ENS_GATEWAY_RESOLVER_ADDRESS"),
		ENSGatewaySigningKeyID:        envString("ENS_GATEWAY_SIGNING_KEY_ID"),
		ENSGatewaySigningPrivateKey:   envString("ENS_GATEWAY_SIGNING_PRIVATE_KEY"),
		ENSGatewaySignatureTTLSeconds: ensGatewayTTL,

		ArtifactBucketName:    envString("ARTIFACT_BUCKET_NAME"),
		PreviewQueueURL:       envString("PREVIEW_QUEUE_URL"),
		SafetyQueueURL:        envString("SAFETY_QUEUE_URL"),
		HostedGenesisQueueURL: envString("HOSTED_GENESIS_QUEUE_URL"),
		ProvisionQueueURL:     envString("PROVISION_QUEUE_URL"),
		CommQueueURL:          envString("COMM_QUEUE_URL"),

		BootstrapWalletAddress: envString("BOOTSTRAP_WALLET_ADDRESS"),

		AttestationSigningKeyID: envString("ATTESTATION_SIGNING_KEY_ID"),
		AttestationPublicKeyIDs: publicKeyIDs,

		WebAuthnRPID:    envString("WEBAUTHN_RP_ID"),
		WebAuthnOrigins: origins,

		TipEnabled:                  tipsOn,
		TipChainID:                  tipChainID,
		TipRPCURL:                   envString("TIP_RPC_URL"),
		TipRPCURLSSMParam:           envString("TIP_RPC_URL_SSM_PARAM"),
		TipContractAddress:          envString("TIP_CONTRACT_ADDRESS"),
		TipAdminSafeAddress:         envString("TIP_ADMIN_SAFE_ADDRESS"),
		TipDefaultHostWalletAddress: envString("TIP_DEFAULT_HOST_WALLET_ADDRESS"),
		TipDefaultHostFeeBps:        tipDefaultHostFeeBps,
		TipTxMode:                   tipTxMode,

		SoulEnabled:                              soulOn,
		SoulChainID:                              soulChainID,
		SoulRPCURL:                               envString("SOUL_RPC_URL"),
		SoulRPCURLSSMParam:                       envString("SOUL_RPC_URL_SSM_PARAM"),
		SoulRegistryContractAddress:              envString("SOUL_REGISTRY_CONTRACT_ADDRESS"),
		SoulReputationAttestationContractAddress: envString("SOUL_REPUTATION_ATTESTATION_CONTRACT_ADDRESS"),
		SoulValidationAttestationContractAddress: envString("SOUL_VALIDATION_ATTESTATION_CONTRACT_ADDRESS"),
		SoulAdminSafeAddress:                     envString("SOUL_ADMIN_SAFE_ADDRESS"),
		SoulTxMode:                               soulTxMode,
		SoulSupportedCapabilities:                soulCaps,
		SoulPackBucketName:                       soulPackBucketName,
		SoulPackBucketNameSSMParam:               envString("SOUL_PACK_BUCKET_NAME_SSM_PARAM"),
		SoulMintSignerKeySSMParam:                envString("SOUL_MINT_SIGNER_KEY_SSM_PARAM"),
		SoulPublicCORSOrigins:                    soulPublicCORSOrigins,
		SoulPublicOnChainAvatarEnabled:           soulPublicOnChainAvatarEnabled,
		SoulV2StrictIntegrity:                    soulV2StrictIntegrity,

		SoulReputationTipStartBlock:       soulRepTipStartBlock,
		SoulReputationTipBlockChunkSize:   soulRepTipChunkSize,
		SoulReputationTipScale:            soulRepTipScale,
		SoulReputationWeightEconomic:      soulRepWeightEconomic,
		SoulReputationWeightSocial:        soulRepWeightSocial,
		SoulReputationWeightValidation:    soulRepWeightValidation,
		SoulReputationWeightTrust:         soulRepWeightTrust,
		SoulReputationWeightIntegrity:     soulRepWeightIntegrity,
		SoulReputationWeightCommunication: soulRepWeightCommunication,
		SoulValidationDecayEpochHours:     soulValEpochHours,
		SoulValidationDecayRate:           soulValDecayRate,

		ManagedProvisioningEnabled:        managedOn,
		ManagedOrgVendingRoleARN:          envString("MANAGED_ORG_VENDING_ROLE_ARN"),
		ManagedParentDomain:               managedParentDomain,
		ManagedParentHostedZoneID:         envString("MANAGED_PARENT_HOSTED_ZONE_ID"),
		ManagedInstanceRoleName:           managedInstanceRoleName,
		ManagedTargetOrganizationalUnitID: envString("MANAGED_TARGET_OU_ID"),
		ManagedAccountEmailTemplate:       envString("MANAGED_ACCOUNT_EMAIL_TEMPLATE"),
		ManagedAccountNamePrefix:          managedAccountNamePrefix,
		ManagedDefaultRegion:              managedDefaultRegion,
		ManagedLesserDefaultVersion:       envString("MANAGED_LESSER_DEFAULT_VERSION"),
		ManagedProvisionRunnerProjectName: managedProvisionRunnerProjectName,
		ManagedProvisionRunnerRoleARN:     envString("MANAGED_PROVISION_RUNNER_ROLE_ARN"),
		ManagedLesserGitHubOwner:          managedLesserGitHubOwner,
		ManagedLesserGitHubRepo:           managedLesserGitHubRepo,
		ManagedLesserGitHubTokenSSMParam:  envString("MANAGED_LESSER_GITHUB_TOKEN_SSM_PARAM"),
		ManagedLesserBodyDefaultVersion:   envString("MANAGED_LESSER_BODY_DEFAULT_VERSION"),
		ManagedLesserBodyGitHubOwner:      managedLesserBodyGitHubOwner,
		ManagedLesserBodyGitHubRepo:       managedLesserBodyGitHubRepo,

		// CSR-017: consent encryption key for at-rest protection of provisioning consent
		// payloads stored in the DynamoDB ProvisionJob record.
		ManagedProvisionConsentEncryptionKeyHex: envString("MANAGED_PROVISION_CONSENT_ENCRYPTION_KEY_HEX"),

		PaymentsProvider:            paymentsProvider,
		PaymentsCheckoutSuccessURL:  checkoutSuccessURL,
		PaymentsCheckoutCancelURL:   checkoutCancelURL,
		PaymentsCentsPer1000Credits: centsPer1000Credits,

		HostedGenesisMicroVM: microvmCfg,
	}
}

// hostedGenesisMicroVMCompactConfig is the compact JSON shape stored in
// HOSTED_GENESIS_MICROVM_CONFIG_JSON. The intentionally short JSON field names
// are a deployment-budget measure, not a public API: CDK owns the producer and
// this package owns the consumer. Values remain the same AppTheory request
// inputs the legacy per-variable env carried.
type hostedGenesisMicroVMCompactConfig struct {
	Version                int                                 `json:"v"`
	ControllerEndpoint     string                              `json:"ep"`
	AuthTokenSSMParam      string                              `json:"ap"`
	ImageRef               string                              `json:"img"`
	IngressConnectorRefs   string                              `json:"in"`
	EgressConnectorRefs    string                              `json:"eg"`
	MaximumDurationSeconds *int32                              `json:"max"`
	IdlePolicy             hostedGenesisMicroVMCompactIdleJSON `json:"idle"`
}

type hostedGenesisMicroVMCompactIdleJSON struct {
	AutoResumeEnabled        *bool  `json:"ar"`
	MaxIdleDurationSeconds   *int32 `json:"max"`
	SuspendedDurationSeconds *int32 `json:"sus"`
}

func loadHostedGenesisMicroVMConfig() HostedGenesisMicroVMConfig {
	if raw := envString(HostedGenesisMicroVMConfigJSONEnv); raw != "" {
		return parseHostedGenesisMicroVMCompactConfig(raw)
	}
	return loadHostedGenesisMicroVMLegacyEnvConfig()
}

func loadHostedGenesisMicroVMLegacyEnvConfig() HostedGenesisMicroVMConfig {
	// P52 H1.5: production MicroVM HTTP-transport dispatch config. The
	// AppTheoryMicrovmController CDK construct exposes the controller endpoint;
	// the CDK grants the control-plane Lambda HTTP egress to that endpoint +
	// ssm:GetParameter on the auth-token SecureString. The control plane never
	// receives MicroVM IAM or session-registry access — it only speaks HTTP to
	// the governed controller API with the authorizer bearer token (loaded from
	// SSM at runtime, never committed or logged).
	microvmEgressRefs := parseCSV(envString("APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS"))
	if len(microvmEgressRefs) == 0 {
		microvmEgressRefs = parseCSV(envString("APPTHEORY_MICROVM_NETWORK_CONNECTOR_REFS"))
	}
	return HostedGenesisMicroVMConfig{
		Enabled:                envBoolOn("HOSTED_GENESIS_MICROVM_ENABLED"),
		ControllerEndpoint:     strings.TrimSpace(envString("APPTHEORY_MICROVM_CONTROLLER_ENDPOINT")),
		AuthTokenSSMParam:      strings.TrimSpace(envString("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SSM_PARAM")),
		ImageRef:               envString("APPTHEORY_MICROVM_IMAGE_REF"),
		NetworkConnectorRef:    firstNonEmpty(microvmEgressRefs),
		IngressConnectorRefs:   parseCSV(envString("APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS")),
		EgressConnectorRefs:    microvmEgressRefs,
		MaximumDurationSeconds: envInt32Bounded("HOSTED_GENESIS_MICROVM_MAXIMUM_DURATION_SECONDS", HostedGenesisMicroVMDefaultMaximumDurationSeconds, 0, 3600),
		IdlePolicy:             hostedGenesisMicroVMDefaultIdlePolicyFromEnv(),
	}
}

func parseHostedGenesisMicroVMCompactConfig(raw string) HostedGenesisMicroVMConfig {
	cfg := HostedGenesisMicroVMConfig{
		Enabled:                true,
		MaximumDurationSeconds: HostedGenesisMicroVMDefaultMaximumDurationSeconds,
		IdlePolicy:             hostedGenesisMicroVMDefaultIdlePolicy(),
	}
	var compact hostedGenesisMicroVMCompactConfig
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &compact); err != nil || compact.Version != 1 {
		// Fail closed: a present-but-invalid compact env means deployment config
		// is malformed. Do not silently fall back to legacy variables because
		// that could mask a broken CDK/runtime boundary.
		return cfg
	}
	cfg.ControllerEndpoint = strings.TrimSpace(compact.ControllerEndpoint)
	cfg.AuthTokenSSMParam = strings.TrimSpace(compact.AuthTokenSSMParam)
	cfg.ImageRef = strings.TrimSpace(compact.ImageRef)
	cfg.IngressConnectorRefs = parseCSV(compact.IngressConnectorRefs)
	cfg.EgressConnectorRefs = parseCSV(compact.EgressConnectorRefs)
	cfg.NetworkConnectorRef = firstNonEmpty(cfg.EgressConnectorRefs)
	cfg.MaximumDurationSeconds = boundedInt32Ptr(compact.MaximumDurationSeconds, HostedGenesisMicroVMDefaultMaximumDurationSeconds, 0, 3600)
	cfg.IdlePolicy = HostedGenesisMicroVMIdlePolicyConfig{
		AutoResumeEnabled:        boolPtrValue(compact.IdlePolicy.AutoResumeEnabled, false),
		MaxIdleDurationSeconds:   boundedInt32Ptr(compact.IdlePolicy.MaxIdleDurationSeconds, HostedGenesisMicroVMDefaultIdleMaxSeconds, 1, 3600),
		SuspendedDurationSeconds: boundedInt32Ptr(compact.IdlePolicy.SuspendedDurationSeconds, HostedGenesisMicroVMDefaultIdleSuspendedSeconds, 1, 3600),
	}
	return cfg
}

func hostedGenesisMicroVMDefaultIdlePolicyFromEnv() HostedGenesisMicroVMIdlePolicyConfig {
	return HostedGenesisMicroVMIdlePolicyConfig{
		AutoResumeEnabled:        envBoolOn("HOSTED_GENESIS_MICROVM_IDLE_AUTO_RESUME_ENABLED"),
		MaxIdleDurationSeconds:   envInt32Bounded("HOSTED_GENESIS_MICROVM_IDLE_MAX_SECONDS", HostedGenesisMicroVMDefaultIdleMaxSeconds, 1, 3600),
		SuspendedDurationSeconds: envInt32Bounded("HOSTED_GENESIS_MICROVM_IDLE_SUSPENDED_SECONDS", HostedGenesisMicroVMDefaultIdleSuspendedSeconds, 1, 3600),
	}
}

func hostedGenesisMicroVMDefaultIdlePolicy() HostedGenesisMicroVMIdlePolicyConfig {
	return HostedGenesisMicroVMIdlePolicyConfig{
		MaxIdleDurationSeconds:   HostedGenesisMicroVMDefaultIdleMaxSeconds,
		SuspendedDurationSeconds: HostedGenesisMicroVMDefaultIdleSuspendedSeconds,
	}
}

func boundedInt32Ptr(value *int32, fallback int32, minValue int32, maxValue int32) int32 {
	if value == nil || *value < minValue || *value > maxValue {
		return fallback
	}
	return *value
}

func boolPtrValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultENSGatewayChain(stage string) (int64, string) {
	if stage == "live" {
		return 1, "mainnet"
	}
	return 11155111, "sepolia"
}
