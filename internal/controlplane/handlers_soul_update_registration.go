package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

type SoulAgentUpdateRegistrationResult struct {
	Agent   models.SoulAgentIdentity `json:"agent"`
	S3Key   string                   `json:"s3_key,omitempty"`
	Version int                      `json:"version,omitempty"`
}

type soulUpdateRegistrationResponse = SoulAgentUpdateRegistrationResult

const (
	soulClaimLevelSelfDeclared    = "self-declared"
	soulClaimLevelChallengePassed = "challenge-passed"
	soulClaimLevelPeerEndorsed    = "peer-endorsed"
	soulClaimLevelDeprecated      = "deprecated"
)

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
	if appErr := s.requireSoulUpdateRegistrationPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	agentIDHex, agentInt, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	resp, appErr := s.completeSoulAgentRegistrationUpdate(
		ctx.Context(),
		strings.TrimSpace(ctx.AuthIdentity),
		ctx.RequestID,
		agentIDHex,
		agentInt,
		identity,
		ctx.Request.Body,
		isOperator(ctx),
	)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, resp)
}

func (s *Server) UpdateSoulAgentRegistrationForInstance(
	ctx context.Context,
	instanceSlug string,
	requestID string,
	agentID string,
	body []byte,
) (*SoulAgentUpdateRegistrationResult, *apptheory.AppError) {
	if appErr := s.requireSoulUpdateRegistrationInstancePrereqs(instanceSlug); appErr != nil {
		return nil, appErr
	}

	agentIDHex, agentInt, appErr := parseSoulAgentIDHex(agentID)
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentForInstance(ctx, agentIDHex, instanceSlug)
	if appErr != nil {
		return nil, appErr
	}

	return s.completeSoulAgentRegistrationUpdate(
		ctx,
		fmt.Sprintf("instance:%s", strings.TrimSpace(instanceSlug)),
		requestID,
		agentIDHex,
		agentInt,
		identity,
		body,
		false,
	)
}

func (s *Server) completeSoulAgentRegistrationUpdate(
	ctx context.Context,
	actor string,
	requestID string,
	agentIDHex string,
	agentInt *big.Int,
	identity *models.SoulAgentIdentity,
	body []byte,
	includeS3Key bool,
) (*SoulAgentUpdateRegistrationResult, *apptheory.AppError) {
	regBytes, reg, expectedVersion, appErr := parseSoulUpdateRegistrationBody(body)
	if appErr != nil {
		return nil, appErr
	}

	walletNorm, capsNorm, selfSig, digest, appErr := s.validateSoulUpdateRegistrationDocument(ctx, reg, agentIDHex, agentInt, identity)
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytes(walletNorm, digest, selfSig); err != nil {
		log.Printf("controlplane: soul_integrity invalid_registration_signature agent=%s request_id=%s", agentIDHex, strings.TrimSpace(requestID))
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	schemaVersion, regV2, regV3, appErr := parseSoulUpdateRegistrationSchema(regBytes, reg)
	if appErr != nil {
		return nil, appErr
	}
	if bindingErr := s.validateSoulUpdateRegistrationPrincipalBinding(identity, regV2, regV3); bindingErr != nil {
		return nil, bindingErr
	}

	now := time.Now().UTC()
	claimLevels := extractCapabilityClaimLevels(reg)
	nextVersion, s3Key, appErr := s.publishSoulUpdateRegistration(
		ctx,
		agentIDHex,
		identity,
		schemaVersion,
		reg,
		regBytes,
		regSHA256Hex(regBytes),
		selfSig,
		capsNorm,
		claimLevels,
		expectedVersion,
		now,
		regV2,
		regV3,
	)
	if appErr != nil {
		return nil, appErr
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(actor),
		Action:    "soul.registration.update",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: requestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLogWithContext(ctx, audit)

	resp := &SoulAgentUpdateRegistrationResult{Agent: *identity, Version: nextVersion}
	if includeS3Key {
		resp.S3Key = s3Key
	}
	return resp, nil
}

func (s *Server) requireSoulUpdateRegistrationPrereqs(ctx *apptheory.Context) *apptheory.AppError {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return appErr
	}
	if appErr := s.requireSoulRPCConfigured(); appErr != nil {
		return appErr
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.soulPacks == nil {
		return &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	return nil
}

func (s *Server) requireSoulUpdateRegistrationInstancePrereqs(instanceSlug string) *apptheory.AppError {
	if strings.TrimSpace(instanceSlug) == "" {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return appErr
	}
	if appErr := s.requireSoulRPCConfigured(); appErr != nil {
		return appErr
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.soulPacks == nil {
		return &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	return nil
}

func (s *Server) validateSoulUpdateRegistrationPrincipalBinding(identity *models.SoulAgentIdentity, regV2 *soul.RegistrationFileV2, regV3 *soul.RegistrationFileV3) *apptheory.AppError {
	if regV2 == nil && regV3 == nil {
		return nil
	}
	if identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var principal *soul.PrincipalDeclarationV2
	if regV2 != nil {
		principal = &regV2.Principal
	}
	if regV3 != nil {
		principal = &regV3.Principal
	}
	if principal == nil {
		return nil
	}

	binding := soul.PrincipalDeclarationBindingV2{
		Identifier:  strings.TrimSpace(identity.PrincipalAddress),
		Declaration: strings.TrimSpace(identity.PrincipalDeclaration),
		Signature:   strings.TrimSpace(identity.PrincipalSignature),
		DeclaredAt:  strings.TrimSpace(identity.PrincipalDeclaredAt),
	}
	if err := principal.ValidateVerifiedBinding(binding); err != nil {
		code := appErrCodeBadRequest
		if errors.Is(err, soul.ErrPrincipalBindingMissing) {
			code = appErrCodeConflict
		}
		return &apptheory.AppError{Code: code, Message: err.Error()}
	}

	verifiedRegistration := &models.SoulAgentRegistration{
		AgentID:          strings.TrimSpace(identity.AgentID),
		Wallet:           strings.TrimSpace(identity.Wallet),
		DomainNormalized: strings.TrimSpace(identity.Domain),
		LocalID:          strings.TrimSpace(identity.LocalID),
	}
	digest, appErr := s.computeSoulPrincipalDeclarationDigest(verifiedRegistration, binding.Identifier, binding.Declaration, binding.DeclaredAt)
	if appErr != nil {
		return appErr
	}
	if err := verifyEthereumSignatureBytes(binding.Identifier, digest, binding.Signature); err != nil {
		return &apptheory.AppError{Code: appErrCodeConflict, Message: "verified principal binding is invalid; re-verify agent principal"}
	}
	return nil
}

func parseSoulUpdateRegistrationSchema(regBytes []byte, reg map[string]any) (string, *soul.RegistrationFileV2, *soul.RegistrationFileV3, *apptheory.AppError) {
	schemaVersion := strings.TrimSpace(extractStringField(reg, "version"))
	switch schemaVersion {
	case "", "1":
		return schemaVersion, nil, nil, nil
	case "2":
		parsed, err := soul.ParseRegistrationFileV2(regBytes)
		if err != nil {
			return "", nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v2 registration schema"}
		}
		if err := parsed.Validate(); err != nil {
			return "", nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
		}
		return schemaVersion, parsed, nil, nil
	case "3":
		parsed, err := soul.ParseRegistrationFileV3(regBytes)
		if err != nil {
			return "", nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v3 registration schema"}
		}
		if err := parsed.Validate(); err != nil {
			return "", nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
		}
		return schemaVersion, nil, parsed, nil
	default:
		return "", nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "unsupported registration version"}
	}
}

func regSHA256Hex(regBytes []byte) string {
	sum := sha256.Sum256(regBytes)
	return hex.EncodeToString(sum[:])
}

func (s *Server) publishSoulUpdateRegistration(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	schemaVersion string,
	reg map[string]any,
	regBytes []byte,
	regSHA256 string,
	selfSig string,
	capsNorm []string,
	claimLevels map[string]string,
	expectedVersion *int,
	now time.Time,
	regV2 *soul.RegistrationFileV2,
	regV3 *soul.RegistrationFileV3,
) (int, string, *apptheory.AppError) {
	changeSummary := extractStringField(reg, "changeSummary")
	switch schemaVersion {
	case "2":
		publishedVersion, appErr := s.publishSoulAgentRegistrationV2(ctx, agentIDHex, identity, regV2, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, expectedVersion, now)
		return publishedVersion, soulRegistrationS3Key(agentIDHex), appErr
	case "3":
		publishedVersion, appErr := s.publishSoulAgentRegistrationV3(ctx, agentIDHex, identity, regV3, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, expectedVersion, now)
		if appErr != nil {
			return 0, "", appErr
		}
		if syncErr := s.syncSoulV3StateFromRegistration(ctx, agentIDHex, identity, regV3, now); syncErr != nil {
			return 0, "", syncErr
		}
		return publishedVersion, soulRegistrationS3Key(agentIDHex), nil
	default:
		return s.publishLegacySoulRegistration(ctx, agentIDHex, identity, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, now)
	}
}

func (s *Server) publishLegacySoulRegistration(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	regBytes []byte,
	regSHA256 string,
	selfSig string,
	changeSummary string,
	capsNorm []string,
	claimLevels map[string]string,
	now time.Time,
) (int, string, *apptheory.AppError) {
	nextVersion, prevRegSHA256, appErr := s.getNextSoulAgentVersion(ctx, agentIDHex)
	if appErr != nil {
		return 0, "", appErr
	}
	newCaps := normalizeSoulCapabilitiesLoose(capsNorm)
	if appErr := s.validateCapabilityClaimLevelTransitions(ctx, identity, newCaps, claimLevels); appErr != nil {
		return 0, "", appErr
	}

	s3Key := soulRegistrationS3Key(agentIDHex)
	versionedKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion)
	if err := s.soulPacks.PutObject(ctx, versionedKey, regBytes, "application/json", "private, max-age=0"); err != nil {
		return 0, "", &apptheory.AppError{Code: "app.internal", Message: "failed to publish versioned registration"}
	}
	if err := s.soulPacks.PutObject(ctx, s3Key, regBytes, "application/json", "private, max-age=0"); err != nil {
		return 0, "", &apptheory.AppError{Code: "app.internal", Message: "failed to publish registration"}
	}
	if appErr := s.updateSoulAgentCapabilities(ctx, identity, capsNorm, claimLevels, now, true); appErr != nil {
		return 0, "", appErr
	}

	versionRecord := buildSoulVersionRecord(agentIDHex, strings.TrimSpace(s.cfg.SoulPackBucketName), versionedKey, nextVersion, regSHA256, prevRegSHA256, changeSummary, selfSig, now)
	if err := versionRecord.UpdateKeys(); err != nil {
		return 0, "", &apptheory.AppError{Code: "app.internal", Message: "failed to record version history"}
	}
	if err := s.store.DB.WithContext(ctx).Model(versionRecord).Create(); err != nil {
		return 0, "", &apptheory.AppError{Code: "app.internal", Message: "failed to record version history"}
	}
	return nextVersion, s3Key, nil
}

func buildSoulVersionRecord(
	agentIDHex string,
	bucketName string,
	versionedKey string,
	version int,
	regSHA256 string,
	prevRegSHA256 string,
	changeSummary string,
	selfSig string,
	now time.Time,
) *models.SoulAgentVersion {
	return &models.SoulAgentVersion{
		AgentID:                    agentIDHex,
		VersionNumber:              version,
		RegistrationURI:            fmt.Sprintf("s3://%s/%s", strings.TrimSpace(bucketName), versionedKey),
		RegistrationSHA256:         regSHA256,
		PreviousRegistrationSHA256: strings.TrimSpace(prevRegSHA256),
		ChangeSummary:              strings.TrimSpace(changeSummary),
		SelfAttestation:            strings.TrimSpace(selfSig),
		CreatedAt:                  now,
	}
}

func (s *Server) syncSoulV3StateFromRegistration(ctx context.Context, agentIDHex string, identity *models.SoulAgentIdentity, regV3 *soul.RegistrationFileV3, now time.Time) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil || regV3 == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))

	if appErr := s.syncSoulV3ContactPreferences(ctx, agentIDHex, regV3.ContactPreferences, now); appErr != nil {
		return appErr
	}
	emailAddress, phoneNumber := soulV3ChannelAddresses(regV3)
	ensDesired, ensResolution := s.buildSoulV3ENSSync(agentIDHex, identity, regV3, emailAddress, phoneNumber, now)
	if appErr := s.syncSoulV3Channel(ctx, agentIDHex, identity, models.SoulChannelTypeENS, ensDesired, nil, nil, ensResolution); appErr != nil {
		return appErr
	}
	emailDesired, emailIndex := buildSoulV3EmailSync(agentIDHex, regV3, now)
	if appErr := s.syncSoulV3Channel(ctx, agentIDHex, identity, models.SoulChannelTypeEmail, emailDesired, emailIndex, nil, nil); appErr != nil {
		return appErr
	}
	phoneDesired, phoneIndex := buildSoulV3PhoneSync(agentIDHex, regV3, now)
	return s.syncSoulV3Channel(ctx, agentIDHex, identity, models.SoulChannelTypePhone, phoneDesired, nil, phoneIndex, nil)
}

func (s *Server) syncSoulV3ContactPreferences(ctx context.Context, agentIDHex string, prefs *soul.ContactPreferencesV3, now time.Time) *apptheory.AppError {
	if prefs == nil {
		pref := &models.SoulAgentContactPreferences{AgentID: agentIDHex}
		_ = pref.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(pref).Delete()
		return nil
	}

	model := buildSoulV3ContactPreferencesModel(agentIDHex, prefs, now)
	_ = model.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(model).CreateOrUpdate(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update contact preferences"}
	}
	return nil
}

func buildSoulV3ContactPreferencesModel(agentIDHex string, prefs *soul.ContactPreferencesV3, now time.Time) *models.SoulAgentContactPreferences {
	windows := make([]models.SoulContactAvailabilityWindow, 0, len(prefs.Availability.Windows))
	for _, w := range prefs.Availability.Windows {
		windows = append(windows, models.SoulContactAvailabilityWindow{
			Days:      w.Days,
			StartTime: strings.TrimSpace(w.StartTime),
			EndTime:   strings.TrimSpace(w.EndTime),
		})
	}
	model := &models.SoulAgentContactPreferences{
		AgentID:              agentIDHex,
		Preferred:            strings.TrimSpace(prefs.Preferred),
		Fallback:             strings.TrimSpace(prefs.Fallback),
		AvailabilitySchedule: strings.TrimSpace(prefs.Availability.Schedule),
		AvailabilityTimezone: strings.TrimSpace(prefs.Availability.Timezone),
		AvailabilityWindows:  windows,
		ResponseTarget:       strings.TrimSpace(prefs.ResponseExpectation.Target),
		ResponseGuarantee:    strings.TrimSpace(prefs.ResponseExpectation.Guarantee),
		RateLimits:           prefs.RateLimits,
		Languages:            prefs.Languages,
		ContentTypes:         prefs.ContentTypes,
		UpdatedAt:            now,
	}
	if prefs.FirstContact != nil {
		model.FirstContactRequireSoul = prefs.FirstContact.RequireSoul
		model.FirstContactRequireReputation = prefs.FirstContact.RequireReputation
		model.FirstContactIntroductionExpected = prefs.FirstContact.IntroductionExpected
	}
	return model
}

func soulV3ChannelAddresses(regV3 *soul.RegistrationFileV3) (string, string) {
	emailAddress := ""
	phoneNumber := ""
	if regV3.Channels != nil && regV3.Channels.Email != nil {
		emailAddress = strings.TrimSpace(regV3.Channels.Email.Address)
	}
	if regV3.Channels != nil && regV3.Channels.Phone != nil {
		phoneNumber = strings.TrimSpace(regV3.Channels.Phone.Number)
	}
	return emailAddress, phoneNumber
}

func (s *Server) buildSoulV3ENSSync(agentIDHex string, identity *models.SoulAgentIdentity, regV3 *soul.RegistrationFileV3, emailAddress string, phoneNumber string, now time.Time) (*models.SoulAgentChannel, *models.SoulAgentENSResolution) {
	if regV3.Channels == nil || regV3.Channels.ENS == nil {
		return nil, nil
	}
	ens := regV3.Channels.ENS
	ensName := strings.TrimSpace(ens.Name)
	desired := &models.SoulAgentChannel{
		AgentID:            agentIDHex,
		ChannelType:        models.SoulChannelTypeENS,
		Identifier:         ensName,
		ENSResolverAddress: strings.TrimSpace(ens.ResolverAddress),
		ENSChain:           strings.TrimSpace(ens.Chain),
		Status:             models.SoulChannelStatusActive,
		UpdatedAt:          now,
	}
	resolution := &models.SoulAgentENSResolution{
		ENSName:             ensName,
		AgentID:             agentIDHex,
		Wallet:              firstNonEmpty(identity.Wallet, regV3.Wallet),
		LocalID:             firstNonEmpty(identity.LocalID, regV3.LocalID),
		Domain:              firstNonEmpty(identity.Domain, regV3.Domain),
		SoulRegistrationURI: s.currentSoulRegistrationURI(agentIDHex),
		MCPEndpoint:         strings.TrimSpace(regV3.Endpoints.MCP),
		ActivityPubURI:      strings.TrimSpace(regV3.Endpoints.ActivityPub),
		Email:               emailAddress,
		Phone:               phoneNumber,
		Description:         strings.TrimSpace(regV3.SelfDescription.Purpose),
		Status:              firstNonEmpty(regV3.Lifecycle.Status, identity.LifecycleStatus, identity.Status),
		UpdatedAt:           now,
	}
	if createdAt, ok := parseRFC3339Loose(regV3.Created); ok {
		resolution.CreatedAt = createdAt
	}
	if resolution.CreatedAt.IsZero() {
		resolution.CreatedAt = now
	}
	return desired, resolution
}

func (s *Server) currentSoulRegistrationURI(agentIDHex string) string {
	if s == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" || strings.TrimSpace(agentIDHex) == "" {
		return ""
	}
	return fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), soulRegistrationS3Key(agentIDHex))
}

func buildSoulV3EmailSync(agentIDHex string, regV3 *soul.RegistrationFileV3, now time.Time) (*models.SoulAgentChannel, *models.SoulEmailAgentIndex) {
	if regV3.Channels == nil || regV3.Channels.Email == nil {
		return nil, nil
	}
	email := regV3.Channels.Email
	verifiedAt, _ := parseRFC3339Loose(email.VerifiedAt)
	desired := &models.SoulAgentChannel{
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
	return desired, &models.SoulEmailAgentIndex{Email: strings.TrimSpace(email.Address), AgentID: agentIDHex}
}

func buildSoulV3PhoneSync(agentIDHex string, regV3 *soul.RegistrationFileV3, now time.Time) (*models.SoulAgentChannel, *models.SoulPhoneAgentIndex) {
	if regV3.Channels == nil || regV3.Channels.Phone == nil {
		return nil, nil
	}
	phone := regV3.Channels.Phone
	verifiedAt, _ := parseRFC3339Loose(phone.VerifiedAt)
	desired := &models.SoulAgentChannel{
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
	return desired, &models.SoulPhoneAgentIndex{Phone: strings.TrimSpace(phone.Number), AgentID: agentIDHex}
}

func (s *Server) syncSoulV3Channel(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	channelType string,
	desired *models.SoulAgentChannel,
	desiredEmailIndex *models.SoulEmailAgentIndex,
	desiredPhoneIndex *models.SoulPhoneAgentIndex,
	desiredENS *models.SoulAgentENSResolution,
) *apptheory.AppError {
	existing, err := s.loadExistingSoulChannel(ctx, agentIDHex, channelType)
	if err != nil {
		return err
	}
	legacyEmailAlias, appErr := s.buildLegacyEmailAliasForManagedMigration(ctx, identity, existing, desired, channelType)
	if appErr != nil {
		return appErr
	}
	if trustedManagedSoulChannelForIndex(existing) && !soulChannelsSameIdentifier(existing, desired) && legacyEmailAlias == nil {
		return &apptheory.AppError{Code: "app.conflict", Message: "managed channel must be deprovisioned before changing identifier"}
	}
	if legacyEmailAlias != nil {
		preserveManagedSoulChannelProvisioningMetadata(desired, existing)
	} else {
		preserveManagedSoulChannelMetadata(desired, existing)
	}
	if legacyEmailAlias != nil {
		if appErr := s.ensureSoulEmailLegacyAliasIndex(ctx, legacyEmailAlias); appErr != nil {
			return appErr
		}
	}
	if appErr := s.cleanupSoulV3ChannelIndexes(ctx, agentIDHex, channelType, identity, existing, desired); appErr != nil {
		return appErr
	}
	if desired == nil {
		return s.deleteSoulV3Channel(ctx, identity, channelType, existing)
	}
	if appErr := s.upsertSoulV3Channel(ctx, identity, channelType, desired, desiredEmailIndex, desiredPhoneIndex, desiredENS); appErr != nil {
		return appErr
	}
	return nil
}

func (s *Server) buildLegacyEmailAliasForManagedMigration(
	ctx context.Context,
	identity *models.SoulAgentIdentity,
	existing *models.SoulAgentChannel,
	desired *models.SoulAgentChannel,
	channelType string,
) (*models.SoulEmailLegacyAliasIndex, *apptheory.AppError) {
	if channelType != models.SoulChannelTypeEmail || existing == nil || desired == nil {
		return nil, nil
	}
	if !trustedManagedSoulChannelForIndex(existing) || soulChannelsSameIdentifier(existing, desired) {
		return nil, nil
	}
	existingCopy := *existing
	desiredCopy := *desired
	_ = existingCopy.UpdateKeys()
	_ = desiredCopy.UpdateKeys()
	if existingCopy.Provider != commDeliveryProviderMigadu || desiredCopy.ChannelType != models.SoulChannelTypeEmail {
		return nil, nil
	}
	if !isExpectedLegacyBareSoulEmailAlias(existingCopy.Identifier, identity) {
		return nil, nil
	}
	expected, appErr := s.resolveSoulProvisionEmailAddress(ctx, identity, "")
	if appErr != nil {
		return nil, appErr
	}
	if !strings.EqualFold(strings.TrimSpace(desiredCopy.Identifier), expected.Address) {
		return nil, nil
	}
	return &models.SoulEmailLegacyAliasIndex{
		AliasEmail:     existingCopy.Identifier,
		CanonicalEmail: desiredCopy.Identifier,
		AgentID:        strings.TrimSpace(desiredCopy.AgentID),
		Status:         models.SoulEmailLegacyAliasStatusActive,
		Source:         "project37-m2-registration-sync",
	}, nil
}

func isExpectedLegacyBareSoulEmailAlias(address string, identity *models.SoulAgentIdentity) bool {
	expected, ok := expectedLegacyBareSoulEmailAlias(identity)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.ToLower(strings.TrimSpace(address)), expected)
}

func expectedLegacyBareSoulEmailAlias(identity *models.SoulAgentIdentity) (string, bool) {
	if identity == nil {
		return "", false
	}
	localID, err := soul.NormalizeLocalAgentID(identity.LocalID)
	if err != nil || strings.TrimSpace(localID) == "" {
		return "", false
	}
	return localID + "@" + soulManagedEmailDomain, true
}

func (s *Server) loadExistingSoulChannel(ctx context.Context, agentIDHex string, channelType string) (*models.SoulAgentChannel, *apptheory.AppError) {
	sk := fmt.Sprintf("CHANNEL#%s", channelType)
	existing, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx, agentIDHex, sk)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to read channel"}
	}
	return existing, nil
}

func preserveManagedSoulChannelMetadata(desired *models.SoulAgentChannel, existing *models.SoulAgentChannel) {
	if desired == nil || existing == nil {
		return
	}
	if !soulChannelsSameIdentifier(existing, desired) {
		return
	}
	preserveManagedSoulChannelProvisioningMetadata(desired, existing)
}

func preserveManagedSoulChannelProvisioningMetadata(desired *models.SoulAgentChannel, existing *models.SoulAgentChannel) {
	if desired == nil || existing == nil {
		return
	}
	if strings.TrimSpace(desired.Provider) == "" {
		desired.Provider = strings.TrimSpace(existing.Provider)
	}
	if strings.TrimSpace(desired.SecretRef) == "" {
		desired.SecretRef = strings.TrimSpace(existing.SecretRef)
	}
	if desired.ProvisionedAt.IsZero() && !existing.ProvisionedAt.IsZero() {
		desired.ProvisionedAt = existing.ProvisionedAt
	}
	if desired.DeprovisionedAt.IsZero() && !existing.DeprovisionedAt.IsZero() {
		desired.DeprovisionedAt = existing.DeprovisionedAt
	}
	if existing.Verified {
		desired.Verified = true
		if desired.VerifiedAt.IsZero() && !existing.VerifiedAt.IsZero() {
			desired.VerifiedAt = existing.VerifiedAt
		}
	}
}

func (s *Server) cleanupSoulV3ChannelIndexes(ctx context.Context, agentIDHex string, channelType string, identity *models.SoulAgentIdentity, existing *models.SoulAgentChannel, desired *models.SoulAgentChannel) *apptheory.AppError {
	if existing == nil || strings.TrimSpace(existing.Identifier) == "" || (desired != nil && strings.EqualFold(strings.TrimSpace(existing.Identifier), strings.TrimSpace(desired.Identifier))) {
		return nil
	}
	switch channelType {
	case models.SoulChannelTypeEmail:
		old := &models.SoulEmailAgentIndex{Email: existing.Identifier, AgentID: agentIDHex}
		_ = old.UpdateKeys()
		if err := s.deleteSoulEmailAgentIndexIfOwned(ctx, old); err != nil {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to delete email index"}
		}
	case models.SoulChannelTypePhone:
		old := &models.SoulPhoneAgentIndex{Phone: existing.Identifier, AgentID: agentIDHex}
		_ = old.UpdateKeys()
		if err := s.deleteSoulPhoneAgentIndexIfOwned(ctx, old); err != nil {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to delete phone index"}
		}
	case models.SoulChannelTypeENS:
		old := &models.SoulAgentENSResolution{ENSName: existing.Identifier, AgentID: agentIDHex}
		_ = old.UpdateKeys()
		if err := s.deleteSoulENSResolutionIfOwned(ctx, old); err != nil {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to delete ens resolution"}
		}
	}
	if desired == nil {
		return s.deleteSoulChannelAgentIndex(ctx, identity, channelType, agentIDHex)
	}
	return nil
}

func (s *Server) deleteSoulV3Channel(ctx context.Context, identity *models.SoulAgentIdentity, channelType string, existing *models.SoulAgentChannel) *apptheory.AppError {
	if existing != nil {
		_ = s.store.DB.WithContext(ctx).Model(existing).Delete()
	}
	return s.deleteSoulChannelAgentIndex(ctx, identity, channelType, strings.TrimSpace(identity.AgentID))
}

func (s *Server) deleteSoulChannelAgentIndex(ctx context.Context, identity *models.SoulAgentIdentity, channelType string, agentIDHex string) *apptheory.AppError {
	if channelType != models.SoulChannelTypeEmail && channelType != models.SoulChannelTypePhone {
		return nil
	}
	if strings.TrimSpace(identity.Domain) == "" || strings.TrimSpace(identity.LocalID) == "" {
		return nil
	}
	idx := &models.SoulChannelAgentIndex{
		ChannelType: channelType,
		Domain:      strings.TrimSpace(identity.Domain),
		LocalID:     strings.TrimSpace(identity.LocalID),
		AgentID:     agentIDHex,
	}
	_ = idx.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(idx).Delete()
	return nil
}

func (s *Server) upsertSoulV3Channel(
	ctx context.Context,
	identity *models.SoulAgentIdentity,
	channelType string,
	desired *models.SoulAgentChannel,
	desiredEmailIndex *models.SoulEmailAgentIndex,
	desiredPhoneIndex *models.SoulPhoneAgentIndex,
	desiredENS *models.SoulAgentENSResolution,
) *apptheory.AppError {
	_ = desired.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(desired).CreateOrUpdate(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update channel"}
	}
	if appErr := s.upsertSoulV3ChannelIndexes(ctx, identity, channelType, desired, desiredEmailIndex, desiredPhoneIndex, desiredENS); appErr != nil {
		return appErr
	}
	return nil
}

func (s *Server) upsertSoulV3ChannelIndexes(
	ctx context.Context,
	identity *models.SoulAgentIdentity,
	channelType string,
	desired *models.SoulAgentChannel,
	desiredEmailIndex *models.SoulEmailAgentIndex,
	desiredPhoneIndex *models.SoulPhoneAgentIndex,
	desiredENS *models.SoulAgentENSResolution,
) *apptheory.AppError {
	trustedManaged := trustedManagedSoulChannelForIndex(desired)
	if trustedManaged {
		if appErr := s.ensureTrustedSoulContactIndexes(ctx, desiredEmailIndex, desiredPhoneIndex); appErr != nil {
			return appErr
		}
	}
	if desiredENS != nil {
		if appErr := s.ensureSoulENSResolution(ctx, desiredENS); appErr != nil {
			return appErr
		}
	}
	if channelType != models.SoulChannelTypeEmail && channelType != models.SoulChannelTypePhone {
		return nil
	}
	if !trustedManaged {
		return nil
	}
	if strings.TrimSpace(identity.Domain) == "" || strings.TrimSpace(identity.LocalID) == "" {
		return nil
	}
	idx := &models.SoulChannelAgentIndex{
		ChannelType: channelType,
		Domain:      strings.TrimSpace(identity.Domain),
		LocalID:     strings.TrimSpace(identity.LocalID),
		AgentID:     strings.TrimSpace(identity.AgentID),
	}
	_ = idx.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(idx).CreateOrUpdate(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update channel index"}
	}
	return nil
}

func (s *Server) ensureTrustedSoulContactIndexes(ctx context.Context, emailIdx *models.SoulEmailAgentIndex, phoneIdx *models.SoulPhoneAgentIndex) *apptheory.AppError {
	if emailIdx != nil {
		if appErr := s.ensureSoulEmailAgentIndex(ctx, emailIdx); appErr != nil {
			return appErr
		}
	}
	if phoneIdx != nil {
		if appErr := s.ensureSoulPhoneAgentIndex(ctx, phoneIdx); appErr != nil {
			return appErr
		}
	}
	return nil
}

func soulChannelsSameIdentifier(existing *models.SoulAgentChannel, desired *models.SoulAgentChannel) bool {
	if existing == nil && desired == nil {
		return true
	}
	if existing == nil || desired == nil {
		return false
	}
	existingCopy := *existing
	desiredCopy := *desired
	_ = existingCopy.UpdateKeys()
	_ = desiredCopy.UpdateKeys()
	return strings.EqualFold(strings.TrimSpace(existingCopy.ChannelType), strings.TrimSpace(desiredCopy.ChannelType)) &&
		strings.EqualFold(strings.TrimSpace(existingCopy.Identifier), strings.TrimSpace(desiredCopy.Identifier))
}

func trustedManagedSoulChannelForIndex(ch *models.SoulAgentChannel) bool {
	if ch == nil {
		return false
	}
	chCopy := *ch
	_ = chCopy.UpdateKeys()
	if !chCopy.Verified || chCopy.ProvisionedAt.IsZero() || !chCopy.DeprovisionedAt.IsZero() || chCopy.Status != models.SoulChannelStatusActive {
		return false
	}
	switch chCopy.ChannelType {
	case models.SoulChannelTypeEmail:
		return chCopy.Provider == "migadu"
	case models.SoulChannelTypePhone:
		return chCopy.Provider == "telnyx"
	default:
		return false
	}
}

func (s *Server) ensureSoulEmailAgentIndex(ctx context.Context, idx *models.SoulEmailAgentIndex) *apptheory.AppError {
	if idx == nil {
		return nil
	}
	_ = idx.UpdateKeys()
	return s.ensureSoulContactAgentIndex(ctx, idx, idx.PK, idx.SK, idx.Email, idx.AgentID, "email index is invalid", soulEmailProvisionErrAddressTaken, "failed to validate email mapping", "failed to update email index", func() any {
		return &models.SoulEmailAgentIndex{}
	}, func(existing any) string {
		if idx, ok := existing.(*models.SoulEmailAgentIndex); ok && idx != nil {
			return idx.AgentID
		}
		return ""
	})
}

func (s *Server) ensureSoulPhoneAgentIndex(ctx context.Context, idx *models.SoulPhoneAgentIndex) *apptheory.AppError {
	if idx == nil {
		return nil
	}
	_ = idx.UpdateKeys()
	return s.ensureSoulContactAgentIndex(ctx, idx, idx.PK, idx.SK, idx.Phone, idx.AgentID, "phone index is invalid", "phone number is already provisioned", "failed to validate phone mapping", "failed to update phone index", func() any {
		return &models.SoulPhoneAgentIndex{}
	}, func(existing any) string {
		if idx, ok := existing.(*models.SoulPhoneAgentIndex); ok && idx != nil {
			return idx.AgentID
		}
		return ""
	})
}

func (s *Server) ensureSoulEmailLegacyAliasIndex(ctx context.Context, idx *models.SoulEmailLegacyAliasIndex) *apptheory.AppError {
	if idx == nil {
		return nil
	}
	_ = idx.UpdateKeys()
	if strings.TrimSpace(idx.AliasEmail) == "" || strings.TrimSpace(idx.CanonicalEmail) == "" || strings.TrimSpace(idx.AgentID) == "" {
		return &apptheory.AppError{Code: "app.bad_request", Message: "legacy email alias is invalid"}
	}
	var existing models.SoulEmailLegacyAliasIndex
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulEmailLegacyAliasIndex{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", idx.SK).
		First(&existing)
	if err == nil {
		if strings.EqualFold(strings.TrimSpace(existing.AgentID), strings.TrimSpace(idx.AgentID)) &&
			strings.EqualFold(strings.TrimSpace(existing.CanonicalEmail), strings.TrimSpace(idx.CanonicalEmail)) {
			if updateErr := s.store.DB.WithContext(ctx).Model(idx).CreateOrUpdate(); updateErr != nil {
				return &apptheory.AppError{Code: "app.internal", Message: "failed to update legacy email alias"}
			}
			return nil
		}
		return &apptheory.AppError{Code: "app.conflict", Message: "legacy email alias is already assigned"}
	}
	if !theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to validate legacy email alias"}
	}
	if err := s.store.DB.WithContext(ctx).Model(idx).Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return &apptheory.AppError{Code: "app.conflict", Message: "legacy email alias is already assigned"}
		}
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update legacy email alias"}
	}
	return nil
}

func (s *Server) ensureSoulContactAgentIndex(
	ctx context.Context,
	idx any,
	pk string,
	sk string,
	identifier string,
	agentID string,
	invalidMessage string,
	conflictMessage string,
	validateMessage string,
	updateMessage string,
	newExisting func() any,
	owner func(any) string,
) *apptheory.AppError {
	if strings.TrimSpace(identifier) == "" || strings.TrimSpace(agentID) == "" {
		return &apptheory.AppError{Code: "app.bad_request", Message: invalidMessage}
	}
	existing := newExisting()
	err := s.store.DB.WithContext(ctx).
		Model(idx).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		First(existing)
	if err == nil {
		if strings.EqualFold(strings.TrimSpace(owner(existing)), strings.TrimSpace(agentID)) {
			return nil
		}
		return &apptheory.AppError{Code: "app.conflict", Message: conflictMessage}
	}
	if !theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.internal", Message: validateMessage}
	}
	if createErr := s.store.DB.WithContext(ctx).Model(idx).Create(); createErr != nil {
		if theoryErrors.IsConditionFailed(createErr) {
			return &apptheory.AppError{Code: "app.conflict", Message: conflictMessage}
		}
		return &apptheory.AppError{Code: "app.internal", Message: updateMessage}
	}
	return nil
}

func (s *Server) ensureSoulENSResolution(ctx context.Context, idx *models.SoulAgentENSResolution) *apptheory.AppError {
	if idx == nil {
		return nil
	}
	_ = idx.UpdateKeys()
	if strings.TrimSpace(idx.ENSName) == "" || strings.TrimSpace(idx.AgentID) == "" {
		return &apptheory.AppError{Code: "app.bad_request", Message: "ens resolution is invalid"}
	}
	var existing models.SoulAgentENSResolution
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentENSResolution{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", idx.SK).
		First(&existing)
	if err == nil {
		if strings.EqualFold(strings.TrimSpace(existing.AgentID), strings.TrimSpace(idx.AgentID)) {
			if updateErr := s.store.DB.WithContext(ctx).Model(idx).CreateOrUpdate(); updateErr != nil {
				return &apptheory.AppError{Code: "app.internal", Message: "failed to update ens resolution"}
			}
			return nil
		}
		return &apptheory.AppError{Code: "app.conflict", Message: "ens name is already provisioned"}
	}
	if !theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to validate ens resolution"}
	}
	if err := s.store.DB.WithContext(ctx).Model(idx).Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return &apptheory.AppError{Code: "app.conflict", Message: "ens name is already provisioned"}
		}
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update ens resolution"}
	}
	return nil
}

func (s *Server) deleteSoulEmailAgentIndexIfOwned(ctx context.Context, idx *models.SoulEmailAgentIndex) error {
	if idx == nil {
		return nil
	}
	_ = idx.UpdateKeys()
	var existing models.SoulEmailAgentIndex
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulEmailAgentIndex{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", idx.SK).
		First(&existing)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(existing.AgentID), strings.TrimSpace(idx.AgentID)) {
		return nil
	}
	return s.store.DB.WithContext(ctx).Model(idx).Delete()
}

func (s *Server) deleteSoulPhoneAgentIndexIfOwned(ctx context.Context, idx *models.SoulPhoneAgentIndex) error {
	if idx == nil {
		return nil
	}
	_ = idx.UpdateKeys()
	var existing models.SoulPhoneAgentIndex
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulPhoneAgentIndex{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", idx.SK).
		First(&existing)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(existing.AgentID), strings.TrimSpace(idx.AgentID)) {
		return nil
	}
	return s.store.DB.WithContext(ctx).Model(idx).Delete()
}

func (s *Server) deleteSoulENSResolutionIfOwned(ctx context.Context, idx *models.SoulAgentENSResolution) error {
	if idx == nil {
		return nil
	}
	_ = idx.UpdateKeys()
	var existing models.SoulAgentENSResolution
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentENSResolution{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", idx.SK).
		First(&existing)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(existing.AgentID), strings.TrimSpace(idx.AgentID)) {
		return nil
	}
	return s.store.DB.WithContext(ctx).Model(idx).Delete()
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

func (s *Server) requireActiveSoulAgentForInstance(ctx context.Context, agentIDHex string, instanceSlug string) (*models.SoulAgentIdentity, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	identity, err := s.getSoulAgentIdentity(ctx, agentIDHex)
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

	if appErr := s.requireSoulAgentInstanceAccess(ctx, instanceSlug, identity); appErr != nil {
		return nil, appErr
	}

	return identity, nil
}

func (s *Server) requireSoulAgentInstanceAccess(ctx context.Context, instanceSlug string, identity *models.SoulAgentIdentity) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	instanceSlug = strings.TrimSpace(instanceSlug)
	if instanceSlug == "" {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	normalizedDomain := strings.ToLower(strings.TrimSpace(identity.Domain))
	if normalizedDomain == "" {
		return &apptheory.AppError{Code: "app.conflict", Message: "agent domain is invalid"}
	}

	d, err := s.loadManagedStageAwareDomain(ctx, normalizedDomain)
	if theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if d == nil || !domainIsVerifiedOrActive(d.Status) {
		return &apptheory.AppError{Code: "app.conflict", Message: "agent domain is not verified"}
	}
	if !strings.EqualFold(strings.TrimSpace(d.InstanceSlug), instanceSlug) {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	return nil
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

	// Capabilities affect indexing; the host config list is informational only.
	// v2 uses structured capabilities (array of objects with "capability" field);
	// v1 uses a flat string array. Extract capability names for both.
	caps := extractCapabilityNames(reg)
	capsNorm = normalizeSoulCapabilitiesLoose(caps)

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
	localNorm, err := soul.ValidateManagedHandle(bodyLocal)
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
		recordCapabilityClaimLevel(out, item)
	}
	return out
}

func recordCapabilityClaimLevel(out map[string]string, item any) {
	switch v := item.(type) {
	case string:
		name := strings.ToLower(strings.TrimSpace(v))
		if name != "" {
			out[name] = soulClaimLevelSelfDeclared
		}
	case map[string]any:
		name := strings.ToLower(strings.TrimSpace(extractStringField(v, "capability")))
		if name == "" {
			return
		}
		claimLevel := extractStringField(v, "claimLevel")
		if claimLevel == "" {
			claimLevel = extractStringField(v, "claim_level")
		}
		if claimLevel == "" {
			claimLevel = soulClaimLevelSelfDeclared
		}
		out[name] = strings.ToLower(strings.TrimSpace(claimLevel))
	}
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
	return validateSoulRegistrationPreviousVersionURIValue(strings.TrimSpace(s.cfg.SoulPackBucketName), agentIDHex, nextVersion, reg.PreviousVersionURI)
}

func validateSoulRegistrationPreviousVersionURIValue(bucketName string, agentIDHex string, nextVersion int, previousVersionURI *string) *apptheory.AppError {
	if nextVersion <= 1 {
		if strings.TrimSpace(ptrString(previousVersionURI)) != "" {
			log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=prev_uri_set_on_first", agentIDHex, nextVersion)
			return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri must be null for the first version"}
		}
		return nil
	}

	currentURI := strings.TrimSpace(ptrString(previousVersionURI))
	if currentURI == "" {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=missing_prev_uri", agentIDHex, nextVersion)
		return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri is required for subsequent versions"}
	}

	expectedURI := fmt.Sprintf("s3://%s/%s", bucketName, soulRegistrationVersionedS3Key(agentIDHex, nextVersion-1))
	if currentURI != expectedURI {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=prev_uri_mismatch expected=%s got=%s", agentIDHex, nextVersion, expectedURI, currentURI)
		return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri does not match the expected previous version"}
	}
	return nil
}

func ptrString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
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
			cl = soulClaimLevelSelfDeclared
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
		cl = soulClaimLevelSelfDeclared
	}
	switch cl {
	case soulClaimLevelSelfDeclared, soulClaimLevelChallengePassed, soulClaimLevelPeerEndorsed, soulClaimLevelDeprecated:
		return cl, true
	default:
		return "", false
	}
}

func claimLevelRank(cl string) int {
	switch cl {
	case soulClaimLevelSelfDeclared:
		return 1
	case soulClaimLevelChallengePassed:
		return 2
	case soulClaimLevelPeerEndorsed:
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
		return soulClaimLevelSelfDeclared, nil
	}
	if err != nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "failed to read capability index"}
	}

	cl := strings.ToLower(strings.TrimSpace(existing.ClaimLevel))
	if cl == "" {
		cl = soulClaimLevelSelfDeclared
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
			normOld = soulClaimLevelSelfDeclared
		}

		if normOld == soulClaimLevelDeprecated && normNew != soulClaimLevelDeprecated {
			return &apptheory.AppError{Code: "app.bad_request", Message: "cannot un-deprecate capability: " + cap}
		}
		if normNew == soulClaimLevelDeprecated {
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
