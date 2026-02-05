package config

import (
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

	return Config{
		AppName: "lesser-host",
		Stage:   stage,

		StateTableName: stateTableName,

		ArtifactBucketName: strings.TrimSpace(os.Getenv("ARTIFACT_BUCKET_NAME")),
		PreviewQueueURL:    strings.TrimSpace(os.Getenv("PREVIEW_QUEUE_URL")),
		SafetyQueueURL:     strings.TrimSpace(os.Getenv("SAFETY_QUEUE_URL")),

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
	}
}
