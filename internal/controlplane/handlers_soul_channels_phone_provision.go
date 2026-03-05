package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if s == nil || s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if identity.SelfDescriptionVersion <= 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet published; update registration first"}
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
		PhoneNumber:       desired,
		ENSName:           strings.TrimSpace(identity.LocalID) + ".lessersoul.eth",
		IssuedAt:          now,
		ExpectedPrev:      expectedVersion,
		NextVersion:       nextVersion,
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
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if s == nil || s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if identity.SelfDescriptionVersion <= 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet published; update registration first"}
	}

	var req soulProvisionPhoneConfirmRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	if req.ExpectedVersion == nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is required"}
	}
	expectedVersion := *req.ExpectedVersion
	if expectedVersion < 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is invalid"}
	}

	issuedAtRaw := strings.TrimSpace(req.IssuedAt)
	if issuedAtRaw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at is required"}
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if err != nil {
		issuedAt, err = time.Parse(time.RFC3339, issuedAtRaw)
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at must be an RFC3339 timestamp"}
	}

	selfSig := strings.TrimSpace(req.SelfAttestation)
	if selfSig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "self_attestation is required"}
	}

	// Retry-friendly: if the agent has already advanced and the phone channel exists, treat as idempotent success.
	if expectedVersion < identity.SelfDescriptionVersion {
		if ch, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx.Context(), agentIDHex, "CHANNEL#phone"); err == nil && ch != nil && strings.TrimSpace(ch.Identifier) != "" {
			return apptheory.JSON(http.StatusOK, soulProvisionPhoneConfirmResponse{
				Version:             "1",
				Number:              strings.TrimSpace(ch.Identifier),
				RegistrationVersion: identity.SelfDescriptionVersion,
			})
		}
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}
	if expectedVersion != identity.SelfDescriptionVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	number := strings.TrimSpace(req.Number)
	if number == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "number is required"}
	}

	// Ensure the number is not already mapped to another agent.
	phoneIdx := &models.SoulPhoneAgentIndex{Phone: number}
	_ = phoneIdx.UpdateKeys()
	var existingIdx models.SoulPhoneAgentIndex
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulPhoneAgentIndex{}).
		Where("PK", "=", phoneIdx.PK).
		Where("SK", "=", phoneIdx.SK).
		First(&existingIdx)
	if err == nil && strings.TrimSpace(existingIdx.AgentID) != "" && !strings.EqualFold(strings.TrimSpace(existingIdx.AgentID), agentIDHex) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "phone number is already provisioned"}
	}
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to validate phone mapping"}
	}

	// Load the current registration as the base document (v2 or v3).
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx.Context(), agentIDHex, identity)
	if appErr != nil {
		return nil, appErr
	}

	nextVersion := expectedVersion + 1

	regMap, regV3, digest, appErr := s.buildSoulProvisionPhoneRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:       number,
		ENSName:           strings.TrimSpace(identity.LocalID) + ".lessersoul.eth",
		IssuedAt:          issuedAt.UTC(),
		ExpectedPrev:      expectedVersion,
		NextVersion:       nextVersion,
		SelfAttestationHex: selfSig,
	})
	if appErr != nil {
		return nil, appErr
	}

	if err := verifyEthereumSignatureBytes(identity.Wallet, digest, selfSig); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	if s.telnyxOrderNumber == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "phone provider is not configured"}
	}
	if _, err := s.telnyxOrderNumber(ctx.Context(), number); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to provision phone number"}
	}

	regBytes, err := json.Marshal(regMap)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	regSHA256 := func() string {
		sum := sha256.Sum256(regBytes)
		return hex.EncodeToString(sum[:])
	}()

	// Capability indexing inputs.
	caps := extractCapabilityNames(regMap)
	capsNorm, appErr := normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, caps)
	if appErr != nil {
		return nil, appErr
	}
	claimLevels := extractCapabilityClaimLevels(regMap)
	changeSummary := extractStringField(regMap, "changeSummary")
	now := time.Now().UTC()

	publishedVersion, pubErr := s.publishSoulAgentRegistrationV3(ctx.Context(), agentIDHex, identity, regV3, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, &expectedVersion, now)
	if pubErr != nil {
		return nil, pubErr
	}

	// Best-effort: keep v3 channel/preferences state in sync.
	_ = s.syncSoulV3StateFromRegistration(ctx.Context(), agentIDHex, identity, regV3, now)

	channel := &models.SoulAgentChannel{
		AgentID:       agentIDHex,
		ChannelType:   models.SoulChannelTypePhone,
		Identifier:    number,
		Provider:      "telnyx",
		Verified:      true,
		VerifiedAt:    now,
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
		Capabilities:  []string{"sms-receive", "sms-send", "voice-receive"},
		UpdatedAt:     now,
	}
	_ = channel.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(channel).CreateOrUpdate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to record phone channel"}
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
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentENSResolution{}).
		Where("PK", "=", res.PK).
		Where("SK", "=", res.SK).
		First(&existing); err == nil {
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
	PhoneNumber       string
	ENSName           string
	IssuedAt          time.Time
	ExpectedPrev      int
	NextVersion       int
	SelfAttestationHex string
}

func (s *Server) buildSoulProvisionPhoneRegistration(ctx context.Context, base map[string]any, baseVersion string, agentIDHex string, identity *models.SoulAgentIdentity, input soulProvisionPhoneBuildInput) (reg map[string]any, regV3 *soul.RegistrationFileV3, digest []byte, appErr *apptheory.AppError) {
	if s == nil || identity == nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if base == nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	baseVersion = strings.TrimSpace(baseVersion)
	if baseVersion != "2" && baseVersion != "3" {
		return nil, nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "registration version is unsupported; update registration first"}
	}
	if input.ExpectedPrev < 0 || input.NextVersion <= 0 || input.NextVersion != input.ExpectedPrev+1 {
		return nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid expected_version"}
	}

	// Shallow copy base map so we can mutate fields for the new version.
	reg = make(map[string]any, len(base))
	for k, v := range base {
		reg[k] = v
	}

	// Upgrade to v3 and set version chain.
	reg["version"] = "3"
	if input.NextVersion <= 1 {
		delete(reg, "previousVersionUri")
	} else {
		prevKey := soulRegistrationVersionedS3Key(agentIDHex, input.NextVersion-1)
		reg["previousVersionUri"] = fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), prevKey)
	}

	issuedAt := input.IssuedAt.UTC().Format(time.RFC3339Nano)
	reg["updated"] = issuedAt
	reg["changeSummary"] = "Provision phone channel"

	// Ensure attestations object exists and set selfAttestation signature (confirm only).
	attAny, ok := reg["attestations"]
	att, ok2 := attAny.(map[string]any)
	if !ok || !ok2 || att == nil {
		att = map[string]any{}
	}
	selfSig := strings.TrimSpace(input.SelfAttestationHex)
	if selfSig == "" {
		selfSig = "0x00" // placeholder; digest excludes selfAttestation
	}
	att["selfAttestation"] = selfSig
	reg["attestations"] = att

	// Upsert channels object.
	chAny, _ := reg["channels"].(map[string]any)
	ch := map[string]any{}
	for k, v := range chAny {
		ch[k] = v
	}
	if _, ok := ch["ens"]; !ok && strings.TrimSpace(input.ENSName) != "" {
		ch["ens"] = map[string]any{
			"name":  strings.TrimSpace(input.ENSName),
			"chain": "mainnet",
		}
	}
	ch["phone"] = map[string]any{
		"number":       strings.TrimSpace(input.PhoneNumber),
		"provider":     "telnyx",
		"capabilities": []any{"sms-receive", "sms-send", "voice-receive"},
		"verified":     true,
		"verifiedAt":   issuedAt,
	}
	reg["channels"] = ch

	digest, appErr = computeSoulRegistrationSelfAttestationDigest(reg)
	if appErr != nil {
		return nil, nil, nil, appErr
	}

	regBytes, err := json.Marshal(reg)
	if err != nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	parsed, err := soul.ParseRegistrationFileV3(regBytes)
	if err != nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v3 registration schema"}
	}
	if err := parsed.Validate(); err != nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	return reg, parsed, digest, nil
}
