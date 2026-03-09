package controlplane

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "phone provider is not configured"}
		}
		nums, err := s.telnyxSearchNums(ctx.Context(), strings.TrimSpace(req.CountryCode), 5)
		if err != nil {
			log.Printf("controlplane: soul phone search failed agent=%s country=%s: %v", agentIDHex, strings.TrimSpace(req.CountryCode), err)
		}
		if err != nil || len(nums) == 0 {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to find available phone numbers"}
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

	regMap, _, digest, appErr := s.buildSoulProvisionPhoneRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:        desired,
		ENSName:            strings.TrimSpace(identity.LocalID) + ".lessersoul.eth",
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
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	number := strings.TrimSpace(req.Number)
	if number == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "number is required"}
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

func (s *Server) validateSoulProvisionPhoneNumberAvailability(ctx context.Context, agentIDHex string, number string) *apptheory.AppError {
	phoneIdx := &models.SoulPhoneAgentIndex{Phone: number}
	_ = phoneIdx.UpdateKeys()

	var existingIdx models.SoulPhoneAgentIndex
	lookupErr := s.store.DB.WithContext(ctx).
		Model(&models.SoulPhoneAgentIndex{}).
		Where("PK", "=", phoneIdx.PK).
		Where("SK", "=", phoneIdx.SK).
		First(&existingIdx)
	if lookupErr == nil && strings.TrimSpace(existingIdx.AgentID) != "" && !strings.EqualFold(strings.TrimSpace(existingIdx.AgentID), agentIDHex) {
		return &apptheory.AppError{Code: "app.conflict", Message: "phone number is already provisioned"}
	}
	if lookupErr != nil && !theoryErrors.IsNotFound(lookupErr) {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to validate phone mapping"}
	}
	return nil
}

func (s *Server) prepareSoulProvisionPhoneChannel(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	expectedVersion int,
	issuedAt time.Time,
	number string,
	selfSig string,
) (map[string]any, *soul.RegistrationFileV3, *apptheory.AppError) {
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx, agentIDHex, identity)
	if appErr != nil {
		return nil, nil, appErr
	}

	regMap, regV3, digest, appErr := s.buildSoulProvisionPhoneRegistration(ctx, baseReg, baseVersion, agentIDHex, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:        number,
		ENSName:            strings.TrimSpace(identity.LocalID) + ".lessersoul.eth",
		IssuedAt:           issuedAt.UTC(),
		ExpectedPrev:       expectedVersion,
		NextVersion:        expectedVersion + 1,
		SelfAttestationHex: selfSig,
	})
	if appErr != nil {
		return nil, nil, appErr
	}
	if verifyErr := verifyEthereumSignatureBytes(identity.Wallet, digest, selfSig); verifyErr != nil {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
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
	return nil, true, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
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
	if s.telnyxOrderNumber == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "phone provider is not configured"}
	}
	if _, orderErr := s.telnyxOrderNumber(ctx.Context(), number); orderErr != nil {
		log.Printf("controlplane: soul phone provision failed agent=%s number=%s: %v", agentIDHex, number, orderErr)
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to provision phone number"}
	}

	caps := extractCapabilityNames(regMap)
	capsNorm, appErr := normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, caps)
	if appErr != nil {
		return nil, appErr
	}

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

	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.channel.phone.provision",
		Target:    fmt.Sprintf("soul_agent:%s:channel:phone", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusCreated, soulProvisionPhoneConfirmResponse{
		Version:             "1",
		Number:              number,
		RegistrationVersion: publishedVersion,
	})
}

func upsertProvisionedPhoneChannel(ctx context.Context, s *Server, agentIDHex string, number string, now time.Time) *apptheory.AppError {
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
		return &apptheory.AppError{Code: "app.internal", Message: "failed to record phone channel"}
	}
	return nil
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
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to load phone channel"}
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
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update phone channel"}
	}

	// Remove reverse lookup index so the number no longer resolves.
	idx := &models.SoulPhoneAgentIndex{Phone: strings.TrimSpace(ch.Identifier), AgentID: agentIDHex}
	_ = idx.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(idx).Delete()

	// Best-effort: clear phone field from ENS resolution record (if it exists).
	ensName := strings.TrimSpace(identity.LocalID) + ".lessersoul.eth"
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

	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.channel.phone.deprovision",
		Target:    fmt.Sprintf("soul_agent:%s:channel:phone", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

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

func (s *Server) buildSoulProvisionPhoneRegistration(ctx context.Context, base map[string]any, baseVersion string, agentIDHex string, identity *models.SoulAgentIdentity, input soulProvisionPhoneBuildInput) (reg map[string]any, regV3 *soul.RegistrationFileV3, digest []byte, appErr *apptheory.AppError) {
	_ = ctx
	if s == nil || identity == nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
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
	ensureProvisionENSChannel(ch, input.ENSName)
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
