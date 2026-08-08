package controlplane

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const hostedGenesisEmailRecoveryDelay = 30 * time.Second

type hostedGenesisEmailPlan struct {
	identity           *models.SoulAgentIdentity
	agentID            string
	address            soulProvisionManagedEmailAddress
	forwardingAddress  string
	passParamName      string
	password           string
	passwordPreviously bool
	channel            *models.SoulAgentChannel
}

// ensureHostedGenesisRequiredEmail enforces the hosted-agent creation invariant:
// an instance-trust agent is not published until its managed email channel is
// usable. Wallet-principal agents retain the explicit signed provisioning flow.
func (s *Server) ensureHostedGenesisRequiredEmail(ctx *apptheory.Context, finalizeCtx mintConversationFinalizeContext) *apptheory.AppTheoryError {
	if !isExplicitInstanceTrustAuthority(finalizeCtx.reg, finalizeCtx.identity) {
		return nil
	}
	if ctx == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if s != nil && s.hostedGenesisEmailProvisioner != nil {
		return s.hostedGenesisEmailProvisioner(ctx, finalizeCtx.identity, finalizeCtx.inst)
	}
	return s.ensureHostedGenesisEmailChannel(ctx, finalizeCtx.identity, finalizeCtx.inst)
}

func (s *Server) ensureHostedGenesisEmailChannel(ctx *apptheory.Context, identity *models.SoulAgentIdentity, inst *models.Instance) *apptheory.AppTheoryError {
	plan, planErr := s.prepareHostedGenesisEmailPlan(ctx, identity, inst)
	if planErr != nil {
		return planErr
	}
	if done, reconcileErr := s.reconcileHostedGenesisEmailPlan(ctx, plan); done || reconcileErr != nil {
		return reconcileErr
	}
	createdClaim, reserveErr := s.reserveHostedGenesisEmailClaim(ctx.Context(), plan)
	if reserveErr != nil {
		return reserveErr
	}
	providerCreated, providerErr := s.ensureHostedGenesisProviderMailbox(ctx.Context(), plan.channel, plan.address.ProviderLocalPart, identity.LocalID, plan.password, createdClaim)
	if providerErr != nil {
		return providerErr
	}
	if forwardingErr := s.ensureHostedGenesisEmailForwarding(ctx.Context(), plan, providerCreated); forwardingErr != nil {
		return forwardingErr
	}
	return s.activateHostedGenesisEmailPlan(ctx, plan)
}

func (s *Server) prepareHostedGenesisEmailPlan(ctx *apptheory.Context, identity *models.SoulAgentIdentity, inst *models.Instance) (*hostedGenesisEmailPlan, *apptheory.AppTheoryError) {
	if !hostedGenesisEmailInputsValid(s, ctx, identity, inst) {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	address, addressErr := buildSoulProvisionManagedEmailAddress(identity.LocalID, inst.Slug, "")
	if addressErr != nil {
		return nil, addressErr
	}
	forwardingAddress := soulEmailInboundForwardingAddress(address.ProviderLocalPart, s.cfg.SoulEmailInboundDomain)
	if forwardingAddress == "" {
		return nil, newAppTheoryError("app.conflict", "email inbound bridge is not configured")
	}
	if s.migaduCreateEmail == nil || s.migaduUpdateEmail == nil || s.migaduForwarding == nil {
		return nil, newAppTheoryError("app.conflict", "email provider is not configured")
	}

	agentID := strings.ToLower(strings.TrimSpace(identity.AgentID))
	if availabilityErr := s.validateSoulProvisionEmailAddressAvailability(ctx.Context(), agentID, address.Address); availabilityErr != nil {
		return nil, availabilityErr
	}

	existing, loadErr := s.loadExistingSoulChannel(ctx.Context(), agentID, models.SoulChannelTypeEmail)
	if loadErr != nil {
		return nil, loadErr
	}
	passParamName := s.soulAgentEmailPasswordSSMParam(agentID)
	passwordExisted := false
	if s.ssmGetParameter != nil {
		password, err := s.ssmGetParameter(ctx.Context(), passParamName)
		passwordExisted = err == nil && strings.TrimSpace(password) != ""
	}
	password, passwordErr := s.ensureSoulAgentEmailPassword(ctx.Context(), passParamName)
	if passwordErr != nil {
		return nil, passwordErr
	}
	return &hostedGenesisEmailPlan{
		identity:           identity,
		agentID:            agentID,
		address:            address,
		forwardingAddress:  forwardingAddress,
		passParamName:      passParamName,
		password:           password,
		passwordPreviously: passwordExisted,
		channel:            existing,
	}, nil
}

func hostedGenesisEmailInputsValid(s *Server, ctx *apptheory.Context, identity *models.SoulAgentIdentity, inst *models.Instance) bool {
	return s != nil && s.store != nil && ctx != nil && identity != nil && inst != nil
}

func (s *Server) reconcileHostedGenesisEmailPlan(ctx *apptheory.Context, plan *hostedGenesisEmailPlan) (bool, *apptheory.AppTheoryError) {
	if plan.channel == nil {
		return false, nil
	}
	if claimErr := validateHostedGenesisEmailClaim(plan.channel, plan.agentID, plan.address.Address, plan.passParamName); claimErr != nil {
		return true, claimErr
	}
	if hostedGenesisEmailChannelHealthy(plan.channel) && plan.passwordPreviously {
		if err := s.migaduForwarding(ctx.Context(), plan.address.ProviderLocalPart, plan.forwardingAddress); err != nil {
			log.Printf("controlplane: hosted genesis email forwarding reconciliation failed agent=%s address=%s: %v", plan.agentID, plan.address.Address, err)
			return true, newAppTheoryError("app.internal", "failed to provision required email")
		}
		indexErr := s.upsertSoulV3ChannelIndexes(ctx.Context(), plan.identity, models.SoulChannelTypeEmail, plan.channel, &models.SoulEmailAgentIndex{Email: plan.address.Address, AgentID: plan.agentID}, nil, nil)
		return true, indexErr
	}
	if plan.channel.Status == models.SoulChannelStatusPaused && !plan.channel.UpdatedAt.IsZero() && time.Since(plan.channel.UpdatedAt) < hostedGenesisEmailRecoveryDelay {
		return true, newAppTheoryError("app.conflict", "email provisioning is already in progress")
	}
	return false, nil
}

func (s *Server) reserveHostedGenesisEmailClaim(ctx context.Context, plan *hostedGenesisEmailPlan) (bool, *apptheory.AppTheoryError) {
	if plan.channel != nil {
		return false, nil
	}
	plan.channel = provisionalHostedGenesisEmailChannel(plan.agentID, plan.address.Address, plan.passParamName, time.Now().UTC())
	if err := s.store.DB.WithContext(ctx).Model(plan.channel).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return false, newAppTheoryError("app.conflict", "email provisioning is already in progress")
		}
		return false, newAppTheoryError("app.internal", "failed to reserve required email")
	}
	return true, nil
}

func (s *Server) ensureHostedGenesisEmailForwarding(ctx context.Context, plan *hostedGenesisEmailPlan, providerCreated bool) *apptheory.AppTheoryError {
	if err := s.migaduForwarding(ctx, plan.address.ProviderLocalPart, plan.forwardingAddress); err == nil {
		return nil
	} else if providerCreated && s.migaduDeleteEmail != nil {
		if rollbackErr := s.migaduDeleteEmail(ctx, plan.address.ProviderLocalPart); rollbackErr != nil {
			log.Printf("controlplane: hosted genesis email rollback failed agent=%s address=%s: %v", plan.agentID, plan.address.Address, rollbackErr)
		}
	}
	log.Printf("controlplane: hosted genesis email forwarding failed agent=%s address=%s", plan.agentID, plan.address.Address)
	return newAppTheoryError("app.internal", "failed to provision required email")
}

func (s *Server) activateHostedGenesisEmailPlan(ctx *apptheory.Context, plan *hostedGenesisEmailPlan) *apptheory.AppTheoryError {
	now := time.Now().UTC()
	activateHostedGenesisEmailChannel(plan.channel, now)
	if updateErr := s.upsertSoulV3Channel(ctx.Context(), plan.identity, models.SoulChannelTypeEmail, plan.channel, &models.SoulEmailAgentIndex{Email: plan.address.Address, AgentID: plan.agentID}, nil, nil); updateErr != nil {
		return updateErr
	}
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.channel.email.provision.automatic",
		Target:    fmt.Sprintf("soul_agent:%s:channel:email", plan.agentID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})
	return nil
}

func validateHostedGenesisEmailClaim(existing *models.SoulAgentChannel, agentID string, address string, passParamName string) *apptheory.AppTheoryError {
	if existing == nil {
		return nil
	}
	copy := *existing
	_ = copy.UpdateKeys()
	if copy.AgentID != agentID || copy.ChannelType != models.SoulChannelTypeEmail || copy.Identifier != strings.ToLower(strings.TrimSpace(address)) || copy.Provider != "migadu" || copy.SecretRef != passParamName || copy.Status == models.SoulChannelStatusDecommissioned {
		return newAppTheoryError("app.conflict", "required email channel conflicts with existing state")
	}
	return nil
}

func hostedGenesisEmailChannelHealthy(channel *models.SoulAgentChannel) bool {
	return trustedManagedSoulChannelForIndex(channel) && strings.TrimSpace(channel.SecretRef) != ""
}

func provisionalHostedGenesisEmailChannel(agentID string, address string, passParamName string, now time.Time) *models.SoulAgentChannel {
	channel := &models.SoulAgentChannel{
		AgentID:     agentID,
		ChannelType: models.SoulChannelTypeEmail,
		Identifier:  address,
		Provider:    "migadu",
		Status:      models.SoulChannelStatusPaused,
		SecretRef:   passParamName,
		UpdatedAt:   now,
	}
	_ = channel.UpdateKeys()
	return channel
}

func activateHostedGenesisEmailChannel(channel *models.SoulAgentChannel, now time.Time) {
	channel.Verified = true
	channel.VerifiedAt = now
	channel.Status = models.SoulChannelStatusActive
	channel.ProvisionedAt = now
	channel.DeprovisionedAt = time.Time{}
	channel.Capabilities = []string{"receive", "send"}
	channel.Protocols = []string{"smtp", "imap"}
	channel.UpdatedAt = now
	_ = channel.UpdateKeys()
}

func (s *Server) ensureHostedGenesisProviderMailbox(ctx context.Context, channel *models.SoulAgentChannel, localPart string, displayName string, password string, createdClaim bool) (bool, *apptheory.AppTheoryError) {
	if createdClaim {
		if err := s.migaduCreateEmail(ctx, localPart, displayName, password); err != nil {
			if isMigaduStatus(err, http.StatusConflict) {
				channel.Status = models.SoulChannelStatusDecommissioned
				channel.DeprovisionedAt = time.Now().UTC()
				if updateErr := s.store.DB.WithContext(ctx).Model(channel).CreateOrUpdate(); updateErr != nil {
					log.Printf("controlplane: hosted genesis email collision state failed agent=%s address=%s: %v", channel.AgentID, channel.Identifier, updateErr)
				}
				return false, newAppTheoryError("app.conflict", soulEmailProvisionErrAddressTaken)
			}
			log.Printf("controlplane: hosted genesis email mailbox create failed agent=%s address=%s: %v", channel.AgentID, channel.Identifier, err)
			return false, newAppTheoryError("app.internal", "failed to provision required email")
		}
		return true, nil
	}

	if err := s.migaduUpdateEmail(ctx, localPart, password); err != nil {
		if !isMigaduStatus(err, http.StatusNotFound) {
			log.Printf("controlplane: hosted genesis email mailbox recovery failed agent=%s address=%s: %v", channel.AgentID, channel.Identifier, err)
			return false, newAppTheoryError("app.internal", "failed to provision required email")
		}
		if createErr := s.migaduCreateEmail(ctx, localPart, displayName, password); createErr != nil {
			log.Printf("controlplane: hosted genesis email mailbox recovery create failed agent=%s address=%s: %v", channel.AgentID, channel.Identifier, createErr)
			return false, newAppTheoryError("app.internal", "failed to provision required email")
		}
		return true, nil
	}
	return false, nil
}
