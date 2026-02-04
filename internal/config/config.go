package config

import (
	"os"
	"strings"
)

type Config struct {
	AppName string
	Stage   string

	StateTableName string

	ArtifactBucketName string
	PreviewQueueURL    string
	SafetyQueueURL     string

	BootstrapWalletAddress string

	WebAuthnRPID    string
	WebAuthnOrigins []string
}

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

	return Config{
		AppName: "lesser-host",
		Stage:   stage,

		StateTableName: stateTableName,

		ArtifactBucketName: strings.TrimSpace(os.Getenv("ARTIFACT_BUCKET_NAME")),
		PreviewQueueURL:    strings.TrimSpace(os.Getenv("PREVIEW_QUEUE_URL")),
		SafetyQueueURL:     strings.TrimSpace(os.Getenv("SAFETY_QUEUE_URL")),

		BootstrapWalletAddress: strings.TrimSpace(os.Getenv("BOOTSTRAP_WALLET_ADDRESS")),

		WebAuthnRPID:    strings.TrimSpace(os.Getenv("WEBAUTHN_RP_ID")),
		WebAuthnOrigins: origins,
	}
}

