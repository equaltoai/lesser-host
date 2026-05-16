package controlplane

import (
	"context"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	commMetricEntitlementRequired = "entitlement_required"
	commCodeEntitlementRequired   = "comm.entitlement_required"
)

type soulAgentEffectivePolicy struct {
	Version                          string                             `json:"version"`
	AnchorState                      string                             `json:"anchorState"`
	OperationalBinding               string                             `json:"operationalBinding"`
	CapabilityPolicyVersion          string                             `json:"capabilityPolicyVersion"`
	CallerAccessPaymentPolicyVersion string                             `json:"callerAccessPaymentPolicyVersion"`
	Capabilities                     soulAgentCapabilityPolicyView      `json:"capabilities"`
	CallerAccessPayment              soulAgentCallerAccessPolicyView    `json:"callerAccessPayment"`
	Migration                        soulAgentPolicyMigrationPolicyView `json:"migration"`
}

type soulAgentCapabilityPolicyView struct {
	Email soulAgentEmailCapabilityPolicyView `json:"email"`
	Phone soulAgentPhoneCapabilityPolicyView `json:"phone"`
}

type soulAgentEmailCapabilityPolicyView struct {
	DefaultAllowed bool `json:"defaultAllowed"`
}

type soulAgentPhoneCapabilityPolicyView struct {
	EntitlementStatus string `json:"entitlementStatus"`
	SMSAllowed        bool   `json:"smsAllowed"`
	VoiceAllowed      bool   `json:"voiceAllowed"`
}

type soulAgentCallerAccessPolicyView struct {
	PublicPaidCaller soulAgentCallerClassPolicyView `json:"publicPaidCaller"`
}

type soulAgentCallerClassPolicyView struct {
	Access string `json:"access"`
}

type soulAgentPolicyMigrationPolicyView struct {
	State string `json:"state"`
}

func effectiveSoulAgentPolicy(identity *models.SoulAgentIdentity) soulAgentEffectivePolicy {
	policy := defaultSoulAgentEffectivePolicy()
	if identity == nil {
		return policy
	}
	return applySoulAgentIdentityPolicy(policy, identity)
}

func defaultSoulAgentEffectivePolicy() soulAgentEffectivePolicy {
	return soulAgentEffectivePolicy{
		Version:                          models.SoulPolicyVersionHostedBoundSoulV1,
		AnchorState:                      models.SoulAnchorStateHostedOffchain,
		OperationalBinding:               models.SoulOperationalBindingHostedBoundSoul,
		CapabilityPolicyVersion:          models.SoulCapabilityPolicyVersionV1,
		CallerAccessPaymentPolicyVersion: models.SoulCallerAccessPaymentPolicyVersionV1,
		Capabilities: soulAgentCapabilityPolicyView{
			Email: soulAgentEmailCapabilityPolicyView{DefaultAllowed: true},
			Phone: soulAgentPhoneCapabilityPolicyView{
				EntitlementStatus: models.SoulPhoneEntitlementNotEntitled,
			},
		},
		CallerAccessPayment: soulAgentCallerAccessPolicyView{
			PublicPaidCaller: soulAgentCallerClassPolicyView{Access: models.SoulPublicPaidCallerAccessDenied},
		},
		Migration: soulAgentPolicyMigrationPolicyView{State: models.SoulPolicyMigrationStateImplicitDefaultV1},
	}
}

func applySoulAgentIdentityPolicy(policy soulAgentEffectivePolicy, identity *models.SoulAgentIdentity) soulAgentEffectivePolicy {
	if v := strings.ToLower(strings.TrimSpace(identity.PolicyVersion)); v != "" {
		policy.Version = v
		policy.Migration.State = models.SoulPolicyMigrationStatePersistedV1
	}
	if v := strings.ToLower(strings.TrimSpace(identity.AnchorState)); v != "" {
		policy.AnchorState = v
	} else if soulIdentityHasOnchainAnchor(identity) {
		policy.AnchorState = models.SoulAnchorStateImmutableOnchain
	}
	if v := strings.ToLower(strings.TrimSpace(identity.OperationalBinding)); v != "" {
		policy.OperationalBinding = v
	}
	if v := strings.ToLower(strings.TrimSpace(identity.CapabilityPolicyVersion)); v != "" {
		policy.CapabilityPolicyVersion = v
	}
	if v := strings.ToLower(strings.TrimSpace(identity.CallerAccessPaymentPolicyVersion)); v != "" {
		policy.CallerAccessPaymentPolicyVersion = v
	}
	if strings.TrimSpace(identity.PolicyVersion) != "" {
		policy.Capabilities.Email.DefaultAllowed = identity.EmailDefaultAllowed
	}
	if v := strings.ToLower(strings.TrimSpace(identity.PhoneEntitlementStatus)); v != "" {
		policy.Capabilities.Phone.EntitlementStatus = v
	}
	entitled := soulPhoneEntitlementActive(policy.Capabilities.Phone.EntitlementStatus)
	policy.Capabilities.Phone.SMSAllowed = identity.SMSAllowed && entitled
	policy.Capabilities.Phone.VoiceAllowed = identity.VoiceAllowed && entitled
	if v := strings.ToLower(strings.TrimSpace(identity.PublicPaidCallerAccess)); v != "" {
		policy.CallerAccessPayment.PublicPaidCaller.Access = v
	}
	if v := strings.ToLower(strings.TrimSpace(identity.PolicyMigrationState)); v != "" {
		policy.Migration.State = v
	}
	return policy
}

func soulIdentityHasOnchainAnchor(identity *models.SoulAgentIdentity) bool {
	if identity == nil {
		return false
	}
	return strings.TrimSpace(identity.MintTxHash) != "" ||
		!identity.MintedAt.IsZero()
}

func soulPhoneEntitlementActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.SoulPhoneEntitlementProvisioned, models.SoulPhoneEntitlementPaid:
		return true
	default:
		return false
	}
}

func applyHostedBoundSoulPolicyDefaults(identity *models.SoulAgentIdentity) {
	if identity == nil {
		return
	}
	missingPolicyVersion := strings.TrimSpace(identity.PolicyVersion) == ""
	if strings.TrimSpace(identity.PolicyVersion) == "" {
		identity.PolicyVersion = models.SoulPolicyVersionHostedBoundSoulV1
	}
	if strings.TrimSpace(identity.AnchorState) == "" {
		if soulIdentityHasOnchainAnchor(identity) {
			identity.AnchorState = models.SoulAnchorStateImmutableOnchain
		} else {
			identity.AnchorState = models.SoulAnchorStateHostedOffchain
		}
	}
	if strings.TrimSpace(identity.OperationalBinding) == "" {
		identity.OperationalBinding = models.SoulOperationalBindingHostedBoundSoul
	}
	if strings.TrimSpace(identity.CapabilityPolicyVersion) == "" {
		identity.CapabilityPolicyVersion = models.SoulCapabilityPolicyVersionV1
	}
	if strings.TrimSpace(identity.CallerAccessPaymentPolicyVersion) == "" {
		identity.CallerAccessPaymentPolicyVersion = models.SoulCallerAccessPaymentPolicyVersionV1
	}
	if missingPolicyVersion {
		identity.EmailDefaultAllowed = true
	}
	if strings.TrimSpace(identity.PhoneEntitlementStatus) == "" {
		identity.PhoneEntitlementStatus = models.SoulPhoneEntitlementNotEntitled
	}
	if strings.TrimSpace(identity.PublicPaidCallerAccess) == "" {
		identity.PublicPaidCallerAccess = models.SoulPublicPaidCallerAccessDenied
	}
	if strings.TrimSpace(identity.PolicyMigrationState) == "" {
		identity.PolicyMigrationState = models.SoulPolicyMigrationStatePersistedV1
	}
}

func applyProvisionedPhonePolicy(identity *models.SoulAgentIdentity) {
	applyHostedBoundSoulPolicyDefaults(identity)
	if identity == nil {
		return
	}
	identity.PhoneEntitlementStatus = models.SoulPhoneEntitlementProvisioned
	identity.SMSAllowed = true
	identity.VoiceAllowed = true
}

func applyPhonePolicyNotEntitled(identity *models.SoulAgentIdentity) {
	applyHostedBoundSoulPolicyDefaults(identity)
	if identity == nil {
		return
	}
	identity.PhoneEntitlementStatus = models.SoulPhoneEntitlementNotEntitled
	identity.SMSAllowed = false
	identity.VoiceAllowed = false
}

func (s *Server) persistSoulAgentPolicyFields(ctx context.Context, identity *models.SoulAgentIdentity, now time.Time) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	update := &models.SoulAgentIdentity{
		AgentID:                          identity.AgentID,
		PolicyVersion:                    identity.PolicyVersion,
		AnchorState:                      identity.AnchorState,
		OperationalBinding:               identity.OperationalBinding,
		CapabilityPolicyVersion:          identity.CapabilityPolicyVersion,
		CallerAccessPaymentPolicyVersion: identity.CallerAccessPaymentPolicyVersion,
		EmailDefaultAllowed:              identity.EmailDefaultAllowed,
		PhoneEntitlementStatus:           identity.PhoneEntitlementStatus,
		SMSAllowed:                       identity.SMSAllowed,
		VoiceAllowed:                     identity.VoiceAllowed,
		PublicPaidCallerAccess:           identity.PublicPaidCallerAccess,
		PolicyMigrationState:             identity.PolicyMigrationState,
		UpdatedAt:                        now.UTC(),
	}
	_ = update.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(update).IfExists().Update(
		"PolicyVersion",
		"AnchorState",
		"OperationalBinding",
		"CapabilityPolicyVersion",
		"CallerAccessPaymentPolicyVersion",
		"EmailDefaultAllowed",
		"PhoneEntitlementStatus",
		"SMSAllowed",
		"VoiceAllowed",
		"PublicPaidCallerAccess",
		"PolicyMigrationState",
		"UpdatedAt",
	); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to persist soul policy"}
	}
	return nil
}

func enforceSoulCommCapabilityPolicy(identity *models.SoulAgentIdentity, req validatedSoulCommSendRequest, metrics *soulCommSendMetrics) *apptheory.AppTheoryError {
	policy := effectiveSoulAgentPolicy(identity)
	switch strings.TrimSpace(req.channel) {
	case commChannelEmail:
		if policy.Capabilities.Email.DefaultAllowed {
			return nil
		}
	case commChannelSMS:
		if policy.Capabilities.Phone.SMSAllowed {
			return nil
		}
	case commChannelVoice:
		if policy.Capabilities.Phone.VoiceAllowed {
			return nil
		}
	default:
		return nil
	}
	metrics.status = commMetricEntitlementRequired
	return apptheory.NewAppTheoryError(commCodeEntitlementRequired, "channel entitlement required").WithStatusCode(http.StatusForbidden)
}
