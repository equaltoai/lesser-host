package controlplane

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulProvisionEmailBeginRequest struct {
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

	var req soulProvisionEmailBeginRequest
	if len(ctx.Request.Body) > 0 {
		if err := httpx.ParseJSON(ctx, &req); err != nil {
			return nil, err
		}
	}

	localPart := strings.TrimSpace(req.LocalPart)
	if localPart == "" {
		localPart = strings.TrimSpace(identity.LocalID)
	}
	localNorm, err := soul.NormalizeLocalAgentID(localPart)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "localPart is invalid"}
	}

	ensName := strings.TrimSpace(identity.LocalID) + ".lessersoul.eth"
	address := localNorm + "@lessersoul.ai"

	// Load the current registration as the base document (v2 or v3).
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx.Context(), agentIDHex, identity)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	expectedVersion := identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1

	regMap, regV3, digest, appErr := s.buildSoulProvisionEmailRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, soulProvisionEmailBuildInput{
		LocalPart:          localNorm,
		EmailAddress:       address,
		ENSName:            ensName,
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
		Address:         address,
		ENSName:         ensName,
		DigestHex:       "0x" + hex.EncodeToString(digest),
		IssuedAt:        now.Format(time.RFC3339Nano),
		ExpectedVersion: expectedVersion,
		NextVersion:     nextVersion,
		Registration:    regMap,
	})
}

func (s *Server) handleSoulProvisionEmailChannel(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var req soulProvisionEmailConfirmRequest
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

	// Retry-friendly: if the agent has already advanced and the email channel exists, treat as idempotent success.
	if expectedVersion < identity.SelfDescriptionVersion {
		if ch, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx.Context(), agentIDHex, "CHANNEL#email"); err == nil && ch != nil && strings.TrimSpace(ch.Identifier) != "" {
			return apptheory.JSON(http.StatusOK, soulProvisionEmailConfirmResponse{
				Version:             "1",
				Address:             strings.TrimSpace(ch.Identifier),
				RegistrationVersion: identity.SelfDescriptionVersion,
			})
		}
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}
	if expectedVersion != identity.SelfDescriptionVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	localPart := strings.TrimSpace(req.LocalPart)
	if localPart == "" {
		localPart = strings.TrimSpace(identity.LocalID)
	}
	localNorm, err := soul.NormalizeLocalAgentID(localPart)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "localPart is invalid"}
	}

	ensName := strings.TrimSpace(identity.LocalID) + ".lessersoul.eth"
	address := localNorm + "@lessersoul.ai"

	// Load the current registration as the base document (v2 or v3).
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx.Context(), agentIDHex, identity)
	if appErr != nil {
		return nil, appErr
	}

	nextVersion := expectedVersion + 1

	regMap, regV3, digest, appErr := s.buildSoulProvisionEmailRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, soulProvisionEmailBuildInput{
		LocalPart:          localNorm,
		EmailAddress:       address,
		ENSName:            ensName,
		IssuedAt:           issuedAt.UTC(),
		ExpectedPrev:       expectedVersion,
		NextVersion:        nextVersion,
		SelfAttestationHex: selfSig,
	})
	if appErr != nil {
		return nil, appErr
	}

	if err := verifyEthereumSignatureBytes(identity.Wallet, digest, selfSig); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	// Capability indexing inputs.
	caps := extractCapabilityNames(regMap)
	capsNorm, appErr := normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, caps)
	if appErr != nil {
		return nil, appErr
	}

	// Provision the Migadu mailbox (idempotent) and store the mailbox password in SSM.
	passParamName := s.soulAgentEmailPasswordSSMParam(agentIDHex)
	password, passErr := s.ensureSoulAgentEmailPassword(ctx.Context(), passParamName)
	if passErr != nil {
		return nil, passErr
	}
	if s.migaduCreateEmail == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "email provider is not configured"}
	}
	if err := s.migaduCreateEmail(ctx.Context(), localNorm, identity.LocalID, password); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to provision email"}
	}

	regBytes, err := json.Marshal(regMap)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	regSHA256 := func() string {
		sum := sha256.Sum256(regBytes)
		return hex.EncodeToString(sum[:])
	}()

	claimLevels := extractCapabilityClaimLevels(regMap)
	now := time.Now().UTC()
	changeSummary := extractStringField(regMap, "changeSummary")

	publishedVersion, pubErr := s.publishSoulAgentRegistrationV3(ctx.Context(), agentIDHex, identity, regV3, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, &expectedVersion, now)
	if pubErr != nil {
		return nil, pubErr
	}

	// Best-effort: keep v3 channel/preferences state in sync.
	_ = s.syncSoulV3StateFromRegistration(ctx.Context(), agentIDHex, identity, regV3, now)

	// Record host-managed mailbox metadata on the channel record.
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
	if err := s.store.DB.WithContext(ctx.Context()).Model(channel).CreateOrUpdate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to record email channel"}
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
	reg["changeSummary"] = "Provision email channel"

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

func (s *Server) soulAgentEmailPasswordSSMParam(agentIDHex string) string {
	stage := strings.ToLower(strings.TrimSpace(s.cfg.Stage))
	if stage == "" {
		stage = "lab"
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
