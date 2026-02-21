package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
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
	Agent models.SoulAgentIdentity `json:"agent"`
	S3Key string                   `json:"s3_key"`
}

func soulRegistrationS3Key(agentIDHex string) string {
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	return fmt.Sprintf("registry/v1/agents/%s/registration.json", agentIDHex)
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

	regBytes := ctx.Request.Body
	var wrapper struct {
		Registration json.RawMessage `json:"registration"`
	}
	if err := json.Unmarshal(ctx.Request.Body, &wrapper); err == nil && len(wrapper.Registration) > 0 {
		regBytes = wrapper.Registration
	}

	var reg map[string]any
	if err := json.Unmarshal(regBytes, &reg); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid JSON"}
	}

	bodyAgentID := strings.ToLower(extractStringField(reg, "agentId"))
	if bodyAgentID == "" {
		bodyAgentID = strings.ToLower(extractStringField(reg, "agent_id"))
	}
	if !strings.EqualFold(bodyAgentID, agentIDHex) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "agentId does not match path"}
	}

	bodyDomain := strings.ToLower(extractStringField(reg, "domain"))
	if bodyDomain == "" || !strings.EqualFold(bodyDomain, strings.TrimSpace(identity.Domain)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "domain does not match agent"}
	}

	bodyLocal := extractStringField(reg, "localId")
	if bodyLocal == "" {
		bodyLocal = extractStringField(reg, "local_id")
	}
	localNorm, err := soul.NormalizeLocalAgentID(bodyLocal)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}
	if !strings.EqualFold(localNorm, strings.TrimSpace(identity.LocalID)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "localId does not match agent"}
	}

	bodyWallet := extractStringField(reg, "wallet")
	walletNorm, wErr := s.normalizeSoulWalletAddress(ctx.Context(), bodyWallet)
	if wErr != nil {
		return nil, wErr
	}

	attAny, ok := reg["attestations"]
	if !ok {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "attestations are required"}
	}
	att, ok := attAny.(map[string]any)
	if !ok {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "attestations must be an object"}
	}
	selfSig := extractStringField(att, "selfAttestation")
	if selfSig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "attestations.selfAttestation is required"}
	}

	// Verify the wallet is the on-chain owner.
	contractAddr, _, appErr := s.soulRegistryContractAddress()
	if appErr != nil {
		return nil, appErr
	}

	dial := s.dialEVM
	if dial == nil {
		dial = func(ctx context.Context, rpcURL string) (ethRPCClient, error) { return dialEthClient(ctx, rpcURL) }
	}
	client, err := dial(ctx.Context(), s.cfg.SoulRPCURL)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to connect to rpc"}
	}
	defer client.Close()

	onChainWallet, err := s.soulRegistryGetAgentWallet(ctx.Context(), client, contractAddr, agentInt)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to read agent wallet"}
	}
	if (onChainWallet == common.Address{}) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent is not minted"}
	}
	if !strings.EqualFold(onChainWallet.Hex(), walletNorm) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "wallet does not match on-chain state"}
	}
	if !strings.EqualFold(walletNorm, strings.TrimSpace(identity.Wallet)) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent wallet is out of sync; record operation execution first"}
	}

	// Compute canonical digest over the full JSON document, omitting attestations.selfAttestation.
	delete(att, "selfAttestation")
	unsignedBytes, _ := json.Marshal(reg)
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	digest := crypto.Keccak256(jcsBytes)

	if err := verifyEthereumSignatureBytes(walletNorm, digest, selfSig); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	// Capabilities affect indexing; validate against the allowlist if configured.
	caps := extractStringSliceField(reg, "capabilities")
	capsNorm, appErr := normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, caps)
	if appErr != nil {
		return nil, appErr
	}

	s3Key := soulRegistrationS3Key(agentIDHex)
	if err := s.soulPacks.PutObject(ctx.Context(), s3Key, regBytes, "application/json", "private, max-age=0"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to publish registration"}
	}

	now := time.Now().UTC()
	oldCaps := normalizeSoulCapabilitiesLoose(identity.Capabilities)
	newCaps := normalizeSoulCapabilitiesLoose(capsNorm)

	identity.Capabilities = newCaps
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(identity).IfExists().Update("Capabilities", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update identity"}
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
		_ = s.store.DB.WithContext(ctx.Context()).Model(ci).Delete()
	}
	for c := range newSet {
		if _, ok := oldSet[c]; ok {
			continue
		}
		ci := &models.SoulCapabilityAgentIndex{Capability: c, Domain: identity.Domain, LocalID: identity.LocalID, AgentID: identity.AgentID}
		_ = ci.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(ci).CreateOrUpdate()
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.registration.update",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, soulUpdateRegistrationResponse{Agent: *identity, S3Key: s3Key})
}
