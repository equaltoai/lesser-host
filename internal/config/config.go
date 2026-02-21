package config

import (
	"fmt"
)

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	AppName string
	Stage   string

	StateTableName string

	ArtifactBucketName string
	PreviewQueueURL    string
	SafetyQueueURL     string
	ProvisionQueueURL  string

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
	SoulEnabled                 bool
	SoulChainID                 int64
	SoulRPCURL                  string
	SoulRPCURLSSMParam          string
	SoulRegistryContractAddress string
	SoulAdminSafeAddress        string
	SoulTxMode                  string // safe|direct
	SoulSupportedCapabilities   []string
	SoulPackBucketName          string
	SoulPackBucketNameSSMParam  string // optional override; default is /soul/<stage>/packBucketName

	// Soul reputation (v0).
	SoulReputationTipStartBlock     uint64
	SoulReputationTipBlockChunkSize uint64
	SoulReputationTipScale          float64
	SoulReputationWeightEconomic    float64
	SoulReputationWeightSocial      float64
	SoulReputationWeightValidation  float64
	SoulReputationWeightTrust       float64

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
	ManagedLesserDefaultVersion       string // semver tag, optional
	ManagedProvisionRunnerProjectName string // CodeBuild project name used to run lesser up
	ManagedLesserGitHubOwner          string // GitHub org/user for the lesser repo
	ManagedLesserGitHubRepo           string // GitHub repo name for lesser
	ManagedLesserGitHubTokenSSMParam  string // optional SSM param name for a GitHub token (CodeBuild)

	// Payments (M10).
	PaymentsProvider            string // stripe|mock|none
	PaymentsCheckoutSuccessURL  string // redirect target after checkout completion
	PaymentsCheckoutCancelURL   string // redirect target after checkout cancel
	PaymentsCentsPer1000Credits int64  // pricing policy: cents per 1000 credits
}

// Load reads environment variables and returns a Config with defaults applied.
func Load() Config {
	stage := envStringDefault("STAGE", "lab")
	stateTableName := envString("STATE_TABLE_NAME")

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

	soulRepTipStartBlock := envUint64("SOUL_REPUTATION_TIP_START_BLOCK", 0)
	soulRepTipChunkSize := envUint64Positive("SOUL_REPUTATION_TIP_BLOCK_CHUNK_SIZE", 5000)
	soulRepTipScale := envFloat64Bounded("SOUL_REPUTATION_TIP_SCALE", 10, 0.000001, 1_000_000)
	soulRepWeightEconomic := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_ECONOMIC", 1, 0, 1000)
	soulRepWeightSocial := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_SOCIAL", 0, 0, 1000)
	soulRepWeightValidation := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_VALIDATION", 0, 0, 1000)
	soulRepWeightTrust := envFloat64Bounded("SOUL_REPUTATION_WEIGHT_TRUST", 0, 0, 1000)

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

	paymentsProvider := envLowerStringDefault("PAYMENTS_PROVIDER", "none")
	centsPer1000Credits := envInt64Bounded("PAYMENTS_CENTS_PER_1000_CREDITS", 100, 1, 1_000_000)

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

		StateTableName: stateTableName,

		ArtifactBucketName: envString("ARTIFACT_BUCKET_NAME"),
		PreviewQueueURL:    envString("PREVIEW_QUEUE_URL"),
		SafetyQueueURL:     envString("SAFETY_QUEUE_URL"),
		ProvisionQueueURL:  envString("PROVISION_QUEUE_URL"),

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

		SoulEnabled:                 soulOn,
		SoulChainID:                 soulChainID,
		SoulRPCURL:                  envString("SOUL_RPC_URL"),
		SoulRPCURLSSMParam:          envString("SOUL_RPC_URL_SSM_PARAM"),
		SoulRegistryContractAddress: envString("SOUL_REGISTRY_CONTRACT_ADDRESS"),
		SoulAdminSafeAddress:        envString("SOUL_ADMIN_SAFE_ADDRESS"),
		SoulTxMode:                  soulTxMode,
		SoulSupportedCapabilities:   soulCaps,
		SoulPackBucketName:          soulPackBucketName,
		SoulPackBucketNameSSMParam:  envString("SOUL_PACK_BUCKET_NAME_SSM_PARAM"),

		SoulReputationTipStartBlock:     soulRepTipStartBlock,
		SoulReputationTipBlockChunkSize: soulRepTipChunkSize,
		SoulReputationTipScale:          soulRepTipScale,
		SoulReputationWeightEconomic:    soulRepWeightEconomic,
		SoulReputationWeightSocial:      soulRepWeightSocial,
		SoulReputationWeightValidation:  soulRepWeightValidation,
		SoulReputationWeightTrust:       soulRepWeightTrust,

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
		ManagedLesserGitHubOwner:          managedLesserGitHubOwner,
		ManagedLesserGitHubRepo:           managedLesserGitHubRepo,
		ManagedLesserGitHubTokenSSMParam:  envString("MANAGED_LESSER_GITHUB_TOKEN_SSM_PARAM"),

		PaymentsProvider:            paymentsProvider,
		PaymentsCheckoutSuccessURL:  checkoutSuccessURL,
		PaymentsCheckoutCancelURL:   checkoutCancelURL,
		PaymentsCentsPer1000Credits: centsPer1000Credits,
	}
}
