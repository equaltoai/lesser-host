package controlplane

import (
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulConfigReputationWeights struct {
	Economic      float64 `json:"economic"`
	Social        float64 `json:"social"`
	Validation    float64 `json:"validation"`
	Trust         float64 `json:"trust"`
	Integrity     float64 `json:"integrity"`
	Communication float64 `json:"communication"`
}

type soulConfigResponse struct {
	Enabled                 bool                         `json:"enabled"`
	ChainID                 int64                        `json:"chain_id"`
	RegistryContractAddress string                       `json:"registry_contract_address"`
	AdminSafeAddress        string                       `json:"admin_safe_address,omitempty"`
	TxMode                  string                       `json:"tx_mode,omitempty"`
	SupportedCapabilities   []string                     `json:"supported_capabilities,omitempty"`
	PolicyVocabulary        *soulConfigPolicyVocabulary  `json:"policy_vocabulary,omitempty"`
	ReputationWeights       *soulConfigReputationWeights `json:"reputation_weights,omitempty"`
}

type soulConfigPolicyVocabulary struct {
	Version                           string   `json:"version"`
	AnchorStates                      []string `json:"anchor_states"`
	OperationalBindings               []string `json:"operational_bindings"`
	CapabilityPolicyVersion           string   `json:"capability_policy_version"`
	CallerAccessPaymentPolicyVersion  string   `json:"caller_access_payment_policy_version"`
	PhoneEntitlementStatuses          []string `json:"phone_entitlement_statuses"`
	PublicPaidCallerAccess            []string `json:"public_paid_caller_access"`
	MissingPolicyRecordMigrationState string   `json:"missing_policy_record_migration_state"`
}

func (s *Server) requireSoulRegistryConfigured() *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if !s.cfg.SoulEnabled {
		return newAppTheoryError("app.conflict", "soul registry is not configured")
	}
	if s.cfg.SoulChainID <= 0 || strings.TrimSpace(s.cfg.SoulRegistryContractAddress) == "" {
		return newAppTheoryError("app.conflict", "soul registry is not configured")
	}
	if !common.IsHexAddress(strings.TrimSpace(s.cfg.SoulRegistryContractAddress)) {
		return newAppTheoryError("app.conflict", "soul registry is not configured")
	}
	return nil
}

func (s *Server) requireSoulRPCConfigured() *apptheory.AppTheoryError {
	if strings.TrimSpace(s.cfg.SoulRPCURL) == "" {
		return newAppTheoryError("app.conflict", "soul rpc not configured")
	}
	return nil
}

func (s *Server) handleSoulConfig(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	contractAddr := strings.TrimSpace(s.cfg.SoulRegistryContractAddress)
	if !s.cfg.SoulEnabled || s.cfg.SoulChainID <= 0 || contractAddr == "" || !common.IsHexAddress(contractAddr) {
		return nil, newAppTheoryError("app.not_found", "not found")
	}

	caps := normalizeSoulCapabilitiesLoose(s.cfg.SoulSupportedCapabilities)

	resp, err := apptheory.JSON(http.StatusOK, soulConfigResponse{
		Enabled:                 true,
		ChainID:                 s.cfg.SoulChainID,
		RegistryContractAddress: strings.ToLower(contractAddr),
		AdminSafeAddress:        strings.ToLower(strings.TrimSpace(s.cfg.SoulAdminSafeAddress)),
		TxMode:                  strings.ToLower(strings.TrimSpace(s.cfg.SoulTxMode)),
		SupportedCapabilities:   caps,
		PolicyVocabulary: &soulConfigPolicyVocabulary{
			Version: models.SoulPolicyVersionHostedBoundSoulV1,
			AnchorStates: []string{
				models.SoulAnchorStateHostedOffchain,
				models.SoulAnchorStateImmutableOnchain,
			},
			OperationalBindings: []string{
				models.SoulOperationalBindingHostedBoundSoul,
			},
			CapabilityPolicyVersion:          models.SoulCapabilityPolicyVersionV1,
			CallerAccessPaymentPolicyVersion: models.SoulCallerAccessPaymentPolicyVersionV1,
			PhoneEntitlementStatuses: []string{
				models.SoulPhoneEntitlementNotEntitled,
				models.SoulPhoneEntitlementProvisioned,
				models.SoulPhoneEntitlementPaid,
			},
			PublicPaidCallerAccess: []string{
				models.SoulPublicPaidCallerAccessDenied,
			},
			MissingPolicyRecordMigrationState: models.SoulPolicyMigrationStateImplicitDefaultV1,
		},
		ReputationWeights: &soulConfigReputationWeights{
			Economic:      s.cfg.SoulReputationWeightEconomic,
			Social:        s.cfg.SoulReputationWeightSocial,
			Validation:    s.cfg.SoulReputationWeightValidation,
			Trust:         s.cfg.SoulReputationWeightTrust,
			Integrity:     s.cfg.SoulReputationWeightIntegrity,
			Communication: s.cfg.SoulReputationWeightCommunication,
		},
	})
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	resp.Headers["cache-control"] = []string{"public, max-age=3600"}
	resp.Headers["access-control-allow-origin"] = []string{"*"}
	return resp, nil
}
