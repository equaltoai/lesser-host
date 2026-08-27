package store

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/theory-cloud/tabletheory/v3"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// lambdaTimeoutBuffer is the cold-start TableTheory buffer host leaves before
// the Lambda hard deadline so handlers can return structured errors instead
// of timing out mid-DynamoDB operation.
const lambdaTimeoutBuffer = 1500 * time.Millisecond

func registeredModels() []any {
	return []any{
		&models.AIJob{},
		&models.AIResult{},
		&models.Attestation{},
		&models.BillingPaymentMethod{},
		&models.BillingProfile{},
		&models.ControlPlaneConfig{},
		&models.CostTelemetry{},
		&models.CreditPurchase{},
		&models.Domain{},
		&models.ExternalInstanceRegistration{},
		&models.HostedGenesisSession{},
		&models.HostedGenesisMicroVMExecution{},
		&models.SetupSession{},
		&models.TipHostRegistration{},
		&models.TipHostState{},
		&models.TipRegistryOperation{},
		&models.UsageLedgerEntry{},
		&models.UsageMeteringDedup{},
		&models.User{},
		&models.OperatorSession{},
		&models.InstanceBudgetMonth{},
		&models.WalletChallenge{},
		&models.WalletCredential{},
		&models.WalletIndex{},
		&models.WebAuthnChallenge{},
		&models.WebAuthnCredential{},
		&models.Instance{},
		&models.LinkPreview{},
		&models.LinkSafetyBasicResult{},
		&models.InstanceKey{},
		&models.ProvisionJob{},
		&models.UpdateJob{},
		&models.ProvisionConsentChallenge{},
		&models.RenderArtifact{},
		&models.AuditLogEntry{},
		&models.VanityDomainRequest{},
		&models.SoulAgentFailure{},
		&models.SoulAgentPromotion{},
		&models.SoulAgentCommActivity{},
		&models.SoulAgentRegistration{},
		&models.SoulAgentContactPreferences{},
		&models.SoulAgentChannel{},
		&models.SoulAgentIdentity{},
		&models.SoulAgentIdentityGSI3BackfillMarker{},
		&models.SoulAgentMintConversationGSI4BackfillMarker{},
		&models.SoulAgentPeerEndorsement{},
		&models.SoulWalletRotationRequest{},
		&models.SoulAgentVersion{},
		&models.SoulCommVoiceInstruction{},
		&models.SoulWalletAgentIndex{},
		&models.SoulDomainAgentIndex{},
		&models.SoulCapabilityAgentIndex{},
		&models.SoulBoundaryKeywordAgentIndex{},
		&models.SoulAgentENSResolution{},
		&models.SoulAgentRelationship{},
		&models.SoulAgentReputation{},
		&models.SoulCommSendIdempotency{},
		&models.SoulX402InvocationGrant{},
		&models.SoulX402InvocationGrantUsage{},
		&models.SoulAgentCommQueue{},
		&models.SoulAgentBoundary{},
		&models.SoulAgentValidationChallenge{},
		&models.SoulAgentPromotionLifecycleEvent{},
		&models.SoulAgentContinuity{},
		&models.SoulLifecycleChallenge{},
		&models.SoulAgentMintConversation{},
		&models.SoulMintConversationIdempotency{},
		&models.SoulAgentValidationRecord{},
		&models.SoulCommMessageStatus{},
		&models.SoulCommMailboxMessage{},
		&models.SoulCommMailboxEvent{},
		&models.SoulOperation{},
		&models.SoulEmailAgentIndex{},
		&models.SoulEmailLegacyAliasIndex{},
		&models.SoulPhoneAgentIndex{},
		&models.SoulChannelAgentIndex{},
		&models.SoulRelationshipFromIndex{},
		&models.SoulAgentDispute{},
		&models.TrustQueueDepthSample{},
	}
}

// LambdaInit initializes the database connection and registers all models.
func LambdaInit() (DB, error) {
	db, err := tabletheory.LambdaInit(registeredModels()...)
	if err != nil {
		return nil, err
	}
	return db.WithLambdaTimeoutConfig(tabletheory.LambdaTimeoutConfig{Buffer: lambdaTimeoutBuffer}), nil
}

// MicroVMInit initializes a fresh TableTheory DB for an in-VM turn execution.
//
// Do not use TableTheory LambdaInit here: LambdaInit intentionally caches a
// process-global DB for Lambda warm starts, but Lambda MicroVM images may start
// the workload during image build and then run user turns much later. A global
// DB would preserve a DynamoDB client whose credential provider was resolved at
// image-build/startup time, producing ExpiredTokenException on real turns.
// MicroVMInit creates a fresh DB for the current turn using the caller-supplied
// execution-role credentials provider loaded inside the running MicroVM.
func MicroVMInit(_ context.Context, credentials aws.CredentialsProvider) (DB, error) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}
	if credentials != nil {
		options = append(options, awsconfig.WithCredentialsProvider(credentials))
	}
	db, err := tabletheory.New(tabletheory.Config{
		Region:           region,
		KMSKeyARN:        firstNonEmptyEnv("TABLETHEORY_KMS_KEY_ARN", "KMS_KEY_ARN"),
		MaxRetries:       3,
		DefaultRCU:       5,
		DefaultWCU:       5,
		AutoMigrate:      false,
		EnableMetrics:    false,
		AWSConfigOptions: options,
	})
	if err != nil {
		return nil, err
	}
	fresh, ok := db.(DB)
	if !ok {
		return nil, fmt.Errorf("tabletheory DB does not satisfy host DB contract")
	}
	return lambdaTimeoutGuardDB(fresh), nil
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}
