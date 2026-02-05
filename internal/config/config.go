package config

import (
	"os"
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
	}
}
