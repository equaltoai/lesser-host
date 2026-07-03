package config

import (
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
	// and complete, NewServer constructs an in-process MicroVMControllerRuntime
	// and wraps it in a ControllerRuntimeDispatcher on the Server so the hosted
	// genesis accept path dispatches the controller run command and returns 202
	// without a synchronous control-plane LLM call. When disabled or incomplete,
	// NewServer leaves the dispatcher unwired and the accept path fails closed
	// and loudly with a typed 503 microvm_unavailable (no sync LLM fallback).
	// These mirror the env vars the AppTheoryMicrovmController CDK construct sets
	// on the controller Lambda; the CDK grants the control-plane Lambda the same
	// values plus MicroVM IAM + session-registry access.
	HostedGenesisMicroVM HostedGenesisMicroVMConfig
}

// HostedGenesisMicroVMConfig groups the AppTheory M16 MicroVM controller runtime
// inputs controlplane.NewServer uses to construct the production dispatcher.
type HostedGenesisMicroVMConfig struct {
	Enabled                   bool
	ImageRef                  string
	NetworkConnectorRef       string
	IngressConnectorRefs      []string
	EgressConnectorRefs       []string
	SessionRegistryTable      string
	MaximumDurationSeconds    int32
	ReconstructionStaleAfterS int64
}

// Complete reports whether the MicroVM dispatch config has the minimum required
// fields to construct a real ControllerRuntimeDispatcher. NewServer uses this to
// decide between wiring the real dispatcher and failing closed (never a silent
// sync LLM fallback).
func (c HostedGenesisMicroVMConfig) Complete() bool {
	return c.Enabled && c.ImageRef != "" && c.NetworkConnectorRef != "" && c.SessionRegistryTable != ""
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

	// P52 H1.5: production MicroVM dispatch config. The AppTheoryMicrovmController
	// CDK construct sets these env vars on the controller Lambda; the CDK also
	// grants the control-plane Lambda the same values (plus MicroVM IAM + session
	// registry access) so NewServer can construct the in-process dispatcher.
	microvmEgressRefs := parseCSV(envString("APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS"))
	if len(microvmEgressRefs) == 0 {
		microvmEgressRefs = parseCSV(envString("APPTHEORY_MICROVM_NETWORK_CONNECTOR_REFS"))
	}
	microvmNetworkConnectorRef := ""
	for _, ref := range microvmEgressRefs {
		if ref = strings.TrimSpace(ref); ref != "" {
			microvmNetworkConnectorRef = ref
			break
		}
	}
	// MaximumDurationSeconds is sized for the longest LLM turn plus in-VM
	// declaration extraction (decision 7): default 300s, bounded to the M16
	// provider's int32 range [0,3600]. A non-positive config disables the cap
	// and lets the provider/default apply (still fail-closed; never a sync
	// fallback). The bound keeps the int64->int32 narrowing safe (gosec G115).
	microvmMaxDurationRaw := envInt64Bounded("HOSTED_GENESIS_MICROVM_MAXIMUM_DURATION_SECONDS", 300, 0, 3600)
	microvmMaxDuration := int32(0)
	if microvmMaxDurationRaw > 0 && microvmMaxDurationRaw <= 3600 {
		microvmMaxDuration = int32(microvmMaxDurationRaw)
	}
	microvmReconstructionStaleAfter := envInt64Bounded("HOSTED_GENESIS_MICROVM_RECONSTRUCTION_STALE_AFTER_SECONDS", 300, 1, 3600)
	microvmCfg := HostedGenesisMicroVMConfig{
		Enabled:                   envBoolOn("HOSTED_GENESIS_MICROVM_ENABLED"),
		ImageRef:                  envString("APPTHEORY_MICROVM_IMAGE_REF"),
		NetworkConnectorRef:       microvmNetworkConnectorRef,
		IngressConnectorRefs:      parseCSV(envString("APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS")),
		EgressConnectorRefs:       microvmEgressRefs,
		SessionRegistryTable:      envString("APPTHEORY_MICROVM_SESSION_REGISTRY_TABLE"),
		MaximumDurationSeconds:    microvmMaxDuration,
		ReconstructionStaleAfterS: microvmReconstructionStaleAfter,
	}

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

func defaultENSGatewayChain(stage string) (int64, string) {
	if stage == "live" {
		return 1, "mainnet"
	}
	return 11155111, "sepolia"
}
