package store

import (
	"time"

	"github.com/theory-cloud/tabletheory"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// lambdaTimeoutBuffer is the cold-start TableTheory buffer host leaves before
// the Lambda hard deadline so handlers can return structured errors instead
// of timing out mid-DynamoDB operation.
const lambdaTimeoutBuffer = 1500 * time.Millisecond

// LambdaInit initializes the database connection and registers all models.
func LambdaInit() (DB, error) {
	db, err := tabletheory.LambdaInit(
		&models.AIJob{},
		&models.AIResult{},
		&models.Attestation{},
		&models.BillingPaymentMethod{},
		&models.BillingProfile{},
		&models.ControlPlaneConfig{},
		&models.CreditPurchase{},
		&models.Domain{},
		&models.ExternalInstanceRegistration{},
		&models.SetupSession{},
		&models.TipHostRegistration{},
		&models.TipHostState{},
		&models.TipRegistryOperation{},
		&models.UsageLedgerEntry{},
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
		&models.SoulAgentMintConversation{},
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
	)
	if err != nil {
		return nil, err
	}
	return db.WithLambdaTimeoutConfig(tabletheory.LambdaTimeoutConfig{Buffer: lambdaTimeoutBuffer}), nil
}
