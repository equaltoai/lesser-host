package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
	TipContractAddress          string
	TipAdminSafeAddress         string
	TipDefaultHostWalletAddress string
	TipDefaultHostFeeBps        uint16
	TipTxMode                   string // safe|direct

	// Managed hosting (M9 provisioning).
	ManagedProvisioningEnabled        bool
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
	stage := strings.TrimSpace(os.Getenv("STAGE"))
	if stage == "" {
		stage = "lab"
	}

	stateTableName := strings.TrimSpace(os.Getenv("STATE_TABLE_NAME"))

	originsRaw := strings.TrimSpace(os.Getenv("WEBAUTHN_ORIGINS"))
	var origins []string
	for _, part := range strings.Split(originsRaw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		origins = append(origins, part)
	}

	publicKeyIDsRaw := strings.TrimSpace(os.Getenv("ATTESTATION_PUBLIC_KEY_IDS"))
	var publicKeyIDs []string
	for _, part := range strings.Split(publicKeyIDsRaw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		publicKeyIDs = append(publicKeyIDs, part)
	}

	tipEnabled := strings.ToLower(strings.TrimSpace(os.Getenv("TIP_ENABLED")))
	tipsOn := tipEnabled == "1" || tipEnabled == "true" || tipEnabled == "yes" || tipEnabled == "on"

	tipChainID := int64(0)
	if v := strings.TrimSpace(os.Getenv("TIP_CHAIN_ID")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			tipChainID = n
		}
	}

	tipDefaultHostFeeBps := uint16(0)
	if v := strings.TrimSpace(os.Getenv("TIP_DEFAULT_HOST_FEE_BPS")); v != "" {
		if n, err := strconv.ParseUint(v, 10, 16); err == nil && n <= 500 {
			tipDefaultHostFeeBps = uint16(n)
		}
	}

	tipTxMode := strings.ToLower(strings.TrimSpace(os.Getenv("TIP_TX_MODE")))
	if tipTxMode == "" {
		tipTxMode = "safe"
	}

	managedProvisioningEnabled := strings.ToLower(strings.TrimSpace(os.Getenv("MANAGED_PROVISIONING_ENABLED")))
	managedOn := managedProvisioningEnabled == "1" || managedProvisioningEnabled == "true" || managedProvisioningEnabled == "yes" || managedProvisioningEnabled == "on"

	managedParentDomain := strings.ToLower(strings.TrimSpace(os.Getenv("MANAGED_PARENT_DOMAIN")))
	if managedParentDomain == "" {
		managedParentDomain = "greater.website"
	}

	managedInstanceRoleName := strings.TrimSpace(os.Getenv("MANAGED_INSTANCE_ROLE_NAME"))
	if managedInstanceRoleName == "" {
		managedInstanceRoleName = "OrganizationAccountAccessRole"
	}

	managedAccountNamePrefix := strings.TrimSpace(os.Getenv("MANAGED_ACCOUNT_NAME_PREFIX"))
	if managedAccountNamePrefix == "" {
		managedAccountNamePrefix = "lesser-"
	}

	managedDefaultRegion := strings.TrimSpace(os.Getenv("MANAGED_DEFAULT_REGION"))
	if managedDefaultRegion == "" {
		managedDefaultRegion = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if managedDefaultRegion == "" {
		managedDefaultRegion = "us-east-1"
	}

	managedProvisionRunnerProjectName := strings.TrimSpace(os.Getenv("MANAGED_PROVISION_RUNNER_PROJECT_NAME"))
	if managedProvisionRunnerProjectName == "" {
		managedProvisionRunnerProjectName = fmt.Sprintf("lesser-host-%s-provision-runner", stage)
	}

	managedLesserGitHubOwner := strings.TrimSpace(os.Getenv("MANAGED_LESSER_GITHUB_OWNER"))
	if managedLesserGitHubOwner == "" {
		managedLesserGitHubOwner = "equaltoai"
	}
	managedLesserGitHubRepo := strings.TrimSpace(os.Getenv("MANAGED_LESSER_GITHUB_REPO"))
	if managedLesserGitHubRepo == "" {
		managedLesserGitHubRepo = "lesser"
	}

	paymentsProvider := strings.ToLower(strings.TrimSpace(os.Getenv("PAYMENTS_PROVIDER")))
	if paymentsProvider == "" {
		paymentsProvider = "none"
	}

	centsPer1000Credits := int64(100)
	if v := strings.TrimSpace(os.Getenv("PAYMENTS_CENTS_PER_1000_CREDITS")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 && n <= 1_000_000 {
			centsPer1000Credits = n
		}
	}

	portalHost := strings.TrimSpace(os.Getenv("WEBAUTHN_RP_ID"))
	if portalHost == "" {
		portalHost = "lesser.host"
	}
	checkoutSuccessURL := strings.TrimSpace(os.Getenv("PAYMENTS_CHECKOUT_SUCCESS_URL"))
	if checkoutSuccessURL == "" {
		checkoutSuccessURL = fmt.Sprintf("https://%s/portal/billing?success=1", portalHost)
	}
	checkoutCancelURL := strings.TrimSpace(os.Getenv("PAYMENTS_CHECKOUT_CANCEL_URL"))
	if checkoutCancelURL == "" {
		checkoutCancelURL = fmt.Sprintf("https://%s/portal/billing?canceled=1", portalHost)
	}

	return Config{
		AppName: "lesser-host",
		Stage:   stage,

		StateTableName: stateTableName,

		ArtifactBucketName: strings.TrimSpace(os.Getenv("ARTIFACT_BUCKET_NAME")),
		PreviewQueueURL:    strings.TrimSpace(os.Getenv("PREVIEW_QUEUE_URL")),
		SafetyQueueURL:     strings.TrimSpace(os.Getenv("SAFETY_QUEUE_URL")),
		ProvisionQueueURL:  strings.TrimSpace(os.Getenv("PROVISION_QUEUE_URL")),

		BootstrapWalletAddress: strings.TrimSpace(os.Getenv("BOOTSTRAP_WALLET_ADDRESS")),

		AttestationSigningKeyID: strings.TrimSpace(os.Getenv("ATTESTATION_SIGNING_KEY_ID")),
		AttestationPublicKeyIDs: publicKeyIDs,

		WebAuthnRPID:    strings.TrimSpace(os.Getenv("WEBAUTHN_RP_ID")),
		WebAuthnOrigins: origins,

		TipEnabled:                  tipsOn,
		TipChainID:                  tipChainID,
		TipRPCURL:                   strings.TrimSpace(os.Getenv("TIP_RPC_URL")),
		TipContractAddress:          strings.TrimSpace(os.Getenv("TIP_CONTRACT_ADDRESS")),
		TipAdminSafeAddress:         strings.TrimSpace(os.Getenv("TIP_ADMIN_SAFE_ADDRESS")),
		TipDefaultHostWalletAddress: strings.TrimSpace(os.Getenv("TIP_DEFAULT_HOST_WALLET_ADDRESS")),
		TipDefaultHostFeeBps:        tipDefaultHostFeeBps,
		TipTxMode:                   tipTxMode,

		ManagedProvisioningEnabled:        managedOn,
		ManagedParentDomain:               managedParentDomain,
		ManagedParentHostedZoneID:         strings.TrimSpace(os.Getenv("MANAGED_PARENT_HOSTED_ZONE_ID")),
		ManagedInstanceRoleName:           managedInstanceRoleName,
		ManagedTargetOrganizationalUnitID: strings.TrimSpace(os.Getenv("MANAGED_TARGET_OU_ID")),
		ManagedAccountEmailTemplate:       strings.TrimSpace(os.Getenv("MANAGED_ACCOUNT_EMAIL_TEMPLATE")),
		ManagedAccountNamePrefix:          managedAccountNamePrefix,
		ManagedDefaultRegion:              managedDefaultRegion,
		ManagedLesserDefaultVersion:       strings.TrimSpace(os.Getenv("MANAGED_LESSER_DEFAULT_VERSION")),
		ManagedProvisionRunnerProjectName: managedProvisionRunnerProjectName,
		ManagedLesserGitHubOwner:          managedLesserGitHubOwner,
		ManagedLesserGitHubRepo:           managedLesserGitHubRepo,
		ManagedLesserGitHubTokenSSMParam:  strings.TrimSpace(os.Getenv("MANAGED_LESSER_GITHUB_TOKEN_SSM_PARAM")),

		PaymentsProvider:            paymentsProvider,
		PaymentsCheckoutSuccessURL:  checkoutSuccessURL,
		PaymentsCheckoutCancelURL:   checkoutCancelURL,
		PaymentsCentsPer1000Credits: centsPer1000Credits,
	}
}
