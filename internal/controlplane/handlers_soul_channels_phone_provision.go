package controlplane

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulProvisionPhoneBeginRequest struct {
	CountryCode string `json:"country_code,omitempty"`
	Number      string `json:"number,omitempty"`
}

type soulProvisionPhoneBeginResponse struct {
	Version         string         `json:"version"`
	Number          string         `json:"number"`
	DigestHex       string         `json:"digest_hex"`
	IssuedAt        string         `json:"issued_at"`
	ExpectedVersion int            `json:"expected_version"`
	NextVersion     int            `json:"next_version"`
	Registration    map[string]any `json:"registration"`
}

type soulProvisionPhoneConfirmRequest struct {
	Number string `json:"number,omitempty"`

	IssuedAt        string `json:"issued_at"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
	SelfAttestation string `json:"self_attestation"`
}

type soulProvisionPhoneConfirmResponse struct {
	Version             string `json:"version"`
	Number              string `json:"number"`
	RegistrationVersion int    `json:"registration_version"`
}

func (s *Server) handleSoulBeginProvisionPhoneChannel(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentIDHex, identity, appErr := s.requireSoulProvisionIdentity(ctx)
	if appErr != nil {
		return nil, appErr
	}

	var req soulProvisionPhoneBeginRequest
	if len(ctx.Request.Body) > 0 {
		if err := httpx.ParseJSON(ctx, &req); err != nil {
			return nil, err
		}
	}

	desired := strings.TrimSpace(req.Number)
	if desired == "" {
		if s.telnyxSearchNums == nil {
			return nil, newAppTheoryError("app.conflict", "phone provider is not configured")
		}
		nums, err := s.telnyxSearchNums(ctx.Context(), strings.TrimSpace(req.CountryCode), 5)
		if err != nil {
			log.Printf("controlplane: soul phone search failed agent=%s country=%s: %v", agentIDHex, strings.TrimSpace(req.CountryCode), err)
		}
		if err != nil || len(nums) == 0 {
			return nil, newAppTheoryError("app.internal", "failed to find available phone numbers")
		}
		desired = strings.TrimSpace(nums[0])
	}

	// Load the current registration as the base document (v2 or v3).
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx.Context(), agentIDHex, identity)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	expectedVersion := identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1
	ensName, appErr := s.resolveSoulProvisionENSName(ctx.Context(), identity)
	if appErr != nil {
		return nil, appErr
	}

	regMap, _, digest, appErr := s.buildSoulProvisionPhoneRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:        desired,
		ENSName:            ensName,
		IssuedAt:           now,
		ExpectedPrev:       expectedVersion,
		NextVersion:        nextVersion,
		SelfAttestationHex: "",
	})
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulProvisionPhoneBeginResponse{
		Version:         "1",
		Number:          desired,
		DigestHex:       "0x" + hex.EncodeToString(digest),
		IssuedAt:        now.Format(time.RFC3339Nano),
		ExpectedVersion: expectedVersion,
		NextVersion:     nextVersion,
		Registration:    regMap,
	})
}

func (s *Server) handleSoulProvisionPhoneChannel(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentIDHex, identity, appErr := s.requireSoulProvisionIdentity(ctx)
	if appErr != nil {
		return nil, appErr
	}

	var req soulProvisionPhoneConfirmRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	expectedVersion, issuedAt, selfSig, appErr := parseSoulProvisionConfirm(req.ExpectedVersion, req.IssuedAt, req.SelfAttestation)
	if appErr != nil {
		return nil, appErr
	}

	// Retry-friendly: if the agent has already advanced and the phone channel exists, treat as idempotent success.
	if resp, ok, err := s.maybeRespondWithExistingPhoneProvision(ctx, agentIDHex, identity.SelfDescriptionVersion, expectedVersion); ok || err != nil {
		return resp, err
	}
	if expectedVersion != identity.SelfDescriptionVersion {
		return nil, newAppTheoryError("app.conflict", "version conflict; reload and try again")
	}

	number := strings.TrimSpace(req.Number)
	if number == "" {
		return nil, newAppTheoryError("app.bad_request", "number is required")
	}
	phoneAppErr := s.validateSoulProvisionPhoneNumberAvailability(ctx.Context(), agentIDHex, number)
	if phoneAppErr != nil {
		return nil, phoneAppErr
	}

	regMap, regV3, appErr := s.prepareSoulProvisionPhoneChannel(ctx.Context(), agentIDHex, identity, expectedVersion, issuedAt, number, selfSig)
	if appErr != nil {
		return nil, appErr
	}
	return s.finalizeSoulProvisionPhoneChannel(ctx, agentIDHex, identity, expectedVersion, number, regMap, regV3, selfSig)
}

func (s *Server) validateSoulProvisionPhoneNumberAvailability(ctx context.Context, agentIDHex string, number string) *apptheory.AppTheoryError {
	phoneIdx := &models.SoulPhoneAgentIndex{Phone: number}
	_ = phoneIdx.UpdateKeys()
	return s.validateSoulProvisionIndexAvailability(ctx, &models.SoulPhoneAgentIndex{}, phoneIdx.PK, phoneIdx.SK, agentIDHex, "phone number is already provisioned", "failed to validate phone mapping", func() any {
		return &models.SoulPhoneAgentIndex{}
	}, func(existing any) string {
		if idx, ok := existing.(*models.SoulPhoneAgentIndex); ok && idx != nil {
			return idx.AgentID
		}
		return ""
	})
}

func (s *Server) prepareSoulProvisionPhoneChannel(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	expectedVersion int,
	issuedAt time.Time,
	number string,
	selfSig string,
) (map[string]any, *soul.RegistrationFileV3, *apptheory.AppTheoryError) {
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx, agentIDHex, identity)
	if appErr != nil {
		return nil, nil, appErr
	}
	ensName, appErr := s.resolveSoulProvisionENSName(ctx, identity)
	if appErr != nil {
		return nil, nil, appErr
	}

	regMap, regV3, digest, appErr := s.buildSoulProvisionPhoneRegistration(ctx, baseReg, baseVersion, agentIDHex, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:        number,
		ENSName:            ensName,
		IssuedAt:           issuedAt.UTC(),
		ExpectedPrev:       expectedVersion,
		NextVersion:        expectedVersion + 1,
		SelfAttestationHex: selfSig,
	})
	if appErr != nil {
		return nil, nil, appErr
	}
	if verifyErr := verifyEthereumSignatureBytes(identity.Wallet, digest, selfSig); verifyErr != nil {
		return nil, nil, newAppTheoryError("app.bad_request", "invalid registration signature")
	}
	return regMap, regV3, nil
}

func (s *Server) maybeRespondWithExistingPhoneProvision(ctx *apptheory.Context, agentIDHex string, currentVersion int, expectedVersion int) (*apptheory.Response, bool, error) {
	if expectedVersion >= currentVersion {
		return nil, false, nil
	}
	if identifier := lookupProvisionedChannelIdentifier(ctx.Context(), s, agentIDHex, "CHANNEL#phone"); identifier != "" {
		resp, err := apptheory.JSON(http.StatusOK, soulProvisionPhoneConfirmResponse{
			Version:             "1",
			Number:              identifier,
			RegistrationVersion: currentVersion,
		})
		return resp, true, err
	}
	return nil, true, newAppTheoryError("app.conflict", "version conflict; reload and try again")
}

func (s *Server) finalizeSoulProvisionPhoneChannel(
	ctx *apptheory.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	expectedVersion int,
	number string,
	regMap map[string]any,
	regV3 *soul.RegistrationFileV3,
	selfSig string,
) (*apptheory.Response, error) {
	baseURL := soulCommRequestBaseURL(ctx, s.cfg.PublicBaseURL)
	if baseURL == "" {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	webhookURL := baseURL + "/webhooks/comm/sms/inbound"

	if s.telnyxOrderNumber == nil {
		return nil, newAppTheoryError("app.conflict", "phone provider is not configured")
	}
	if s.telnyxUpdateProfile == nil {
		return nil, newAppTheoryError("app.conflict", "phone provider webhook configuration is not configured")
	}
	if _, orderErr := s.telnyxOrderNumber(ctx.Context(), number); orderErr != nil {
		log.Printf("controlplane: soul phone provision failed agent=%s number=%s: %v", agentIDHex, number, orderErr)
		return nil, newAppTheoryError("app.internal", "failed to provision phone number")
	}
	if updateErr := s.telnyxUpdateProfile(ctx.Context(), webhookURL); updateErr != nil {
		log.Printf("controlplane: soul phone webhook config failed agent=%s number=%s: %v", agentIDHex, number, updateErr)
		return nil, newAppTheoryError("app.internal", "failed to provision phone number")
	}

	caps := extractCapabilityNames(regMap)
	capsNorm := normalizeSoulCapabilitiesLoose(caps)

	regBytes, regSHA256, claimLevels, changeSummary, appErr := buildProvisionRegistrationPayload(regMap)
	if appErr != nil {
		return nil, appErr
	}
	now := time.Now().UTC()
	publishedVersion, pubErr := s.publishSoulAgentRegistrationV3(ctx.Context(), agentIDHex, identity, regV3, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, &expectedVersion, now)
	if pubErr != nil {
		return nil, pubErr
	}

	_ = s.syncSoulV3StateFromRegistration(ctx.Context(), agentIDHex, identity, regV3, now)
	if appErr := upsertProvisionedPhoneChannel(ctx.Context(), s, agentIDHex, number, now); appErr != nil {
		return nil, appErr
	}
	applyProvisionedPhonePolicy(identity)
	if appErr := s.persistSoulAgentPolicyFields(ctx.Context(), identity, now); appErr != nil {
		return nil, appErr
	}

	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.channel.phone.provision",
		Target:    fmt.Sprintf("soul_agent:%s:channel:phone", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusCreated, soulProvisionPhoneConfirmResponse{
		Version:             "1",
		Number:              number,
		RegistrationVersion: publishedVersion,
	})
}

func upsertProvisionedPhoneChannel(ctx context.Context, s *Server, agentIDHex string, number string, now time.Time) *apptheory.AppTheoryError {
	channel := &models.SoulAgentChannel{
		AgentID:       agentIDHex,
		ChannelType:   models.SoulChannelTypePhone,
		Identifier:    number,
		Provider:      "telnyx",
		Verified:      true,
		VerifiedAt:    now,
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
		Capabilities:  []string{"sms-receive", "sms-send", "voice-receive", "voice-send"},
		UpdatedAt:     now,
	}
	_ = channel.UpdateKeys()
	if createErr := s.store.DB.WithContext(ctx).Model(channel).CreateOrUpdate(); createErr != nil {
		return newAppTheoryError("app.internal", "failed to record phone channel")
	}
	return s.ensureSoulPhoneAgentIndex(ctx, &models.SoulPhoneAgentIndex{Phone: number, AgentID: agentIDHex})
}

func (s *Server) handleSoulDeprovisionPhoneChannel(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	ch, chErr := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx.Context(), agentIDHex, "CHANNEL#phone")
	if chErr != nil {
		if theoryErrors.IsNotFound(chErr) {
			return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
		}
		return nil, newAppTheoryError("app.internal", "failed to load phone channel")
	}
	if ch == nil || strings.TrimSpace(ch.Identifier) == "" {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}
	if !ch.DeprovisionedAt.IsZero() || strings.TrimSpace(ch.Status) == models.SoulChannelStatusDecommissioned {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}

	if s.telnyxRelease != nil {
		_ = s.telnyxRelease(ctx.Context(), strings.TrimSpace(ch.Identifier))
	}

	now := time.Now().UTC()
	ch.Status = models.SoulChannelStatusDecommissioned
	ch.DeprovisionedAt = now
	ch.Verified = false
	ch.UpdatedAt = now
	_ = ch.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(ch).CreateOrUpdate(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to update phone channel")
	}

	return s.finalizeSoulDeprovisionPhoneChannel(ctx, agentIDHex, identity, ch, now)
}

func (s *Server) finalizeSoulDeprovisionPhoneChannel(ctx *apptheory.Context, agentIDHex string, identity *models.SoulAgentIdentity, ch *models.SoulAgentChannel, now time.Time) (*apptheory.Response, error) {
	// Remove reverse lookup index so the number no longer resolves.
	idx := &models.SoulPhoneAgentIndex{Phone: strings.TrimSpace(ch.Identifier), AgentID: agentIDHex}
	_ = idx.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(idx).Delete()
	applyPhonePolicyNotEntitled(identity)
	if appErr := s.persistSoulAgentPolicyFields(ctx.Context(), identity, now); appErr != nil {
		return nil, appErr
	}

	// Best-effort: clear phone field from ENS resolution record (if it exists).
	ensName, appErr := s.resolveSoulProvisionENSName(ctx.Context(), identity)
	if appErr == nil {
		res := &models.SoulAgentENSResolution{ENSName: ensName}
		_ = res.UpdateKeys()
		var existing models.SoulAgentENSResolution
		loadResolutionErr := s.store.DB.WithContext(ctx.Context()).
			Model(&models.SoulAgentENSResolution{}).
			Where("PK", "=", res.PK).
			Where("SK", "=", res.SK).
			First(&existing)
		if loadResolutionErr == nil {
			existing.Phone = ""
			existing.UpdatedAt = now
			_ = existing.UpdateKeys()
			_ = s.store.DB.WithContext(ctx.Context()).Model(&existing).CreateOrUpdate()
		}
	}

	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.channel.phone.deprovision",
		Target:    fmt.Sprintf("soul_agent:%s:channel:phone", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}

type soulProvisionPhoneBuildInput struct {
	PhoneNumber        string
	ENSName            string
	IssuedAt           time.Time
	ExpectedPrev       int
	NextVersion        int
	SelfAttestationHex string
}

func (s *Server) buildSoulProvisionPhoneRegistration(ctx context.Context, base map[string]any, baseVersion string, agentIDHex string, identity *models.SoulAgentIdentity, input soulProvisionPhoneBuildInput) (reg map[string]any, regV3 *soul.RegistrationFileV3, digest []byte, appErr *apptheory.AppTheoryError) {
	_ = ctx
	if s == nil || identity == nil {
		return nil, nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	reg, appErr = prepareSoulProvisionRegistrationBase(s, base, baseVersion, agentIDHex, input.ExpectedPrev, input.NextVersion)
	if appErr != nil {
		return nil, nil, nil, appErr
	}
	issuedAt := input.IssuedAt.UTC().Format(time.RFC3339Nano)
	reg["updated"] = issuedAt
	reg["changeSummary"] = "Provision phone channel"
	setProvisionSelfAttestation(reg, input.SelfAttestationHex)
	ch := cloneProvisionChannels(reg)
	s.ensureProvisionENSChannel(ch, input.ENSName)
	ch["phone"] = map[string]any{
		"number":       strings.TrimSpace(input.PhoneNumber),
		"provider":     "telnyx",
		"capabilities": []any{"sms-receive", "sms-send", "voice-receive", "voice-send"},
		"verified":     true,
		"verifiedAt":   issuedAt,
	}
	reg["channels"] = ch

	digest, appErr = computeSoulRegistrationSelfAttestationDigest(reg)
	if appErr != nil {
		return nil, nil, nil, appErr
	}
	regV3, appErr = parseSoulProvisionRegistrationV3(reg)
	if appErr != nil {
		return nil, nil, nil, appErr
	}
	return reg, regV3, digest, nil
}
