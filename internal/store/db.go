package store

import (
	"github.com/theory-cloud/tabletheory"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func LambdaInit() (DB, error) {
	return tabletheory.LambdaInit(
		&models.AIJob{},
		&models.AIResult{},
		&models.ControlPlaneConfig{},
		&models.SetupSession{},
		&models.OperatorUser{},
		&models.OperatorSession{},
		&models.WalletChallenge{},
		&models.WalletCredential{},
		&models.WalletIndex{},
		&models.WebAuthnChallenge{},
		&models.WebAuthnCredential{},
		&models.Instance{},
		&models.InstanceKey{},
		&models.InstanceBudgetMonth{},
		&models.AuditLogEntry{},
	)
}
