package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
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
	S3Key   string                   `json:"s3_key"`
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
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul pack bucket is not configured"}
	}

	agentIDHex, agentInt, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	regBytes, reg, appErr := parseSoulUpdateRegistrationBody(ctx.Request.Body)
	if appErr != nil {
		return nil, appErr
	}

	walletNorm, capsNorm, selfSig, digest, appErr := s.validateSoulUpdateRegistrationDocument(ctx.Context(), reg, agentIDHex, agentInt, identity)
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytes(walletNorm, digest, selfSig); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	// Determine the schema version from the document.
	schemaVersion := extractStringField(reg, "version")
	isV2 := schemaVersion == "2"

	// Determine next version number by querying latest VERSION# item.
	nextVersion, appErr := s.getNextSoulAgentVersion(ctx.Context(), agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	// Publish to the current S3 path.
	s3Key := soulRegistrationS3Key(agentIDHex)
	if err := s.soulPacks.PutObject(ctx.Context(), s3Key, regBytes, "application/json", "private, max-age=0"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to publish registration"}
	}

	// Publish to the versioned S3 path.
	versionedKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion)
	_ = s.soulPacks.PutObject(ctx.Context(), versionedKey, regBytes, "application/json", "private, max-age=0")

	now := time.Now().UTC()
	claimLevels := extractCapabilityClaimLevels(reg)
	if appErr := s.updateSoulAgentCapabilities(ctx.Context(), identity, capsNorm, claimLevels, now); appErr != nil {
		return nil, appErr
	}

	// Update SelfDescriptionVersion if v2.
	if isV2 {
		identity.SelfDescriptionVersion = nextVersion
		_ = s.store.DB.WithContext(ctx.Context()).Model(identity).IfExists().Update("SelfDescriptionVersion")
	}

	// Create version record.
	changeSummary := extractStringField(reg, "changeSummary")
	versionRecord := &models.SoulAgentVersion{
		AgentID:         agentIDHex,
		VersionNumber:   nextVersion,
		RegistrationUri: fmt.Sprintf("s3://%s/%s", s.cfg.SoulPackBucketName, versionedKey),
		ChangeSummary:   changeSummary,
		SelfAttestation: selfSig,
		CreatedAt:       now,
	}
	_ = versionRecord.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(versionRecord).Create()

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.registration.update",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, soulUpdateRegistrationResponse{Agent: *identity, S3Key: s3Key, Version: nextVersion})
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
	if strings.TrimSpace(identity.Status) != models.SoulAgentStatusActive {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent is not active"}
	}

	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}
	return identity, nil
}

func parseSoulUpdateRegistrationBody(body []byte) ([]byte, map[string]any, *apptheory.AppError) {
	regBytes := body

	var wrapper struct {
		Registration json.RawMessage `json:"registration"`
	}
	if unmarshalErr := json.Unmarshal(body, &wrapper); unmarshalErr == nil && len(wrapper.Registration) > 0 {
		regBytes = wrapper.Registration
	}

	var reg map[string]any
	if unmarshalErr := json.Unmarshal(regBytes, &reg); unmarshalErr != nil {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid JSON"}
	}
	return regBytes, reg, nil
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
			out[strings.TrimSpace(v)] = "self-declared"
		case map[string]any:
			if cap, ok := v["capability"].(string); ok {
				cl, _ := v["claimLevel"].(string)
				if cl == "" {
					cl, _ = v["claim_level"].(string)
				}
				if cl == "" {
					cl = "self-declared"
				}
				out[strings.TrimSpace(cap)] = strings.ToLower(strings.TrimSpace(cl))
			}
		}
	}
	return out
}

// getNextSoulAgentVersion queries the latest VERSION# item and returns the next version number.
func (s *Server) getNextSoulAgentVersion(ctx context.Context, agentIDHex string) (int, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return 1, nil
	}

	var items []*models.SoulAgentVersion
	_, err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentVersion{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "VERSION#").
		OrderBy("SK", "DESC").
		Limit(1).
		AllPaginated(&items)
	if err != nil || len(items) == 0 {
		return 1, nil
	}
	return items[0].VersionNumber + 1, nil
}

func (s *Server) updateSoulAgentCapabilities(ctx context.Context, identity *models.SoulAgentIdentity, capsNorm []string, claimLevels map[string]string, now time.Time) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	oldCaps := normalizeSoulCapabilitiesLoose(identity.Capabilities)
	newCaps := normalizeSoulCapabilitiesLoose(capsNorm)

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
		ci := &models.SoulCapabilityAgentIndex{Capability: c, ClaimLevel: cl, Domain: identity.Domain, LocalID: identity.LocalID, AgentID: identity.AgentID}
		_ = ci.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(ci).CreateOrUpdate()
	}

	return nil
}
