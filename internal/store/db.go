package store

import (
	"github.com/theory-cloud/tabletheory"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// LambdaInit initializes the database connection and registers all models.
func LambdaInit() (DB, error) {
	return tabletheory.LambdaInit(
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
		&models.SoulCommMailboxMessage{},
		&models.SoulCommMailboxEvent{},
	)
}
