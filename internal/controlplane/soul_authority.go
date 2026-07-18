package controlplane

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func normalizeSoulAuthorityModel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case models.SoulAuthorityModelInstanceTrust:
		return models.SoulAuthorityModelInstanceTrust
	case models.SoulAuthorityModelWalletPrincipal:
		return models.SoulAuthorityModelWalletPrincipal
	default:
		return ""
	}
}

func soulRegistrationAuthorityModel(reg *models.SoulAgentRegistration) string {
	if reg == nil {
		return ""
	}
	if model := normalizeSoulAuthorityModel(reg.AuthorityModel); model != "" {
		return model
	}
	if strings.TrimSpace(reg.Wallet) != "" || strings.TrimSpace(reg.WalletMessage) != "" || reg.WalletVerified {
		return models.SoulAuthorityModelWalletPrincipal
	}
	return ""
}

func isRegistrationInstanceTrust(reg *models.SoulAgentRegistration) bool {
	return normalizeSoulAuthorityModel(soulRegistrationAuthorityModel(reg)) == models.SoulAuthorityModelInstanceTrust
}

func soulIdentityAuthorityModel(identity *models.SoulAgentIdentity) string {
	if identity == nil {
		return ""
	}
	if model := normalizeSoulAuthorityModel(identity.AuthorityModel); model != "" {
		return model
	}
	if strings.TrimSpace(identity.Wallet) != "" || strings.TrimSpace(identity.PrincipalAddress) != "" || strings.TrimSpace(identity.PrincipalDeclaration) != "" {
		return models.SoulAuthorityModelWalletPrincipal
	}
	return ""
}

func requireExplicitInstanceTrustAuthority(reg *models.SoulAgentRegistration, identity *models.SoulAgentIdentity) *apptheory.AppTheoryError {
	if reg == nil || identity == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if normalizeSoulAuthorityModel(reg.AuthorityModel) != models.SoulAuthorityModelInstanceTrust || normalizeSoulAuthorityModel(identity.AuthorityModel) != models.SoulAuthorityModelInstanceTrust {
		return newAppTheoryError("app.conflict", "hosted instance-trust authority is not recorded explicitly")
	}
	return nil
}

func isExplicitInstanceTrustAuthority(reg *models.SoulAgentRegistration, identity *models.SoulAgentIdentity) bool {
	return reg != nil && identity != nil &&
		normalizeSoulAuthorityModel(reg.AuthorityModel) == models.SoulAuthorityModelInstanceTrust &&
		normalizeSoulAuthorityModel(identity.AuthorityModel) == models.SoulAuthorityModelInstanceTrust
}
