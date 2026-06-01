package controlplane

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulManagedEmailDomain    = "lessersoul.ai"
	soulEmailSMTPLocalPartMax = 64

	soulEmailProvisionErrAddressTaken        = "email address is already provisioned"
	soulEmailProvisionErrDerivedLocalPartReq = "local_part must match derived provider local part"
)

type soulProvisionEmailBeginRequest struct {
	// LocalPart is retained for wire compatibility only. New managed soul email
	// provisioning derives the provider local part from agent local ID +
	// Domain.InstanceSlug, and callers may not choose a bare/custom handle.
	LocalPart string `json:"local_part,omitempty"`
}

type soulProvisionEmailBeginResponse struct {
	Version         string         `json:"version"`
	Address         string         `json:"address"`
	ENSName         string         `json:"ens_name"`
	DigestHex       string         `json:"digest_hex"`
	IssuedAt        string         `json:"issued_at"`
	ExpectedVersion int            `json:"expected_version"`
	NextVersion     int            `json:"next_version"`
	Registration    map[string]any `json:"registration"`
}

type soulProvisionEmailConfirmRequest struct {
	// LocalPart is retained for wire compatibility only. When present it must
	// match the derived provider local part exactly; stale bare local-parts are
	// rejected before provider calls.
	LocalPart string `json:"local_part,omitempty"`

	IssuedAt        string `json:"issued_at"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
	SelfAttestation string `json:"self_attestation"`
}

type soulProvisionEmailConfirmResponse struct {
	Version             string `json:"version"`
	Address             string `json:"address"`
	RegistrationVersion int    `json:"registration_version"`
}

func (s *Server) handleSoulBeginProvisionEmailChannel(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentIDHex, identity, appErr := s.requireSoulProvisionIdentity(ctx)
	if appErr != nil {
		return nil, appErr
	}

	var req soulProvisionEmailBeginRequest
	if len(ctx.Request.Body) > 0 {
		if err := httpx.ParseJSON(ctx, &req); err != nil {
			return nil, err
		}
	}

	emailAddress, appErr := s.resolveSoulProvisionEmailAddress(ctx.Context(), identity, req.LocalPart)
	if appErr != nil {
		return nil, appErr
	}
	if availabilityErr := s.validateSoulProvisionEmailAddressAvailability(ctx.Context(), agentIDHex, emailAddress.Address); availabilityErr != nil {
		return nil, availabilityErr
	}

	// Load the current registration as the base document (v2 or v3).
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx.Context(), agentIDHex, identity)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	expectedVersion := identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1

	regMap, regV3, digest, appErr := s.buildSoulProvisionEmailRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, soulProvisionEmailBuildInput{
		LocalPart:          emailAddress.ProviderLocalPart,
		EmailAddress:       emailAddress.Address,
		ENSName:            emailAddress.ENSName,
		IssuedAt:           now,
		ExpectedPrev:       expectedVersion,
		NextVersion:        nextVersion,
		SelfAttestationHex: "",
	})
	if appErr != nil {
		return nil, appErr
	}
	_ = regV3 // keep for debug hooks; regMap is the payload we return

	return apptheory.JSON(http.StatusOK, soulProvisionEmailBeginResponse{
		Version:         "1",
		Address:         emailAddress.Address,
		ENSName:         emailAddress.ENSName,
		DigestHex:       "0x" + hex.EncodeToString(digest),
		IssuedAt:        now.Format(time.RFC3339Nano),
		ExpectedVersion: expectedVersion,
		NextVersion:     nextVersion,
		Registration:    regMap,
	})
}

func (s *Server) handleSoulProvisionEmailChannel(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentIDHex, identity, appErr := s.requireSoulProvisionIdentity(ctx)
	if appErr != nil {
		return nil, appErr
	}

	var req soulProvisionEmailConfirmRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	expectedVersion, issuedAt, selfSig, appErr := parseSoulProvisionConfirm(req.ExpectedVersion, req.IssuedAt, req.SelfAttestation)
	if appErr != nil {
		return nil, appErr
	}

	// Retry-friendly: if the agent has already advanced and the email channel exists, treat as idempotent success.
	if resp, ok, err := s.maybeRespondWithExistingEmailProvision(ctx, agentIDHex, identity.SelfDescriptionVersion, expectedVersion); ok || err != nil {
		return resp, err
	}
	if expectedVersion != identity.SelfDescriptionVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	emailAddress, appErr := s.resolveSoulProvisionEmailAddress(ctx.Context(), identity, req.LocalPart)
	if appErr != nil {
		return nil, appErr
	}
	if availabilityErr := s.validateSoulProvisionEmailAddressAvailability(ctx.Context(), agentIDHex, emailAddress.Address); availabilityErr != nil {
		return nil, availabilityErr
	}

	// Load the current registration as the base document (v2 or v3).
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx.Context(), agentIDHex, identity)
	if appErr != nil {
		return nil, appErr
	}

	nextVersion := expectedVersion + 1

	regMap, regV3, digest, appErr := s.buildSoulProvisionEmailRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, soulProvisionEmailBuildInput{
		LocalPart:          emailAddress.ProviderLocalPart,
		EmailAddress:       emailAddress.Address,
		ENSName:            emailAddress.ENSName,
		IssuedAt:           issuedAt.UTC(),
		ExpectedPrev:       expectedVersion,
		NextVersion:        nextVersion,
		SelfAttestationHex: selfSig,
	})
	if appErr != nil {
		return nil, appErr
	}

	if verifyErr := verifyEthereumSignatureBytes(identity.Wallet, digest, selfSig); verifyErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}
	return s.finalizeSoulProvisionEmailChannel(ctx, agentIDHex, identity, expectedVersion, emailAddress.ProviderLocalPart, emailAddress.Address, regMap, regV3, selfSig)
}

func parseSoulProvisionConfirm(expectedVersion *int, issuedAtRaw string, selfAttestation string) (int, time.Time, string, *apptheory.AppError) {
	if expectedVersion == nil {
		return 0, time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is required"}
	}
	if *expectedVersion < 0 {
		return 0, time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is invalid"}
	}
	issuedAt, issuedAtErr := parseSoulProvisionIssuedAt(issuedAtRaw)
	if issuedAtErr != nil {
		return 0, time.Time{}, "", issuedAtErr
	}
	selfSig := strings.TrimSpace(selfAttestation)
	if selfSig == "" {
		return 0, time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "self_attestation is required"}
	}
	return *expectedVersion, issuedAt, selfSig, nil
}

func (s *Server) maybeRespondWithExistingEmailProvision(ctx *apptheory.Context, agentIDHex string, currentVersion int, expectedVersion int) (*apptheory.Response, bool, error) {
	if expectedVersion >= currentVersion {
		return nil, false, nil
	}
	if identifier := lookupProvisionedChannelIdentifier(ctx.Context(), s, agentIDHex, "CHANNEL#email"); identifier != "" {
		resp, err := apptheory.JSON(http.StatusOK, soulProvisionEmailConfirmResponse{
			Version:             "1",
			Address:             identifier,
			RegistrationVersion: currentVersion,
		})
		return resp, true, err
	}
	return nil, true, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
}

func (s *Server) finalizeSoulProvisionEmailChannel(
	ctx *apptheory.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	expectedVersion int,
	localNorm string,
	address string,
	regMap map[string]any,
	regV3 *soul.RegistrationFileV3,
	selfSig string,
) (*apptheory.Response, error) {
	forwardingAddress := soulEmailInboundForwardingAddress(localNorm, s.cfg.SoulEmailInboundDomain)
	if forwardingAddress == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "email inbound bridge is not configured"}
	}

	caps := extractCapabilityNames(regMap)
	capsNorm := normalizeSoulCapabilitiesLoose(caps)

	passParamName := s.soulAgentEmailPasswordSSMParam(agentIDHex)
	password, passErr := s.ensureSoulAgentEmailPassword(ctx.Context(), passParamName)
	if passErr != nil {
		return nil, passErr
	}
	if s.migaduCreateEmail == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "email provider is not configured"}
	}
	if s.migaduForwarding == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "email provider forwarding is not configured"}
	}
	if provisionErr := s.migaduCreateEmail(ctx.Context(), localNorm, identity.LocalID, password); provisionErr != nil {
		log.Printf("controlplane: soul email provision failed agent=%s address=%s: %v", agentIDHex, address, provisionErr)
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to provision email"}
	}
	if forwardErr := s.migaduForwarding(ctx.Context(), localNorm, forwardingAddress); forwardErr != nil {
		if s.migaduDeleteEmail != nil {
			if rollbackErr := s.migaduDeleteEmail(ctx.Context(), localNorm); rollbackErr != nil {
				log.Printf("controlplane: soul email rollback failed agent=%s address=%s: %v", agentIDHex, address, rollbackErr)
			}
		}
		log.Printf("controlplane: soul email forwarding provision failed agent=%s address=%s: %v", agentIDHex, address, forwardErr)
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to provision email"}
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
	if appErr := upsertProvisionedEmailChannel(ctx.Context(), s, agentIDHex, address, passParamName, now); appErr != nil {
		return nil, appErr
	}
	applyHostedBoundSoulPolicyDefaults(identity)
	if appErr := s.persistSoulAgentPolicyFields(ctx.Context(), identity, now); appErr != nil {
		return nil, appErr
	}

	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Action:    "soul.channel.email.provision",
		Target:    fmt.Sprintf("soul_agent:%s:channel:email", agentIDHex),
		CreatedAt: now,
	})
	return apptheory.JSON(http.StatusCreated, soulProvisionEmailConfirmResponse{
		Version:             "1",
		Address:             address,
		RegistrationVersion: publishedVersion,
	})
}

func soulEmailInboundForwardingAddress(localPart string, inboundDomain string) string {
	localPart = strings.ToLower(strings.TrimSpace(localPart))
	inboundDomain = strings.ToLower(strings.TrimSpace(inboundDomain))
	if localPart == "" || inboundDomain == "" {
		return ""
	}
	return localPart + "@" + inboundDomain
}

func buildProvisionRegistrationPayload(regMap map[string]any) ([]byte, string, map[string]string, string, *apptheory.AppError) {
	regBytes, err := json.Marshal(regMap)
	if err != nil {
		return nil, "", nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	sum := sha256.Sum256(regBytes)
	return regBytes, hex.EncodeToString(sum[:]), extractCapabilityClaimLevels(regMap), extractStringField(regMap, "changeSummary"), nil
}

func upsertProvisionedEmailChannel(ctx context.Context, s *Server, agentIDHex string, address string, passParamName string, now time.Time) *apptheory.AppError {
	channel := &models.SoulAgentChannel{
		AgentID:       agentIDHex,
		ChannelType:   models.SoulChannelTypeEmail,
		Identifier:    address,
		Provider:      "migadu",
		Verified:      true,
		VerifiedAt:    now,
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
		SecretRef:     passParamName,
		Capabilities:  []string{"receive", "send"},
		Protocols:     []string{"smtp", "imap"},
		UpdatedAt:     now,
	}
	_ = channel.UpdateKeys()
	if createErr := s.store.DB.WithContext(ctx).Model(channel).CreateOrUpdate(); createErr != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to record email channel"}
	}
	return s.ensureSoulEmailAgentIndex(ctx, &models.SoulEmailAgentIndex{Email: address, AgentID: agentIDHex})
}

type soulProvisionEmailBuildInput struct {
	LocalPart          string
	EmailAddress       string
	ENSName            string
	IssuedAt           time.Time
	ExpectedPrev       int
	NextVersion        int
	SelfAttestationHex string
}

func (s *Server) buildSoulProvisionEmailRegistration(ctx context.Context, base map[string]any, baseVersion string, agentIDHex string, identity *models.SoulAgentIdentity, input soulProvisionEmailBuildInput) (reg map[string]any, regV3 *soul.RegistrationFileV3, digest []byte, appErr *apptheory.AppError) {
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
	reg["changeSummary"] = "Provision email channel"
	setProvisionSelfAttestation(reg, input.SelfAttestationHex)
	ch := cloneProvisionChannels(reg)
	ensureProvisionENSChannel(ch, input.ENSName)
	ch["email"] = map[string]any{
		"address":      strings.TrimSpace(input.EmailAddress),
		"capabilities": []any{"receive", "send"},
		"protocols":    []any{"smtp", "imap"},
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

func prepareSoulProvisionRegistrationBase(s *Server, base map[string]any, baseVersion string, agentIDHex string, expectedPrev int, nextVersion int) (map[string]any, *apptheory.AppError) {
	if base == nil || s == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	baseVersion = strings.TrimSpace(baseVersion)
	if baseVersion != "2" && baseVersion != "3" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration version is unsupported; update registration first"}
	}
	if expectedPrev < 0 || nextVersion <= 0 || nextVersion != expectedPrev+1 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid expected_version"}
	}

	reg := make(map[string]any, len(base))
	for k, v := range base {
		reg[k] = v
	}
	reg["version"] = "3"
	if nextVersion <= 1 {
		delete(reg, "previousVersionUri")
		return reg, nil
	}
	prevKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion-1)
	reg["previousVersionUri"] = fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), prevKey)
	return reg, nil
}

func setProvisionSelfAttestation(reg map[string]any, selfAttestationHex string) {
	attAny, _ := reg["attestations"].(map[string]any)
	att := map[string]any{}
	for k, v := range attAny {
		att[k] = v
	}
	selfSig := strings.TrimSpace(selfAttestationHex)
	if selfSig == "" {
		selfSig = "0x00"
	}
	att["selfAttestation"] = selfSig
	reg["attestations"] = att
}

func cloneProvisionChannels(reg map[string]any) map[string]any {
	src, _ := reg["channels"].(map[string]any)
	out := map[string]any{}
	for k, v := range src {
		out[k] = v
	}
	return out
}

func ensureProvisionENSChannel(channels map[string]any, ensName string) {
	if _, ok := channels["ens"]; ok || strings.TrimSpace(ensName) == "" {
		return
	}
	channels["ens"] = map[string]any{
		"name":  strings.TrimSpace(ensName),
		"chain": "mainnet",
	}
}

func parseSoulProvisionRegistrationV3(reg map[string]any) (*soul.RegistrationFileV3, *apptheory.AppError) {
	regBytes, err := json.Marshal(reg)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	parsed, err := soul.ParseRegistrationFileV3(regBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v3 registration schema"}
	}
	if err := parsed.Validate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}
	return parsed, nil
}

func parseSoulProvisionIssuedAt(raw string) (time.Time, *apptheory.AppError) {
	issuedAtRaw := strings.TrimSpace(raw)
	if issuedAtRaw == "" {
		return time.Time{}, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at is required"}
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if err == nil {
		return issuedAt, nil
	}
	issuedAt, err = time.Parse(time.RFC3339, issuedAtRaw)
	if err != nil {
		return time.Time{}, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at must be an RFC3339 timestamp"}
	}
	return issuedAt, nil
}

func lookupProvisionedChannelIdentifier(ctx context.Context, s *Server, agentIDHex string, channelKey string) string {
	channel, channelErr := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx, agentIDHex, channelKey)
	if channelErr != nil || channel == nil {
		return ""
	}
	return strings.TrimSpace(channel.Identifier)
}

func (s *Server) requireSoulProvisionIdentity(ctx *apptheory.Context) (string, *models.SoulAgentIdentity, *apptheory.AppError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return "", nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return "", nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return "", nil, appErr
	}
	if s == nil || s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return "", nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return "", nil, appErr
	}
	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return "", nil, appErr
	}
	if identity.SelfDescriptionVersion <= 0 {
		return "", nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet published; update registration first"}
	}
	return agentIDHex, identity, nil
}

type soulProvisionManagedEmailAddress struct {
	AgentLocalID      string
	InstanceSlug      string
	ProviderLocalPart string
	Address           string
	ENSName           string
}

func (s *Server) resolveSoulProvisionEmailAddress(ctx context.Context, identity *models.SoulAgentIdentity, requestedLocalPart string) (soulProvisionManagedEmailAddress, *apptheory.AppError) {
	if identity == nil {
		return soulProvisionManagedEmailAddress{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	canonicalLocal, err := soul.ValidateManagedHandle(identity.LocalID)
	if err != nil || canonicalLocal == "" {
		return soulProvisionManagedEmailAddress{}, &apptheory.AppError{Code: "app.conflict", Message: "agent local id is invalid"}
	}
	instanceSlug, appErr := s.resolveSoulProvisionEmailInstanceSlug(ctx, identity)
	if appErr != nil {
		return soulProvisionManagedEmailAddress{}, appErr
	}
	return buildSoulProvisionManagedEmailAddress(canonicalLocal, instanceSlug, requestedLocalPart)
}

func (s *Server) resolveSoulProvisionEmailInstanceSlug(ctx context.Context, identity *models.SoulAgentIdentity) (string, *apptheory.AppError) {
	return s.resolveSoulManagedIdentityInstanceSlug(ctx, identity, "managed email")
}

func (s *Server) resolveSoulProvisionENSName(ctx context.Context, identity *models.SoulAgentIdentity) (string, *apptheory.AppError) {
	if identity == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	canonicalLocal, err := soul.ValidateManagedHandle(identity.LocalID)
	if err != nil || canonicalLocal == "" {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "agent local id is invalid"}
	}
	instanceSlug, appErr := s.resolveSoulManagedIdentityInstanceSlug(ctx, identity, "managed identity")
	if appErr != nil {
		return "", appErr
	}
	ensName, err := soul.ManagedENSName(canonicalLocal, instanceSlug)
	if err != nil {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "managed ens name is invalid"}
	}
	return ensName, nil
}

func (s *Server) resolveSoulManagedIdentityInstanceSlug(ctx context.Context, identity *models.SoulAgentIdentity, surface string) (string, *apptheory.AppError) {
	if s == nil || identity == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = "managed identity"
	}
	normalizedDomain, err := domains.NormalizeDomain(identity.Domain)
	if err != nil {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "agent domain is invalid"}
	}

	domain, loadErr := s.loadManagedStageAwareDomain(ctx, normalizedDomain)
	if theoryErrors.IsNotFound(loadErr) {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "agent domain is not registered for " + surface}
	}
	if loadErr != nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "failed to load agent domain"}
	}
	if domain == nil || !domainIsVerifiedOrActive(domain.Status) {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "agent domain is not verified for " + surface}
	}

	instanceSlug, err := soul.ValidateManagedInstanceSlug(domain.InstanceSlug)
	if err != nil {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "agent domain instance slug is invalid"}
	}
	return instanceSlug, nil
}

func buildSoulProvisionManagedEmailAddress(agentLocalID string, instanceSlug string, requestedLocalPart string) (soulProvisionManagedEmailAddress, *apptheory.AppError) {
	canonicalLocal, err := soul.ValidateManagedHandle(agentLocalID)
	if err != nil || canonicalLocal == "" {
		return soulProvisionManagedEmailAddress{}, &apptheory.AppError{Code: "app.conflict", Message: "agent local id is invalid"}
	}
	slug, err := soul.ValidateManagedInstanceSlug(instanceSlug)
	if err != nil {
		return soulProvisionManagedEmailAddress{}, &apptheory.AppError{Code: "app.conflict", Message: "agent domain instance slug is invalid"}
	}

	providerLocalPart := canonicalLocal + "." + slug
	if len(providerLocalPart) > soulEmailSMTPLocalPartMax {
		return soulProvisionManagedEmailAddress{}, &apptheory.AppError{Code: "app.conflict", Message: "email local_part exceeds 64-octet SMTP limit"}
	}

	if requestedLocalPart != strings.TrimSpace(requestedLocalPart) {
		return soulProvisionManagedEmailAddress{}, &apptheory.AppError{Code: "app.bad_request", Message: "local_part is invalid"}
	}
	if requested := requestedLocalPart; requested != "" {
		if requested != providerLocalPart {
			return soulProvisionManagedEmailAddress{}, &apptheory.AppError{Code: "app.conflict", Message: soulEmailProvisionErrDerivedLocalPartReq}
		}
	}

	ensName, err := soul.ManagedENSName(canonicalLocal, slug)
	if err != nil {
		return soulProvisionManagedEmailAddress{}, &apptheory.AppError{Code: "app.conflict", Message: "managed ens name is invalid"}
	}

	return soulProvisionManagedEmailAddress{
		AgentLocalID:      canonicalLocal,
		InstanceSlug:      slug,
		ProviderLocalPart: providerLocalPart,
		Address:           providerLocalPart + "@" + soulManagedEmailDomain,
		ENSName:           ensName,
	}, nil
}

func (s *Server) validateSoulProvisionEmailAddressAvailability(ctx context.Context, agentIDHex string, address string) *apptheory.AppError {
	emailIdx := &models.SoulEmailAgentIndex{Email: address}
	_ = emailIdx.UpdateKeys()
	return s.validateSoulProvisionIndexAvailability(ctx, &models.SoulEmailAgentIndex{}, emailIdx.PK, emailIdx.SK, agentIDHex, soulEmailProvisionErrAddressTaken, "failed to validate email mapping", func() any {
		return &models.SoulEmailAgentIndex{}
	}, func(existing any) string {
		if idx, ok := existing.(*models.SoulEmailAgentIndex); ok && idx != nil {
			return idx.AgentID
		}
		return ""
	})
}

func (s *Server) validateSoulProvisionIndexAvailability(
	ctx context.Context,
	model any,
	pk string,
	sk string,
	agentIDHex string,
	conflictMessage string,
	internalMessage string,
	newExisting func() any,
	owner func(any) string,
) *apptheory.AppError {
	existing := newExisting()
	lookupErr := s.store.DB.WithContext(ctx).
		Model(model).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		First(existing)
	if lookupErr == nil {
		existingAgentID := strings.TrimSpace(owner(existing))
		if existingAgentID != "" && !strings.EqualFold(existingAgentID, agentIDHex) {
			return &apptheory.AppError{Code: "app.conflict", Message: conflictMessage}
		}
		return nil
	}
	if !theoryErrors.IsNotFound(lookupErr) {
		return &apptheory.AppError{Code: "app.internal", Message: internalMessage}
	}
	return nil
}

func (s *Server) soulAgentEmailPasswordSSMParam(agentIDHex string) string {
	stage := strings.ToLower(strings.TrimSpace(s.cfg.Stage))
	if stage == "" {
		stage = defaultControlPlaneStage
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	// #nosec G101 -- SSM parameter path, not a hardcoded credential.
	return fmt.Sprintf("/lesser-host/soul/%s/agents/%s/channels/email/migadu_password", stage, agentIDHex)
}

func (s *Server) ensureSoulAgentEmailPassword(ctx context.Context, paramName string) (string, *apptheory.AppError) {
	if strings.TrimSpace(paramName) == "" {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s == nil || s.ssmPutSecureValue == nil || s.ssmGetParameter == nil {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "ssm is not configured"}
	}

	// If it already exists, reuse it.
	if existing, err := s.ssmGetParameter(ctx, paramName); err == nil && strings.TrimSpace(existing) != "" {
		return strings.TrimSpace(existing), nil
	}

	pw, err := generateRandomSecret(24)
	if err != nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "failed to generate password"}
	}
	if err := s.ssmPutSecureValue(ctx, paramName, pw, false); err != nil {
		if secrets.IsSSMParameterAlreadyExists(err) {
			if existing, getErr := s.ssmGetParameter(ctx, paramName); getErr == nil && strings.TrimSpace(existing) != "" {
				return strings.TrimSpace(existing), nil
			}
		}
		return "", &apptheory.AppError{Code: "app.internal", Message: "failed to store password"}
	}
	return pw, nil
}

func generateRandomSecret(nBytes int) (string, error) {
	if nBytes <= 0 {
		nBytes = 24
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
