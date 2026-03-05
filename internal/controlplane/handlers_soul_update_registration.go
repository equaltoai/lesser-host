package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulUpdateRegistrationResponse struct {
	Agent   models.SoulAgentIdentity `json:"agent"`
	S3Key   string                   `json:"s3_key,omitempty"`
	Version int                      `json:"version,omitempty"`
}

func soulRegistrationS3Key(agentIDHex string) string {
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	return fmt.Sprintf("registry/v1/agents/%s/registration.json", agentIDHex)
}

func soulRegistrationVersionedS3Key(agentIDHex string, version int) string {
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	return fmt.Sprintf("registry/v1/agents/%s/versions/%d/registration.json", agentIDHex, version)
}

func extractStringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func extractStringSliceField(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			continue
		}
		out = append(out, strings.TrimSpace(s))
	}
	return out
}

func (s *Server) handleSoulAgentUpdateRegistration(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulRPCConfigured(); appErr != nil {
		return nil, appErr
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	agentIDHex, agentInt, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	regBytes, reg, expectedVersion, appErr := parseSoulUpdateRegistrationBody(ctx.Request.Body)
	if appErr != nil {
		return nil, appErr
	}

	walletNorm, capsNorm, selfSig, digest, appErr := s.validateSoulUpdateRegistrationDocument(ctx.Context(), reg, agentIDHex, agentInt, identity)
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytes(walletNorm, digest, selfSig); err != nil {
		log.Printf("controlplane: soul_integrity invalid_registration_signature agent=%s request_id=%s", agentIDHex, strings.TrimSpace(ctx.RequestID))
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	// Determine the schema version from the document.
	schemaVersion := strings.TrimSpace(extractStringField(reg, "version"))
	isV2 := schemaVersion == "2"
	isV3 := schemaVersion == "3"
	var regV2 *soul.RegistrationFileV2
	var regV3 *soul.RegistrationFileV3
	if isV2 {
		parsed, err := soul.ParseRegistrationFileV2(regBytes)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v2 registration schema"}
		}
		if err := parsed.Validate(); err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
		}
		regV2 = parsed
	} else if isV3 {
		parsed, err := soul.ParseRegistrationFileV3(regBytes)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v3 registration schema"}
		}
		if err := parsed.Validate(); err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
		}
		regV3 = parsed
	} else if schemaVersion != "" && schemaVersion != "1" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "unsupported registration version"}
	}

	regSHA256 := func() string {
		sum := sha256.Sum256(regBytes)
		return hex.EncodeToString(sum[:])
	}()

	now := time.Now().UTC()
	claimLevels := extractCapabilityClaimLevels(reg)

	nextVersion := 0
	s3Key := soulRegistrationS3Key(agentIDHex)
	if isV2 && regV2 != nil {
		changeSummary := extractStringField(reg, "changeSummary")
		publishedVersion, pubErr := s.publishSoulAgentRegistrationV2(ctx.Context(), agentIDHex, identity, regV2, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, expectedVersion, now)
		if pubErr != nil {
			return nil, pubErr
		}
		nextVersion = publishedVersion
	} else if isV3 && regV3 != nil {
		changeSummary := extractStringField(reg, "changeSummary")
		publishedVersion, pubErr := s.publishSoulAgentRegistrationV3(ctx.Context(), agentIDHex, identity, regV3, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, expectedVersion, now)
		if pubErr != nil {
			return nil, pubErr
		}
		nextVersion = publishedVersion

		if appErr := s.syncSoulV3StateFromRegistration(ctx.Context(), agentIDHex, identity, regV3, now); appErr != nil {
			return nil, appErr
		}
	} else {
		// Legacy (v1) publishing path.
		// Determine next version number by querying latest VERSION# item.
		var prevRegSHA256 string
		nextVersion, prevRegSHA256, appErr = s.getNextSoulAgentVersion(ctx.Context(), agentIDHex)
		if appErr != nil {
			return nil, appErr
		}

		// Publish to the versioned S3 path.
		versionedKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion)
		if err := s.soulPacks.PutObject(ctx.Context(), versionedKey, regBytes, "application/json", "private, max-age=0"); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to publish versioned registration"}
		}

		// Publish to the current S3 path.
		if err := s.soulPacks.PutObject(ctx.Context(), s3Key, regBytes, "application/json", "private, max-age=0"); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to publish registration"}
		}

		if appErr := s.updateSoulAgentCapabilities(ctx.Context(), identity, capsNorm, claimLevels, now, false); appErr != nil {
			return nil, appErr
		}

		// Create version record.
		changeSummary := extractStringField(reg, "changeSummary")
		versionRecord := &models.SoulAgentVersion{
			AgentID:                    agentIDHex,
			VersionNumber:              nextVersion,
			RegistrationUri:            fmt.Sprintf("s3://%s/%s", s.cfg.SoulPackBucketName, versionedKey),
			RegistrationSHA256:         regSHA256,
			PreviousRegistrationSHA256: strings.TrimSpace(prevRegSHA256),
			ChangeSummary:              changeSummary,
			SelfAttestation:            selfSig,
			CreatedAt:                  now,
		}
		if err := versionRecord.UpdateKeys(); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to record version history"}
		}
		if err := s.store.DB.WithContext(ctx.Context()).Model(versionRecord).Create(); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to record version history"}
		}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.registration.update",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	resp := soulUpdateRegistrationResponse{Agent: *identity, Version: nextVersion}
	if isOperator(ctx) {
		resp.S3Key = s3Key
	}
	return apptheory.JSON(http.StatusOK, resp)
}

func (s *Server) syncSoulV3StateFromRegistration(ctx context.Context, agentIDHex string, identity *models.SoulAgentIdentity, regV3 *soul.RegistrationFileV3, now time.Time) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil || regV3 == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))

	// Upsert/delete contact preferences.
	if regV3.ContactPreferences != nil {
		p := regV3.ContactPreferences
		windows := make([]models.SoulContactAvailabilityWindow, 0, len(p.Availability.Windows))
		for _, w := range p.Availability.Windows {
			windows = append(windows, models.SoulContactAvailabilityWindow{
				Days:      w.Days,
				StartTime: strings.TrimSpace(w.StartTime),
				EndTime:   strings.TrimSpace(w.EndTime),
			})
		}
		pref := &models.SoulAgentContactPreferences{
			AgentID:              agentIDHex,
			Preferred:            strings.TrimSpace(p.Preferred),
			Fallback:             strings.TrimSpace(p.Fallback),
			AvailabilitySchedule: strings.TrimSpace(p.Availability.Schedule),
			AvailabilityTimezone: strings.TrimSpace(p.Availability.Timezone),
			AvailabilityWindows:  windows,
			ResponseTarget:       strings.TrimSpace(p.ResponseExpectation.Target),
			ResponseGuarantee:    strings.TrimSpace(p.ResponseExpectation.Guarantee),
			RateLimits:           p.RateLimits,
			Languages:            p.Languages,
			ContentTypes:         p.ContentTypes,
			UpdatedAt:            now,
		}
		if p.FirstContact != nil {
			pref.FirstContactRequireSoul = p.FirstContact.RequireSoul
			pref.FirstContactRequireReputation = p.FirstContact.RequireReputation
			pref.FirstContactIntroductionExpected = p.FirstContact.IntroductionExpected
		}
		_ = pref.UpdateKeys()
		if err := s.store.DB.WithContext(ctx).Model(pref).CreateOrUpdate(); err != nil {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to update contact preferences"}
		}
	} else {
		pref := &models.SoulAgentContactPreferences{AgentID: agentIDHex}
		_ = pref.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(pref).Delete()
	}

	// Upsert/delete channel records and reverse indexes.
	ensureChannel := func(channelType string, desired *models.SoulAgentChannel, desiredEmailIndex *models.SoulEmailAgentIndex, desiredPhoneIndex *models.SoulPhoneAgentIndex, desiredENS *models.SoulAgentENSResolution) *apptheory.AppError {
		sk := fmt.Sprintf("CHANNEL#%s", channelType)
		existing, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx, agentIDHex, sk)
		if err != nil && !theoryErrors.IsNotFound(err) {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to read channel"}
		}

		// If identifier changed or channel removed, clean up old reverse indexes.
		if existing != nil && strings.TrimSpace(existing.Identifier) != "" && (desired == nil || !strings.EqualFold(strings.TrimSpace(existing.Identifier), strings.TrimSpace(desired.Identifier))) {
			switch channelType {
			case models.SoulChannelTypeEmail:
				old := &models.SoulEmailAgentIndex{Email: existing.Identifier, AgentID: agentIDHex}
				_ = old.UpdateKeys()
				_ = s.store.DB.WithContext(ctx).Model(old).Delete()
			case models.SoulChannelTypePhone:
				old := &models.SoulPhoneAgentIndex{Phone: existing.Identifier, AgentID: agentIDHex}
				_ = old.UpdateKeys()
				_ = s.store.DB.WithContext(ctx).Model(old).Delete()
			case models.SoulChannelTypeENS:
				old := &models.SoulAgentENSResolution{ENSName: existing.Identifier, AgentID: agentIDHex}
				_ = old.UpdateKeys()
				_ = s.store.DB.WithContext(ctx).Model(old).Delete()
			}
		}

		if desired == nil {
			if existing != nil {
				_ = s.store.DB.WithContext(ctx).Model(existing).Delete()
			}
			if (channelType == models.SoulChannelTypeEmail || channelType == models.SoulChannelTypePhone) && strings.TrimSpace(identity.Domain) != "" && strings.TrimSpace(identity.LocalID) != "" {
				idx := &models.SoulChannelAgentIndex{
					ChannelType: channelType,
					Domain:      strings.TrimSpace(identity.Domain),
					LocalID:     strings.TrimSpace(identity.LocalID),
					AgentID:     agentIDHex,
				}
				_ = idx.UpdateKeys()
				_ = s.store.DB.WithContext(ctx).Model(idx).Delete()
			}
			return nil
		}

		_ = desired.UpdateKeys()
		if err := s.store.DB.WithContext(ctx).Model(desired).CreateOrUpdate(); err != nil {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to update channel"}
		}
		if desiredEmailIndex != nil {
			_ = desiredEmailIndex.UpdateKeys()
			if err := s.store.DB.WithContext(ctx).Model(desiredEmailIndex).CreateOrUpdate(); err != nil {
				return &apptheory.AppError{Code: "app.internal", Message: "failed to update email index"}
			}
		}
		if desiredPhoneIndex != nil {
			_ = desiredPhoneIndex.UpdateKeys()
			if err := s.store.DB.WithContext(ctx).Model(desiredPhoneIndex).CreateOrUpdate(); err != nil {
				return &apptheory.AppError{Code: "app.internal", Message: "failed to update phone index"}
			}
		}
		if desiredENS != nil {
			_ = desiredENS.UpdateKeys()
			if err := s.store.DB.WithContext(ctx).Model(desiredENS).CreateOrUpdate(); err != nil {
				return &apptheory.AppError{Code: "app.internal", Message: "failed to update ens resolution"}
			}
		}
		if (channelType == models.SoulChannelTypeEmail || channelType == models.SoulChannelTypePhone) && strings.TrimSpace(identity.Domain) != "" && strings.TrimSpace(identity.LocalID) != "" {
			idx := &models.SoulChannelAgentIndex{
				ChannelType: channelType,
				Domain:      strings.TrimSpace(identity.Domain),
				LocalID:     strings.TrimSpace(identity.LocalID),
				AgentID:     agentIDHex,
			}
			_ = idx.UpdateKeys()
			if err := s.store.DB.WithContext(ctx).Model(idx).CreateOrUpdate(); err != nil {
				return &apptheory.AppError{Code: "app.internal", Message: "failed to update channel index"}
			}
		}
		return nil
	}

	// ENS
	var ensDesired *models.SoulAgentChannel
	var ensResolution *models.SoulAgentENSResolution
	// Email/phone values may be useful in ENS resolution material.
	emailAddress := ""
	phoneNumber := ""

	if regV3.Channels != nil && regV3.Channels.Email != nil {
		emailAddress = strings.TrimSpace(regV3.Channels.Email.Address)
	}
	if regV3.Channels != nil && regV3.Channels.Phone != nil {
		phoneNumber = strings.TrimSpace(regV3.Channels.Phone.Number)
	}

	if regV3.Channels != nil && regV3.Channels.ENS != nil {
		ens := regV3.Channels.ENS
		ensDesired = &models.SoulAgentChannel{
			AgentID:            agentIDHex,
			ChannelType:        models.SoulChannelTypeENS,
			Identifier:         strings.TrimSpace(ens.Name),
			ENSResolverAddress: strings.TrimSpace(ens.ResolverAddress),
			ENSChain:           strings.TrimSpace(ens.Chain),
			Status:             models.SoulChannelStatusActive,
			UpdatedAt:          now,
		}
		ensResolution = &models.SoulAgentENSResolution{
			ENSName:             strings.TrimSpace(ens.Name),
			AgentID:             agentIDHex,
			Wallet:              strings.TrimSpace(identity.Wallet),
			LocalID:             strings.TrimSpace(identity.LocalID),
			Domain:              strings.TrimSpace(identity.Domain),
			SoulRegistrationURI: s.soulMetaURI(agentIDHex),
			MCPEndpoint:         strings.TrimSpace(regV3.Endpoints.MCP),
			ActivityPubURI:      strings.TrimSpace(regV3.Endpoints.ActivityPub),
			Email:               emailAddress,
			Phone:               phoneNumber,
			Status:              strings.TrimSpace(identity.LifecycleStatus),
			UpdatedAt:           now,
		}
	}
	if appErr := ensureChannel(models.SoulChannelTypeENS, ensDesired, nil, nil, ensResolution); appErr != nil {
		return appErr
	}

	// Email
	var emailDesired *models.SoulAgentChannel
	var emailIndex *models.SoulEmailAgentIndex
	if regV3.Channels != nil && regV3.Channels.Email != nil {
		email := regV3.Channels.Email
		verifiedAt, _ := parseRFC3339Loose(email.VerifiedAt)
		emailDesired = &models.SoulAgentChannel{
			AgentID:      agentIDHex,
			ChannelType:  models.SoulChannelTypeEmail,
			Identifier:   strings.TrimSpace(email.Address),
			Capabilities: email.Capabilities,
			Protocols:    email.Protocols,
			Verified:     email.Verified,
			VerifiedAt:   verifiedAt,
			Status:       models.SoulChannelStatusActive,
			UpdatedAt:    now,
		}
		emailIndex = &models.SoulEmailAgentIndex{Email: strings.TrimSpace(email.Address), AgentID: agentIDHex}
	}
	if appErr := ensureChannel(models.SoulChannelTypeEmail, emailDesired, emailIndex, nil, nil); appErr != nil {
		return appErr
	}

	// Phone
	var phoneDesired *models.SoulAgentChannel
	var phoneIndex *models.SoulPhoneAgentIndex
	if regV3.Channels != nil && regV3.Channels.Phone != nil {
		phone := regV3.Channels.Phone
		verifiedAt, _ := parseRFC3339Loose(phone.VerifiedAt)
		phoneDesired = &models.SoulAgentChannel{
			AgentID:      agentIDHex,
			ChannelType:  models.SoulChannelTypePhone,
			Identifier:   strings.TrimSpace(phone.Number),
			Capabilities: phone.Capabilities,
			Provider:     strings.TrimSpace(phone.Provider),
			Verified:     phone.Verified,
			VerifiedAt:   verifiedAt,
			Status:       models.SoulChannelStatusActive,
			UpdatedAt:    now,
		}
		phoneIndex = &models.SoulPhoneAgentIndex{Phone: strings.TrimSpace(phone.Number), AgentID: agentIDHex}
	}
	if appErr := ensureChannel(models.SoulChannelTypePhone, phoneDesired, nil, phoneIndex, nil); appErr != nil {
		return appErr
	}

	return nil
}

func parseRFC3339Loose(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC(), true
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC(), true
	}
	return time.Time{}, false
}

func (s *Server) requireActiveSoulAgentWithDomainAccess(ctx *apptheory.Context, agentIDHex string) (*models.SoulAgentIdentity, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	effectiveStatus := strings.TrimSpace(identity.LifecycleStatus)
	if effectiveStatus == "" {
		effectiveStatus = strings.TrimSpace(identity.Status)
	}
	if effectiveStatus != models.SoulAgentStatusActive {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent is not active"}
	}

	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}
	return identity, nil
}

func parseSoulUpdateRegistrationBody(body []byte) ([]byte, map[string]any, *int, *apptheory.AppError) {
	regBytes := body

	var wrapper struct {
		Registration        json.RawMessage `json:"registration"`
		ExpectedVersion     *int            `json:"expected_version,omitempty"`
		ExpectedVersionAlt  *int            `json:"expectedVersion,omitempty"`
		ExpectedVersionAlt2 *int            `json:"expected_version_alt,omitempty"` // deprecated; kept for forward compatibility tests/tools
	}
	expectedVersion := (*int)(nil)
	if unmarshalErr := json.Unmarshal(body, &wrapper); unmarshalErr == nil && len(wrapper.Registration) > 0 {
		regBytes = wrapper.Registration
		expectedVersion = wrapper.ExpectedVersion
		if expectedVersion == nil {
			expectedVersion = wrapper.ExpectedVersionAlt
		}
	}

	var reg map[string]any
	if unmarshalErr := json.Unmarshal(regBytes, &reg); unmarshalErr != nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid JSON"}
	}
	return regBytes, reg, expectedVersion, nil
}

func (s *Server) validateSoulUpdateRegistrationDocument(
	ctx context.Context,
	reg map[string]any,
	agentIDHex string,
	agentInt *big.Int,
	identity *models.SoulAgentIdentity,
) (walletNorm string, capsNorm []string, selfSig string, digest []byte, appErr *apptheory.AppError) {
	if s == nil || identity == nil {
		return "", nil, "", nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if validateErr := validateSoulUpdateRegistrationIdentityFields(reg, agentIDHex, identity); validateErr != nil {
		return "", nil, "", nil, validateErr
	}

	walletNorm, appErr = s.normalizeSoulWalletAddress(ctx, extractStringField(reg, "wallet"))
	if appErr != nil {
		return "", nil, "", nil, appErr
	}

	att, selfSig, appErr := extractSoulUpdateRegistrationSelfAttestation(reg)
	if appErr != nil {
		return "", nil, "", nil, appErr
	}

	verifyErr := s.verifySoulAgentWalletOnChain(ctx, agentInt, walletNorm, identity)
	if verifyErr != nil {
		return "", nil, "", nil, verifyErr
	}

	digest, appErr = computeSoulUpdateRegistrationDigest(reg, att)
	if appErr != nil {
		return "", nil, "", nil, appErr
	}

	// Capabilities affect indexing; validate against the allowlist if configured.
	// v2 uses structured capabilities (array of objects with "capability" field);
	// v1 uses a flat string array. Extract capability names for both.
	caps := extractCapabilityNames(reg)
	capsNorm, appErr = normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, caps)
	if appErr != nil {
		return "", nil, "", nil, appErr
	}

	return walletNorm, capsNorm, selfSig, digest, nil
}

func validateSoulUpdateRegistrationIdentityFields(reg map[string]any, agentIDHex string, identity *models.SoulAgentIdentity) *apptheory.AppError {
	if identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	bodyAgentID := strings.ToLower(extractStringField(reg, "agentId"))
	if bodyAgentID == "" {
		bodyAgentID = strings.ToLower(extractStringField(reg, "agent_id"))
	}
	if !strings.EqualFold(bodyAgentID, agentIDHex) {
		return &apptheory.AppError{Code: "app.bad_request", Message: "agentId does not match path"}
	}

	bodyDomain := strings.ToLower(extractStringField(reg, "domain"))
	if bodyDomain == "" || !strings.EqualFold(bodyDomain, strings.TrimSpace(identity.Domain)) {
		return &apptheory.AppError{Code: "app.bad_request", Message: "domain does not match agent"}
	}

	bodyLocal := extractStringField(reg, "localId")
	if bodyLocal == "" {
		bodyLocal = extractStringField(reg, "local_id")
	}
	localNorm, err := soul.NormalizeLocalAgentID(bodyLocal)
	if err != nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}
	if !strings.EqualFold(localNorm, strings.TrimSpace(identity.LocalID)) {
		return &apptheory.AppError{Code: "app.bad_request", Message: "localId does not match agent"}
	}

	return nil
}

func extractSoulUpdateRegistrationSelfAttestation(reg map[string]any) (att map[string]any, selfSig string, appErr *apptheory.AppError) {
	attAny, ok := reg["attestations"]
	if !ok {
		return nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "attestations are required"}
	}
	att, ok = attAny.(map[string]any)
	if !ok {
		return nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "attestations must be an object"}
	}
	selfSig = extractStringField(att, "selfAttestation")
	if selfSig == "" {
		return nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "attestations.selfAttestation is required"}
	}
	return att, selfSig, nil
}

func computeSoulUpdateRegistrationDigest(reg map[string]any, att map[string]any) ([]byte, *apptheory.AppError) {
	// Compute canonical digest over the full JSON document, omitting attestations.selfAttestation.
	delete(att, "selfAttestation")

	unsignedBytes, err := json.Marshal(reg)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	return crypto.Keccak256(jcsBytes), nil
}

func (s *Server) verifySoulAgentWalletOnChain(ctx context.Context, agentInt *big.Int, walletNorm string, identity *models.SoulAgentIdentity) *apptheory.AppError {
	if s == nil || identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	contractAddr, _, appErr := s.soulRegistryContractAddress()
	if appErr != nil {
		return appErr
	}

	dial := s.dialEVM
	if dial == nil {
		dial = func(ctx context.Context, rpcURL string) (ethRPCClient, error) { return dialEthClient(ctx, rpcURL) }
	}
	client, err := dial(ctx, s.cfg.SoulRPCURL)
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to connect to rpc"}
	}
	defer client.Close()

	onChainWallet, err := s.soulRegistryGetAgentWallet(ctx, client, contractAddr, agentInt)
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to read agent wallet"}
	}
	if (onChainWallet == common.Address{}) {
		return &apptheory.AppError{Code: "app.conflict", Message: "agent is not minted"}
	}
	if !strings.EqualFold(onChainWallet.Hex(), walletNorm) {
		return &apptheory.AppError{Code: "app.bad_request", Message: "wallet does not match on-chain state"}
	}
	if !strings.EqualFold(walletNorm, strings.TrimSpace(identity.Wallet)) {
		return &apptheory.AppError{Code: "app.conflict", Message: "agent wallet is out of sync; record operation execution first"}
	}
	return nil
}

// extractCapabilityNames extracts capability name strings from both v1 flat arrays
// and v2 structured capability objects.
func extractCapabilityNames(reg map[string]any) []string {
	raw, ok := reg["capabilities"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			// v1: flat string array
			out = append(out, strings.TrimSpace(v))
		case map[string]any:
			// v2: structured object with "capability" field
			if cap, ok := v["capability"].(string); ok {
				out = append(out, strings.TrimSpace(cap))
			}
		}
	}
	return out
}

// extractCapabilityClaimLevels returns a map of capability name → claimLevel
// from v2 structured capability objects. V1 flat strings default to "self-declared".
func extractCapabilityClaimLevels(reg map[string]any) map[string]string {
	raw, ok := reg["capabilities"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			name := strings.ToLower(strings.TrimSpace(v))
			if name != "" {
				out[name] = "self-declared"
			}
		case map[string]any:
			if cap, ok := v["capability"].(string); ok {
				cl, _ := v["claimLevel"].(string)
				if cl == "" {
					cl, _ = v["claim_level"].(string)
				}
				if cl == "" {
					cl = "self-declared"
				}
				name := strings.ToLower(strings.TrimSpace(cap))
				if name != "" {
					out[name] = strings.ToLower(strings.TrimSpace(cl))
				}
			}
		}
	}
	return out
}

// getNextSoulAgentVersion returns the next version number, plus the sha256 digest
// recorded for the current "latest" version (if present) to allow building a tamper-evident chain.
func (s *Server) getNextSoulAgentVersion(ctx context.Context, agentIDHex string) (nextVersion int, prevRegistrationSHA256 string, appErr *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return 0, "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var items []*models.SoulAgentVersion
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentVersion{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "VERSION#").
		All(&items)
	if err != nil {
		return 0, "", &apptheory.AppError{Code: "app.internal", Message: "failed to read version history"}
	}

	max := 0
	prevHash := ""
	for _, it := range items {
		if it == nil {
			continue
		}
		if it.VersionNumber > max {
			max = it.VersionNumber
			prevHash = strings.TrimSpace(it.RegistrationSHA256)
		}
	}
	return max + 1, prevHash, nil
}

func (s *Server) validateSoulRegistrationPreviousVersionURI(reg *soul.RegistrationFileV2, agentIDHex string, nextVersion int) *apptheory.AppError {
	if s == nil || reg == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if nextVersion <= 1 {
		// First version: previousVersionUri must be empty/null.
		if reg.PreviousVersionURI != nil && strings.TrimSpace(*reg.PreviousVersionURI) != "" {
			log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=prev_uri_set_on_first", agentIDHex, nextVersion)
			return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri must be null for the first version"}
		}
		return nil
	}

	prevKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion-1)
	expected := fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), prevKey)
	if reg.PreviousVersionURI == nil || strings.TrimSpace(*reg.PreviousVersionURI) == "" {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=missing_prev_uri", agentIDHex, nextVersion)
		return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri is required for subsequent versions"}
	}
	if strings.TrimSpace(*reg.PreviousVersionURI) != expected {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=prev_uri_mismatch expected=%s got=%s", agentIDHex, nextVersion, expected, strings.TrimSpace(*reg.PreviousVersionURI))
		return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri does not match the expected previous version"}
	}
	return nil
}

func (s *Server) updateSoulAgentCapabilities(ctx context.Context, identity *models.SoulAgentIdentity, capsNorm []string, claimLevels map[string]string, now time.Time, skipTransitionValidation bool) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	oldCaps := normalizeSoulCapabilitiesLoose(identity.Capabilities)
	newCaps := normalizeSoulCapabilitiesLoose(capsNorm)

	// Enforce monotonic claimLevel transitions (except deprecation).
	if !skipTransitionValidation {
		if appErr := s.validateCapabilityClaimLevelTransitions(ctx, identity, newCaps, claimLevels); appErr != nil {
			return appErr
		}
	}

	identity.Capabilities = newCaps
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(identity).IfExists().Update("Capabilities", "UpdatedAt"); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update identity"}
	}

	// Capability index maintenance (best-effort).
	oldSet := map[string]struct{}{}
	for _, c := range oldCaps {
		oldSet[c] = struct{}{}
	}
	newSet := map[string]struct{}{}
	for _, c := range newCaps {
		newSet[c] = struct{}{}
	}
	for c := range oldSet {
		if _, ok := newSet[c]; ok {
			continue
		}
		ci := &models.SoulCapabilityAgentIndex{Capability: c, Domain: identity.Domain, LocalID: identity.LocalID, AgentID: identity.AgentID}
		_ = ci.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(ci).Delete()
	}
	for c := range newSet {
		cl := ""
		if claimLevels != nil {
			cl = claimLevels[c]
		}
		if strings.TrimSpace(cl) == "" {
			cl = "self-declared"
		}
		ci := &models.SoulCapabilityAgentIndex{Capability: c, ClaimLevel: cl, Domain: identity.Domain, LocalID: identity.LocalID, AgentID: identity.AgentID}
		_ = ci.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(ci).CreateOrUpdate()
	}

	return nil
}

func normalizeCapabilityClaimLevel(raw string) (string, bool) {
	cl := strings.ToLower(strings.TrimSpace(raw))
	if cl == "" {
		cl = "self-declared"
	}
	switch cl {
	case "self-declared", "challenge-passed", "peer-endorsed", "deprecated":
		return cl, true
	default:
		return "", false
	}
}

func claimLevelRank(cl string) int {
	switch cl {
	case "self-declared":
		return 1
	case "challenge-passed":
		return 2
	case "peer-endorsed":
		return 3
	default:
		return 0
	}
}

func (s *Server) getExistingCapabilityClaimLevel(ctx context.Context, identity *models.SoulAgentIdentity, capability string) (string, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	idx := &models.SoulCapabilityAgentIndex{
		Capability: capability,
		Domain:     identity.Domain,
		LocalID:    identity.LocalID,
		AgentID:    identity.AgentID,
	}
	_ = idx.UpdateKeys()

	var existing models.SoulCapabilityAgentIndex
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulCapabilityAgentIndex{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", idx.SK).
		First(&existing)
	if theoryErrors.IsNotFound(err) {
		return "self-declared", nil
	}
	if err != nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "failed to read capability index"}
	}

	cl := strings.ToLower(strings.TrimSpace(existing.ClaimLevel))
	if cl == "" {
		cl = "self-declared"
	}
	return cl, nil
}

func (s *Server) validateCapabilityClaimLevelTransitions(ctx context.Context, identity *models.SoulAgentIdentity, caps []string, claimLevels map[string]string) *apptheory.AppError {
	if s == nil || identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	for _, cap := range caps {
		newLevel := ""
		if claimLevels != nil {
			newLevel = claimLevels[cap]
		}
		normNew, ok := normalizeCapabilityClaimLevel(newLevel)
		if !ok {
			return &apptheory.AppError{Code: "app.bad_request", Message: "invalid claimLevel for capability: " + cap}
		}

		oldLevel, appErr := s.getExistingCapabilityClaimLevel(ctx, identity, cap)
		if appErr != nil {
			return appErr
		}
		normOld, _ := normalizeCapabilityClaimLevel(oldLevel)
		if normOld == "" {
			normOld = "self-declared"
		}

		if normOld == "deprecated" && normNew != "deprecated" {
			return &apptheory.AppError{Code: "app.bad_request", Message: "cannot un-deprecate capability: " + cap}
		}
		if normNew == "deprecated" {
			continue
		}

		if claimLevelRank(normNew) < claimLevelRank(normOld) {
			return &apptheory.AppError{
				Code:    "app.bad_request",
				Message: fmt.Sprintf("invalid claimLevel transition for capability %s: %s -> %s", cap, normOld, normNew),
			}
		}
	}

	return nil
}
