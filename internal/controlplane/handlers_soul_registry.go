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
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulRegistryProofPrefix = "_lesser-soul-agent."
	soulRegistryProofValue  = "lesser-soul-agent="
	soulRegistryWellKnown   = "/.well-known/lesser-soul-agent"
)

type soulAgentRegistrationBeginRequest struct {
	Domain         string `json:"domain"`
	LocalID        string `json:"local_id"`
	Wallet         string `json:"wallet_address"`
	AuthorityModel string `json:"authority_model,omitempty"`
	Capabilities   []any  `json:"capabilities,omitempty"`
}

type soulRegistryProofInstructions struct {
	Method    string `json:"method"`
	DNSName   string `json:"dns_name,omitempty"`
	DNSValue  string `json:"dns_value,omitempty"`
	HTTPSURL  string `json:"https_url,omitempty"`
	HTTPSBody string `json:"https_body,omitempty"`
}

type soulAgentRegistrationBeginResponse struct {
	Registration models.SoulAgentRegistration    `json:"registration"`
	Wallet       *walletChallengeResponse        `json:"wallet,omitempty"`
	Proofs       []soulRegistryProofInstructions `json:"proofs"`
	Promotion    *soulAgentPromotionView         `json:"promotion,omitempty"`
}

type soulAgentRegistrationVerifyRequest struct {
	Signature string `json:"signature"`

	PrincipalAddress     string `json:"principal_address"`
	PrincipalDeclaration string `json:"principal_declaration"`
	PrincipalSignature   string `json:"principal_signature"`
	DeclaredAt           string `json:"declared_at"`
}

type soulAgentRegistrationPrincipalDeclarationPreflightRequest struct {
	PrincipalAddress     string `json:"principal_address"`
	PrincipalDeclaration string `json:"principal_declaration"`
	DeclaredAt           string `json:"declared_at"`
}

type soulAgentRegistrationPrincipalDeclarationPreflightResponse struct {
	Version          string `json:"version"`
	PrincipalAddress string `json:"principal_address"`
	SignerAddress    string `json:"signer_address"`
	SigningMethod    string `json:"signing_method"`
	MessageEncoding  string `json:"message_encoding"`
	MessageHex       string `json:"message_hex"`
	DigestHex        string `json:"digest_hex"`
	CanonicalJSON    string `json:"canonical_json"`
	DeclaredAt       string `json:"declared_at"`
}

type soulAgentRegistrationVerifyResponse struct {
	Registration models.SoulAgentRegistration `json:"registration"`
	Operation    models.SoulOperation         `json:"operation"`
	SafeTx       *safeTxPayload               `json:"safe_tx,omitempty"`
	Promotion    *soulAgentPromotionView      `json:"promotion,omitempty"`
}

func normalizeSoulCapabilitiesLoose(caps []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if len(c) > 64 {
			continue
		}
		if strings.ContainsAny(c, " \t\r\n") {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func parseSoulRegistrationBeginCapabilities(raw []any) ([]string, *apptheory.AppTheoryError) {
	if raw == nil {
		return nil, nil
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			cap := extractStringField(v, "capability")
			if cap == "" {
				return nil, newAppTheoryError("app.bad_request", "capability objects must include capability")
			}

			claimLevel := extractStringField(v, "claimLevel")
			if claimLevel == "" {
				claimLevel = extractStringField(v, "claim_level")
			}
			claimLevel = strings.ToLower(strings.TrimSpace(claimLevel))
			if claimLevel == "" {
				claimLevel = soulClaimLevelSelfDeclared
			}
			if claimLevel != soulClaimLevelSelfDeclared {
				return nil, newAppTheoryError("app.bad_request", "capability claimLevel must be self-declared at registration begin")
			}

			out = append(out, cap)
		default:
			return nil, newAppTheoryError("app.bad_request", "capabilities must be an array of strings or objects")
		}
	}

	return out, nil
}

func (s *Server) normalizeSoulWalletAddress(ctx context.Context, walletAddr string) (string, *apptheory.AppTheoryError) {
	return s.normalizeSoulEVMAddress(ctx, walletAddr, "wallet_address")
}

func (s *Server) normalizeSoulEVMAddress(ctx context.Context, addr string, field string) (string, *apptheory.AppTheoryError) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		if field == "" {
			return "", newAppTheoryError("app.bad_request", "address is required")
		}
		return "", newAppTheoryError("app.bad_request", field+" is required")
	}
	if !common.IsHexAddress(addr) {
		if field == "" {
			return "", newAppTheoryError("app.bad_request", "invalid address")
		}
		return "", newAppTheoryError("app.bad_request", "invalid "+field)
	}
	addr = strings.ToLower(addr)
	if appErr := validateNotReservedWalletAddress(addr, field); appErr != nil {
		return "", appErr
	}
	if appErr := s.validateNotPrivilegedWalletAddress(ctx, walletTypeEthereum, addr, field); appErr != nil {
		return "", appErr
	}
	return addr, nil
}

func (s *Server) soulRegistryContractAddress() (common.Address, string, *apptheory.AppTheoryError) {
	contractAddrRaw := strings.TrimSpace(s.cfg.SoulRegistryContractAddress)
	if !common.IsHexAddress(contractAddrRaw) {
		return common.Address{}, "", newAppTheoryError("app.conflict", "soul registry is not configured")
	}
	contractAddr := common.HexToAddress(contractAddrRaw)
	txTo := strings.ToLower(contractAddr.Hex())
	return contractAddr, txTo, nil
}

func (s *Server) soulRegistrySafeAddress() (string, *apptheory.AppTheoryError) {
	safeAddr := strings.ToLower(strings.TrimSpace(s.cfg.SoulAdminSafeAddress))
	if strings.ToLower(strings.TrimSpace(s.cfg.SoulTxMode)) == tipTxModeSafe && !common.IsHexAddress(safeAddr) {
		return "", newAppTheoryError("app.conflict", "soul registry safe is not configured")
	}
	return safeAddr, nil
}

func (s *Server) requireSoulPortalPrereqs(ctx *apptheory.Context) *apptheory.AppTheoryError {
	if err := requireAuthenticated(ctx); err != nil {
		if appErr, ok := err.(*apptheory.AppTheoryError); ok {
			return appErr
		}
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if appErr := s.requirePortalApproved(ctx); appErr != nil {
		return appErr
	}
	return nil
}

func (s *Server) resolveSoulDomainAccess(
	ctx *apptheory.Context,
	normalizedDomain string,
) (*models.Domain, *models.Instance, bool, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, nil, false, newAppTheoryError("app.internal", "internal error")
	}

	normalizedDomain = strings.ToLower(strings.TrimSpace(normalizedDomain))
	if normalizedDomain == "" {
		return nil, nil, false, newAppTheoryError("app.bad_request", "domain is required")
	}

	var d models.Domain
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", normalizedDomain)).
		Where("SK", "=", models.SKMetadata).
		First(&d)
	if err == nil {
		if !domainIsVerifiedOrActive(d.Status) {
			return nil, nil, false, newAppTheoryError("app.bad_request", "domain is not verified")
		}

		inst, instErr := s.requireInstanceAccess(ctx, strings.TrimSpace(d.InstanceSlug))
		if instErr != nil {
			if appErr, ok := instErr.(*apptheory.AppTheoryError); ok {
				return nil, nil, false, appErr
			}
			return nil, nil, false, newAppTheoryError("app.internal", "internal error")
		}

		return &d, inst, false, nil
	}
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, nil, false, newAppTheoryError("app.internal", "internal error")
	}

	managedAccess := s.resolveManagedSoulStageDomainAccess(ctx, normalizedDomain)
	if managedAccess != nil {
		return managedAccess.domain, managedAccess.instance, true, nil
	}

	return nil, nil, false, newAppTheoryError("app.bad_request", "domain is not registered")
}

func (s *Server) requireSoulDomainAccess(ctx *apptheory.Context, normalizedDomain string) (*models.Domain, *models.Instance, *apptheory.AppTheoryError) {
	d, inst, _, appErr := s.resolveSoulDomainAccess(ctx, normalizedDomain)
	return d, inst, appErr
}

type managedSoulDomainAccess struct {
	domain   *models.Domain
	instance *models.Instance
}

func (s *Server) resolveManagedSoulStageDomainAccess(ctx *apptheory.Context, normalizedDomain string) *managedSoulDomainAccess {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil
	}

	d, err := s.loadManagedStageAliasPrimaryDomain(ctx.Context(), normalizedDomain)
	if err != nil || d == nil {
		return nil
	}

	inst, instErr := s.requireInstanceAccess(ctx, strings.TrimSpace(d.InstanceSlug))
	if instErr != nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(inst.HostedBaseDomain), strings.TrimSpace(d.Domain)) {
		return nil
	}

	return &managedSoulDomainAccess{
		domain:   d,
		instance: inst,
	}
}

func (s *Server) getSoulAgentIdentity(ctx context.Context, agentID string) (*models.SoulAgentIdentity, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}

	var item models.SoulAgentIdentity
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentIdentity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", "IDENTITY").
		First(&item)
	if err != nil {
		return nil, err
	}

	// Backward-compatible read repair: keep lifecycleStatus aligned with status to avoid confusing reads/filters.
	// (Some legacy writes updated Status but not LifecycleStatus.)
	status := strings.ToLower(strings.TrimSpace(item.Status))
	lifecycle := strings.ToLower(strings.TrimSpace(item.LifecycleStatus))
	if lifecycle == "" && status != "" {
		item.LifecycleStatus = status
	} else if status == "" && lifecycle != "" {
		item.Status = lifecycle
	} else if status != "" && lifecycle != "" && status != lifecycle {
		if s.cfg.SoulV2StrictIntegrity {
			log.Printf("controlplane: soul_integrity lifecycle_mismatch agent=%s status=%s lifecycle=%s", agentID, status, lifecycle)
		}
		item.LifecycleStatus = status
	}

	return &item, nil
}

func (s *Server) handleSoulAgentRegistrationBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	var req soulAgentRegistrationBeginRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	rawDomain := strings.TrimSpace(req.Domain)
	domainNormalized, domainAccessAutoVerified, appErr := s.normalizeSoulRegistrationBeginDomain(ctx, rawDomain)
	if appErr != nil {
		return nil, appErr
	}

	out, appErr := s.beginSoulAgentRegistration(ctx, req, domainNormalized, domainAccessAutoVerified, strings.TrimSpace(ctx.AuthIdentity), false)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusCreated, out)
}

func (s *Server) beginSoulAgentRegistration(
	ctx *apptheory.Context,
	req soulAgentRegistrationBeginRequest,
	domainNormalized string,
	domainAccessAutoVerified bool,
	actor string,
	allowInstanceTrustNoWallet bool,
) (soulAgentRegistrationBeginResponse, *apptheory.AppTheoryError) {
	if ctx == nil {
		return soulAgentRegistrationBeginResponse{}, newAppTheoryError("app.internal", "internal error")
	}

	rawDomain := strings.TrimSpace(req.Domain)
	domainNormalized = strings.ToLower(strings.TrimSpace(domainNormalized))
	actor = strings.TrimSpace(actor)

	rawLocal := req.LocalID
	local, appErr := normalizeSoulRegistrationBeginLocalID(rawLocal)
	if appErr != nil {
		return soulAgentRegistrationBeginResponse{}, appErr
	}

	authorityModel, wallet, appErr := s.resolveSoulRegistrationBeginAuthority(ctx.Context(), req, allowInstanceTrustNoWallet)
	if appErr != nil {
		return soulAgentRegistrationBeginResponse{}, appErr
	}

	capNames, appErr := parseSoulRegistrationBeginCapabilities(req.Capabilities)
	if appErr != nil {
		return soulAgentRegistrationBeginResponse{}, appErr
	}

	caps := normalizeSoulCapabilitiesLoose(capNames)

	agentIDHex, err := soul.DeriveAgentIDHex(domainNormalized, local)
	if err != nil {
		return soulAgentRegistrationBeginResponse{}, newAppTheoryError("app.internal", "failed to derive agent_id")
	}

	now := time.Now().UTC()
	replayed, ok, replayErr := s.replaySoulRegistrationBeginIfHostedInstanceTrust(ctx.Context(), authorityModel, agentIDHex, actor)
	if replayErr != nil {
		return soulAgentRegistrationBeginResponse{}, replayErr
	}
	if ok {
		return replayed, nil
	}

	if ensureErr := s.ensureSoulAgentNotActive(ctx.Context(), agentIDHex); ensureErr != nil {
		return soulAgentRegistrationBeginResponse{}, ensureErr
	}

	proofToken, nonce, id, appErr := newSoulAgentRegistrationBeginTokens()
	if appErr != nil {
		return soulAgentRegistrationBeginResponse{}, appErr
	}
	proofValue := soulRegistryProofValue + proofToken

	expiresAt, msg := s.buildSoulRegistrationBeginWalletMaterial(authorityModel, domainNormalized, local, agentIDHex, wallet, caps, proofValue, nonce, now)

	reg := &models.SoulAgentRegistration{
		ID:               id,
		Username:         actor,
		DomainRaw:        rawDomain,
		DomainNormalized: domainNormalized,
		LocalIDRaw:       rawLocal,
		LocalID:          local,
		AgentID:          agentIDHex,
		Wallet:           wallet,
		AuthorityModel:   authorityModel,
		Capabilities:     caps,
		WalletNonce:      nonce,
		WalletMessage:    msg,
		ProofToken:       proofToken,
		DNSVerified:      domainAccessAutoVerified,
		HTTPSVerified:    domainAccessAutoVerified,
		Status:           models.SoulAgentRegistrationStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        expiresAt,
	}
	appErr = s.createSoulAgentRegistration(ctx.Context(), reg)
	if appErr != nil {
		return soulAgentRegistrationBeginResponse{}, appErr
	}
	promotion := buildSoulAgentPromotionFromRegistration(reg, now)
	promotion, appErr = s.prepareSoulRegistrationBeginHostedAuthority(ctx.Context(), reg, promotion, now)
	if appErr != nil {
		return soulAgentRegistrationBeginResponse{}, appErr
	}
	appErr = s.saveSoulAgentPromotion(ctx.Context(), promotion)
	if appErr != nil {
		return soulAgentRegistrationBeginResponse{}, appErr
	}
	if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:  models.SoulAgentPromotionEventTypeRequestCreated,
		RequestID:  strings.TrimSpace(ctx.RequestID),
		OccurredAt: now,
	})); appErr != nil {
		return soulAgentRegistrationBeginResponse{}, appErr
	}

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "soul.registration.begin",
		Target:    fmt.Sprintf("soul_agent_registration:%s", reg.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	proofs := buildSoulRegistrationProofInstructions(domainNormalized, proofToken, domainAccessAutoVerified)

	return s.buildSoulAgentRegistrationBeginResponse(reg, promotion, proofs, authorityModel, actor, wallet, nonce, msg, now, expiresAt), nil
}

func (s *Server) buildSoulRegistrationBeginWalletMaterial(authorityModel string, domainNormalized string, local string, agentIDHex string, wallet string, caps []string, proofValue string, nonce string, now time.Time) (time.Time, string) {
	if authorityModel != models.SoulAuthorityModelWalletPrincipal {
		return time.Time{}, ""
	}
	expiresAt := now.Add(30 * time.Minute)
	msg := buildSoulRegistryWalletMessage(domainNormalized, local, agentIDHex, wallet, s.cfg.SoulChainID, caps, proofValue, nonce, now, expiresAt)
	return expiresAt, msg
}

func (s *Server) prepareSoulRegistrationBeginHostedAuthority(ctx context.Context, reg *models.SoulAgentRegistration, promotion *models.SoulAgentPromotion, now time.Time) (*models.SoulAgentPromotion, *apptheory.AppTheoryError) {
	if normalizeSoulAuthorityModel(reg.AuthorityModel) != models.SoulAuthorityModelInstanceTrust {
		return promotion, nil
	}
	promotion = updateSoulAgentPromotionForInstanceTrustBegin(promotion, now)
	if appErr := s.ensureSoulHostedInstanceTrustIdentity(ctx, reg, now); appErr != nil {
		return nil, appErr
	}
	s.upsertSoulAgentIndexes(ctx, reg)
	return promotion, nil
}

func (s *Server) buildSoulAgentRegistrationBeginResponse(
	reg *models.SoulAgentRegistration,
	promotion *models.SoulAgentPromotion,
	proofs []soulRegistryProofInstructions,
	authorityModel string,
	actor string,
	wallet string,
	nonce string,
	msg string,
	now time.Time,
	expiresAt time.Time,
) soulAgentRegistrationBeginResponse {
	out := soulAgentRegistrationBeginResponse{
		Registration: *reg,
		Proofs:       proofs,
		Promotion:    ptrTo(s.buildSoulAgentPromotionView(promotion)),
	}
	if authorityModel == models.SoulAuthorityModelWalletPrincipal {
		out.Wallet = &walletChallengeResponse{
			ID:        reg.ID,
			Username:  actor,
			Address:   wallet,
			ChainID:   int(s.cfg.SoulChainID),
			Nonce:     nonce,
			Message:   msg,
			IssuedAt:  now,
			ExpiresAt: expiresAt,
		}
	}
	return out
}

func (s *Server) replaySoulRegistrationBeginIfHostedInstanceTrust(ctx context.Context, authorityModel string, agentIDHex string, actor string) (soulAgentRegistrationBeginResponse, bool, *apptheory.AppTheoryError) {
	if authorityModel != models.SoulAuthorityModelInstanceTrust {
		return soulAgentRegistrationBeginResponse{}, false, nil
	}
	return s.replaySoulHostedInstanceTrustBegin(ctx, agentIDHex, actor)
}

func buildSoulRegistrationProofInstructions(domainNormalized string, proofToken string, autoVerified bool) []soulRegistryProofInstructions {
	if autoVerified {
		return nil
	}
	domainNormalized = strings.ToLower(strings.TrimSpace(domainNormalized))
	proofToken = strings.TrimSpace(proofToken)
	proofValue := soulRegistryProofValue + proofToken
	dnsName := soulRegistryProofPrefix + domainNormalized
	httpsURL := "https://" + domainNormalized + path.Clean(soulRegistryWellKnown)
	httpsBody, _ := json.Marshal(map[string]string{"lesser-soul-agent": proofToken})
	return []soulRegistryProofInstructions{
		{Method: "dns_txt", DNSName: dnsName, DNSValue: proofValue},
		{Method: "https_well_known", HTTPSURL: httpsURL, HTTPSBody: string(httpsBody)},
	}
}

func (s *Server) normalizeSoulRegistrationBeginDomain(ctx *apptheory.Context, rawDomain string) (string, bool, *apptheory.AppTheoryError) {
	rawDomain = strings.TrimSpace(rawDomain)
	domainNormalized, err := domains.NormalizeDomain(rawDomain)
	if err != nil {
		return "", false, newAppTheoryError("app.bad_request", err.Error())
	}
	_, _, autoVerified, accessErr := s.resolveSoulDomainAccess(ctx, domainNormalized)
	if accessErr != nil {
		return "", false, accessErr
	}
	return domainNormalized, autoVerified, nil
}

func (s *Server) resolveSoulRegistrationBeginAuthority(ctx context.Context, req soulAgentRegistrationBeginRequest, allowInstanceTrustNoWallet bool) (authorityModel string, wallet string, appErr *apptheory.AppTheoryError) {
	requestedAuthority := normalizeSoulAuthorityModel(req.AuthorityModel)
	if strings.TrimSpace(req.AuthorityModel) != "" && requestedAuthority == "" {
		return "", "", newAppTheoryError("app.bad_request", "authority_model is invalid")
	}

	rawWallet := strings.TrimSpace(req.Wallet)
	switch {
	case requestedAuthority == models.SoulAuthorityModelInstanceTrust:
		if !allowInstanceTrustNoWallet {
			return "", "", newAppTheoryError("app.forbidden", "authority_model is not allowed on this route")
		}
		if rawWallet != "" {
			return "", "", newAppTheoryError("app.bad_request", "wallet_address must be omitted for authority_model=instance_trust")
		}
		return models.SoulAuthorityModelInstanceTrust, "", nil
	case requestedAuthority == models.SoulAuthorityModelWalletPrincipal:
		wallet, appErr = s.normalizeSoulWalletAddress(ctx, rawWallet)
		if appErr != nil {
			return "", "", appErr
		}
		return models.SoulAuthorityModelWalletPrincipal, wallet, nil
	case rawWallet == "":
		if !allowInstanceTrustNoWallet {
			return "", "", newAppTheoryError("app.bad_request", "wallet_address is required")
		}
		return models.SoulAuthorityModelInstanceTrust, "", nil
	default:
		wallet, appErr = s.normalizeSoulWalletAddress(ctx, rawWallet)
		if appErr != nil {
			return "", "", appErr
		}
		return models.SoulAuthorityModelWalletPrincipal, wallet, nil
	}
}

func normalizeSoulRegistrationBeginLocalID(rawLocal string) (string, *apptheory.AppTheoryError) {
	local, err := soul.ValidateManagedHandle(rawLocal)
	if err != nil {
		return "", newAppTheoryError("app.bad_request", err.Error())
	}
	return local, nil
}

func (s *Server) ensureSoulAgentNotActive(ctx context.Context, agentIDHex string) *apptheory.AppTheoryError {
	existing, err := s.getSoulAgentIdentity(ctx, agentIDHex)
	if err == nil && existing != nil && strings.TrimSpace(existing.Status) == models.SoulAgentStatusActive {
		return newAppTheoryError("app.conflict", "agent is already registered")
	}
	if err != nil && !theoryErrors.IsNotFound(err) {
		return newAppTheoryError("app.internal", "internal error")
	}
	return nil
}

func newSoulAgentRegistrationBeginTokens() (proofToken string, nonce string, registrationID string, appErr *apptheory.AppTheoryError) {
	proofToken, err := newToken(16)
	if err != nil {
		return "", "", "", newAppTheoryError("app.internal", "failed to create proof token")
	}

	nonce, err = generateNonce()
	if err != nil {
		return "", "", "", newAppTheoryError("app.internal", "failed to create nonce")
	}

	registrationID, err = newToken(16)
	if err != nil {
		return "", "", "", newAppTheoryError("app.internal", "failed to create registration id")
	}

	return proofToken, nonce, registrationID, nil
}

func (s *Server) createSoulAgentRegistration(ctx context.Context, reg *models.SoulAgentRegistration) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || reg == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	_ = reg.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(reg).IfNotExists().Create(); err != nil {
		return newAppTheoryError("app.internal", "failed to create registration")
	}
	return nil
}

func buildSoulRegistryWalletMessage(domainNormalized, localID, agentIDHex, walletAddr string, chainID int64, caps []string, proofValue, nonce string, issuedAt, expiresAt time.Time) string {
	var sb strings.Builder

	sb.WriteString("lesser.host requests you to register a lesser-soul agent for:\n")
	sb.WriteString(domainNormalized)
	sb.WriteString("/")
	sb.WriteString(localID)
	sb.WriteString("\n\n")
	sb.WriteString("Agent ID: ")
	sb.WriteString(strings.ToLower(strings.TrimSpace(agentIDHex)))
	sb.WriteString("\nWallet: ")
	sb.WriteString(strings.ToLower(strings.TrimSpace(walletAddr)))
	sb.WriteString("\nChain ID: ")
	sb.WriteString(strconv.FormatInt(chainID, 10))
	if len(caps) > 0 {
		sb.WriteString("\nCapabilities: ")
		sb.WriteString(strings.Join(caps, ","))
	}
	sb.WriteString("\nProof value: ")
	sb.WriteString(strings.TrimSpace(proofValue))
	sb.WriteString("\nNonce: ")
	sb.WriteString(strings.TrimSpace(nonce))
	sb.WriteString("\nIssued At: ")
	sb.WriteString(issuedAt.UTC().Format(time.RFC3339))
	sb.WriteString("\nExpiration Time: ")
	sb.WriteString(expiresAt.UTC().Format(time.RFC3339))

	return sb.String()
}

func (s *Server) getSoulAgentRegistration(ctx context.Context, id string) (*models.SoulAgentRegistration, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("id is required")
	}
	var reg models.SoulAgentRegistration
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentRegistration{}).
		Where("PK", "=", fmt.Sprintf("SOUL_REG#%s", id)).
		Where("SK", "=", "REG").
		First(&reg)
	if err != nil {
		return nil, err
	}
	return &reg, nil
}

func (s *Server) loadSoulAgentRegistrationForVerify(ctx *apptheory.Context, id string) (*models.SoulAgentRegistration, *apptheory.AppTheoryError) {
	reg, err := s.getSoulAgentRegistration(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.not_found", "registration not found")
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	if !reg.ExpiresAt.IsZero() && time.Now().After(reg.ExpiresAt) {
		return nil, newAppTheoryError("app.bad_request", "registration expired")
	}
	if reg.Status == models.SoulAgentRegistrationStatusCompleted {
		return nil, newAppTheoryError("app.conflict", "registration already completed")
	}

	return reg, nil
}

func verifySoulRegistryDNS(ctx context.Context, domainNormalized, proofValue string) bool {
	domainNormalized = strings.TrimSpace(domainNormalized)
	proofValue = strings.TrimSpace(proofValue)
	if domainNormalized == "" || proofValue == "" {
		return false
	}

	txtName := soulRegistryProofPrefix + domainNormalized
	lookupCtx := ctx
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	rc, cancel := context.WithTimeout(lookupCtx, 4*time.Second)
	defer cancel()

	records, err := net.DefaultResolver.LookupTXT(rc, txtName)
	if err != nil {
		return false
	}
	for _, r := range records {
		if strings.TrimSpace(r) == proofValue {
			return true
		}
	}
	return false
}

func verifySoulRegistryHTTPS(ctx context.Context, domainNormalized, proofToken string) bool {
	proofToken = strings.TrimSpace(proofToken)
	if strings.TrimSpace(domainNormalized) == "" || proofToken == "" {
		return false
	}

	expectedLegacy := soulRegistryProofValue + proofToken

	status, body, err := fetchWellKnownHTTPSBody(ctx, domainNormalized, soulRegistryWellKnown)
	if err != nil {
		return false
	}
	if status != http.StatusOK {
		return false
	}
	if body == expectedLegacy {
		return true
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return false
	}
	v := strings.TrimSpace(parsed["lesser-soul-agent"])
	return v == proofToken || v == expectedLegacy
}

func verifySoulAgentRegistrationWallet(reg *models.SoulAgentRegistration, signature string) *apptheory.AppTheoryError {
	if reg == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if verifyErr := verifyEthereumSignature(reg.Wallet, reg.WalletMessage, strings.TrimSpace(signature)); verifyErr != nil {
		return newAppTheoryError("app.forbidden", "invalid signature")
	}
	return nil
}

func verifySoulAgentRegistrationProofs(ctx context.Context, reg *models.SoulAgentRegistration) (bool, bool, *apptheory.AppTheoryError) {
	if reg == nil {
		return false, false, newAppTheoryError("app.internal", "internal error")
	}

	proofToken := strings.TrimSpace(reg.ProofToken)
	proofValue := soulRegistryProofValue + proofToken
	verifiedDNS := reg.DNSVerified
	verifiedHTTPS := reg.HTTPSVerified

	if !verifiedDNS {
		if ok := verifySoulRegistryDNS(ctx, reg.DomainNormalized, proofValue); !ok {
			return false, false, newAppTheoryError("app.bad_request", "dns proof not found")
		}
		verifiedDNS = true
	}
	if !verifiedHTTPS {
		if ok := verifySoulRegistryHTTPS(ctx, reg.DomainNormalized, proofToken); !ok {
			return false, false, newAppTheoryError("app.bad_request", "https proof not found")
		}
		verifiedHTTPS = true
	}

	return verifiedDNS, verifiedHTTPS, nil
}

func (s *Server) completeSoulAgentRegistration(ctx *apptheory.Context, reg *models.SoulAgentRegistration, verifiedDNS bool, verifiedHTTPS bool, now time.Time) (*models.SoulAgentRegistration, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if ctx == nil || reg == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	update := &models.SoulAgentRegistration{
		ID:               reg.ID,
		Username:         reg.Username,
		DomainRaw:        reg.DomainRaw,
		DomainNormalized: reg.DomainNormalized,
		LocalIDRaw:       reg.LocalIDRaw,
		LocalID:          reg.LocalID,
		AgentID:          reg.AgentID,
		Wallet:           reg.Wallet,
		AuthorityModel:   soulRegistrationAuthorityModel(reg),
		Capabilities:     reg.Capabilities,
		WalletNonce:      reg.WalletNonce,
		WalletMessage:    reg.WalletMessage,
		ProofToken:       reg.ProofToken,
		DNSVerified:      verifiedDNS,
		HTTPSVerified:    verifiedHTTPS,
		WalletVerified:   true,
		VerifiedAt:       now,
		Status:           models.SoulAgentRegistrationStatusCompleted,
		CreatedAt:        reg.CreatedAt,
		UpdatedAt:        now,
		ExpiresAt:        reg.ExpiresAt,
		CompletedAt:      now,
	}
	_ = update.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update(
		"DNSVerified",
		"HTTPSVerified",
		"WalletVerified",
		"AuthorityModel",
		"VerifiedAt",
		"Status",
		"UpdatedAt",
		"CompletedAt",
	); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to update registration")
	}

	return update, nil
}

func (s *Server) getSoulOperation(ctx context.Context, id string) (*models.SoulOperation, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("id is required")
	}

	var op models.SoulOperation
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulOperation{}).
		Where("PK", "=", fmt.Sprintf("SOUL#OP#%s", id)).
		Where("SK", "=", "OPERATION").
		First(&op)
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func soulOpID(kind string, chainID int64, txTo string, agentID string, wallet string, metaURI string, extra string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	txTo = strings.ToLower(strings.TrimSpace(txTo))
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	wallet = strings.ToLower(strings.TrimSpace(wallet))
	metaURI = strings.TrimSpace(metaURI)
	extra = strings.TrimSpace(extra)

	var sb strings.Builder
	sb.WriteString(kind)
	sb.WriteString("|")
	sb.WriteString(strconv.FormatInt(chainID, 10))
	sb.WriteString("|")
	sb.WriteString(txTo)
	sb.WriteString("|")
	sb.WriteString(agentID)
	sb.WriteString("|")
	sb.WriteString(wallet)
	sb.WriteString("|")
	sb.WriteString(metaURI)
	if extra != "" {
		sb.WriteString("|")
		sb.WriteString(extra)
	}

	sum := sha256.Sum256([]byte(sb.String()))
	return "soulop_" + hex.EncodeToString(sum[:16])
}

func (s *Server) soulMetaURI(agentIDHex string) string {
	host := strings.TrimSpace(s.cfg.WebAuthnRPID)
	if host == "" {
		host = lesserHostDomain
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if agentIDHex == "" {
		return ""
	}
	return "https://" + host + "/api/v1/soul/agents/" + url.PathEscape(agentIDHex) + "/registration"
}

func (s *Server) createSoulMintOperation(ctx context.Context, reg *models.SoulAgentRegistration, principalAddress string, principalSignature string, principalDeclaration string, principalDeclaredAt string) (*models.SoulOperation, *safeTxPayload, string, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, nil, "", newAppTheoryError("app.internal", "internal error")
	}
	if reg == nil {
		return nil, nil, "", newAppTheoryError("app.internal", "internal error")
	}

	payload, metaURI, now, appErr := s.buildSoulMintPayload(reg, principalAddress)
	if appErr != nil {
		return nil, nil, "", appErr
	}

	opID := soulOpID(models.SoulOperationKindMint, s.cfg.SoulChainID, payload.To, reg.AgentID, reg.Wallet, metaURI, "selfMintSoul|principal="+strings.ToLower(strings.TrimSpace(principalAddress)))
	payloadJSON, appErr := marshalJSON(payload, "failed to encode safe tx")
	if appErr != nil {
		return nil, nil, "", appErr
	}

	op := &models.SoulOperation{
		OperationID:     opID,
		Kind:            models.SoulOperationKindMint,
		AgentID:         reg.AgentID,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: strings.TrimSpace(string(payloadJSON)),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_ = op.UpdateKeys()

	op, appErr = s.createOrLoadSoulOperation(ctx, op)
	if appErr != nil {
		return nil, nil, "", appErr
	}
	if storedPayload := parseSafeTxPayload(op.SafePayloadJSON); storedPayload != nil {
		payload = storedPayload
	}

	if appErr := s.ensureSoulPendingAgentIdentity(ctx, reg, metaURI, principalAddress, principalSignature, principalDeclaration, principalDeclaredAt, now); appErr != nil {
		return nil, nil, "", appErr
	}
	s.upsertSoulAgentIndexes(ctx, reg)

	return op, payload, metaURI, nil
}

func (s *Server) handleSoulAgentRegistrationPrincipalDeclarationPreflight(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	id, principalAddrRaw, principalDeclarationRaw, declaredAtRaw, err := parseSoulAgentRegistrationPrincipalDeclarationPreflightInput(ctx)
	if err != nil {
		return nil, err
	}

	reg, appErr := s.loadSoulAgentRegistrationForVerify(ctx, id)
	if appErr != nil {
		return nil, appErr
	}

	if ensureErr := s.ensureSoulAgentNotActive(ctx.Context(), reg.AgentID); ensureErr != nil {
		return nil, ensureErr
	}

	if _, _, accessErr := s.requireSoulDomainAccess(ctx, reg.DomainNormalized); accessErr != nil {
		return nil, accessErr
	}

	principalAddr, principalDeclaration, declaredAt, appErr := s.normalizeSoulRegistrationPrincipalDeclarationInputs(
		ctx.Context(),
		principalAddrRaw,
		principalDeclarationRaw,
		declaredAtRaw,
	)
	if appErr != nil {
		return nil, appErr
	}

	material, appErr := s.computeSoulPrincipalDeclarationSigningMaterial(reg, principalAddr, principalDeclaration, declaredAt)
	if appErr != nil {
		return nil, appErr
	}
	digestHex := "0x" + hex.EncodeToString(material.digest)

	return apptheory.JSON(http.StatusOK, soulAgentRegistrationPrincipalDeclarationPreflightResponse{
		Version:          "1",
		PrincipalAddress: principalAddr,
		SignerAddress:    principalAddr,
		SigningMethod:    "eip191_personal_sign",
		MessageEncoding:  "hex_bytes",
		MessageHex:       digestHex,
		DigestHex:        digestHex,
		CanonicalJSON:    material.canonicalJSON,
		DeclaredAt:       declaredAt,
	})
}

func (s *Server) handleSoulAgentRegistrationVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	id, sig, principalAddrRaw, principalDeclarationRaw, principalSigRaw, declaredAtRaw, err := parseSoulAgentRegistrationVerifyInput(ctx)
	if err != nil {
		return nil, err
	}

	reg, appErr := s.loadSoulAgentRegistrationForVerify(ctx, id)
	if appErr != nil {
		return nil, appErr
	}

	if ensureErr := s.ensureSoulAgentNotActive(ctx.Context(), reg.AgentID); ensureErr != nil {
		return nil, ensureErr
	}

	if _, _, accessErr := s.requireSoulDomainAccess(ctx, reg.DomainNormalized); accessErr != nil {
		return nil, accessErr
	}

	if verifyErr := verifySoulAgentRegistrationWallet(reg, sig); verifyErr != nil {
		return nil, verifyErr
	}

	verifiedDNS, verifiedHTTPS, verifyErr := verifySoulAgentRegistrationProofs(ctx.Context(), reg)
	if verifyErr != nil {
		return nil, verifyErr
	}

	principalAddr, principalDeclaration, principalSig, declaredAt, appErr := s.validateSoulRegistrationVerifyPrincipalInputs(
		ctx.Context(),
		reg,
		principalAddrRaw,
		principalDeclarationRaw,
		principalSigRaw,
		declaredAtRaw,
	)
	if appErr != nil {
		return nil, appErr
	}

	op, safeTx, _, opErr := s.createSoulMintOperation(ctx.Context(), reg, principalAddr, principalSig, principalDeclaration, declaredAt)
	if opErr != nil {
		return nil, opErr
	}

	now := time.Now().UTC()
	update, compErr := s.completeSoulAgentRegistration(ctx, reg, verifiedDNS, verifiedHTTPS, now)
	if compErr != nil {
		return nil, compErr
	}
	promotion, _ := s.getSoulAgentPromotion(ctx.Context(), reg.AgentID)
	promotion = updateSoulAgentPromotionForVerification(promotion, reg, op, principalAddr, now)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return nil, appErr
	}
	if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:   models.SoulAgentPromotionEventTypeRequestApproved,
		RequestID:   strings.TrimSpace(ctx.RequestID),
		OperationID: op.OperationID,
		OccurredAt:  now,
	})); appErr != nil {
		return nil, appErr
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.registration.verify",
		Target:    fmt.Sprintf("soul_agent_registration:%s", reg.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	return apptheory.JSON(http.StatusOK, soulAgentRegistrationVerifyResponse{
		Registration: *update,
		Operation:    *op,
		SafeTx:       safeTx,
		Promotion:    ptrTo(s.buildSoulAgentPromotionView(promotion)),
	})
}

func (s *Server) buildSoulMintPayload(reg *models.SoulAgentRegistration, principalAddress string) (*safeTxPayload, string, time.Time, *apptheory.AppTheoryError) {
	if reg == nil {
		return nil, "", time.Time{}, newAppTheoryError("app.internal", "internal error")
	}
	if !common.IsHexAddress(principalAddress) {
		return nil, "", time.Time{}, newAppTheoryError("app.bad_request", "invalid principal_address")
	}

	contractAddr, txTo, appErr := s.soulRegistryContractAddress()
	if appErr != nil {
		return nil, "", time.Time{}, appErr
	}

	signerKey := strings.TrimSpace(s.cfg.SoulMintSignerKey)
	if signerKey == "" {
		return nil, "", time.Time{}, newAppTheoryError("app.conflict", "mint signer key is not configured")
	}

	metaURI := s.soulMetaURI(reg.AgentID)
	if metaURI == "" {
		return nil, "", time.Time{}, newAppTheoryError("app.internal", "failed to derive meta_uri")
	}

	if !common.IsHexAddress(reg.Wallet) {
		return nil, "", time.Time{}, newAppTheoryError("app.bad_request", "invalid wallet address")
	}
	to := common.HexToAddress(reg.Wallet)
	principal := common.HexToAddress(principalAddress)

	// Minting is intentionally direct-wallet even when other soul operations stay Safe-mediated.
	submitter := to

	agentInt, ok := new(big.Int).SetString(strings.TrimPrefix(strings.TrimSpace(reg.AgentID), "0x"), 16)
	if !ok {
		return nil, "", time.Time{}, newAppTheoryError("app.bad_request", "invalid agent_id")
	}

	now := time.Now().UTC()
	deadlineUnix := now.Add(30 * time.Minute).Unix()
	deadline := big.NewInt(deadlineUnix)

	attestation, err := soul.SignSelfMintAttestation(signerKey, s.cfg.SoulChainID, contractAddr, to, agentInt, metaURI, 0, principal, deadline, submitter)
	if err != nil {
		return nil, "", time.Time{}, newAppTheoryError("app.internal", "failed to sign mint attestation")
	}

	// Default mint fee: 0.0005 ETH = 500000000000000 wei.
	mintFeeWei := big.NewInt(500000000000000)

	data, err := soul.EncodeSelfMintSoulCall(to, agentInt, metaURI, 0, principal, deadline, attestation)
	if err != nil {
		return nil, "", time.Time{}, newAppTheoryError("app.internal", "failed to encode transaction")
	}

	payload := &safeTxPayload{
		SafeAddress: "",
		To:          txTo,
		Value:       mintFeeWei.String(),
		Data:        "0x" + hex.EncodeToString(data),
	}

	return payload, metaURI, now, nil
}

func (s *Server) createOrLoadSoulOperation(ctx context.Context, op *models.SoulOperation) (*models.SoulOperation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || op == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	if err := s.store.DB.WithContext(ctx).Model(op).IfNotExists().Create(); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return nil, newAppTheoryError("app.internal", "failed to create operation")
		}

		existing, getErr := s.getSoulOperation(ctx, op.OperationID)
		if getErr == nil && existing != nil {
			return existing, nil
		}
	}

	return op, nil
}

func (s *Server) ensureSoulPendingAgentIdentity(ctx context.Context, reg *models.SoulAgentRegistration, metaURI string, principalAddress string, principalSignature string, principalDeclaration string, principalDeclaredAt string, now time.Time) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || reg == nil {
		return newAppTheoryError("app.internal", "internal error")
	}

	identity := buildSoulPendingAgentIdentity(reg, metaURI, principalAddress, principalSignature, principalDeclaration, principalDeclaredAt, now)
	_ = identity.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(identity).IfNotExists().Create(); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return newAppTheoryError("app.internal", "failed to create agent identity")
		}
		if appErr := s.reconcileSoulPendingIdentity(ctx, reg.AgentID, principalAddress, principalSignature, principalDeclaration, principalDeclaredAt, now); appErr != nil {
			return appErr
		}
	}

	return nil
}

func (s *Server) ensureSoulHostedInstanceTrustIdentity(ctx context.Context, reg *models.SoulAgentRegistration, now time.Time) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || reg == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if normalizeSoulAuthorityModel(reg.AuthorityModel) != models.SoulAuthorityModelInstanceTrust {
		return newAppTheoryError("app.internal", "internal error")
	}

	identity := buildSoulHostedInstanceTrustIdentity(reg, s.soulMetaURI(reg.AgentID), now)
	_ = identity.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(identity).IfNotExists().Create(); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return newAppTheoryError("app.internal", "failed to create hosted agent identity")
		}
		existing, getErr := s.getSoulAgentIdentity(ctx, reg.AgentID)
		if getErr != nil {
			return newAppTheoryError("app.internal", "failed to load hosted agent identity")
		}
		if strings.EqualFold(strings.TrimSpace(existing.Status), models.SoulAgentStatusActive) || existing.SelfDescriptionVersion > 0 {
			return newAppTheoryError("app.conflict", "agent is already registered")
		}
		if normalizeSoulAuthorityModel(existing.AuthorityModel) != models.SoulAuthorityModelInstanceTrust {
			return newAppTheoryError("app.conflict", "agent namespace is already reserved by another authority model")
		}
	}
	return nil
}

func buildSoulPendingAgentIdentity(
	reg *models.SoulAgentRegistration,
	metaURI string,
	principalAddress string,
	principalSignature string,
	principalDeclaration string,
	principalDeclaredAt string,
	now time.Time,
) *models.SoulAgentIdentity {
	identity := &models.SoulAgentIdentity{
		AgentID:              reg.AgentID,
		Domain:               reg.DomainNormalized,
		LocalID:              reg.LocalID,
		Wallet:               reg.Wallet,
		AuthorityModel:       firstNonEmpty(reg.AuthorityModel, models.SoulAuthorityModelWalletPrincipal),
		TokenID:              reg.AgentID,
		MetaURI:              metaURI,
		Capabilities:         reg.Capabilities,
		PrincipalAddress:     principalAddress,
		PrincipalSignature:   principalSignature,
		PrincipalDeclaration: principalDeclaration,
		PrincipalDeclaredAt:  principalDeclaredAt,
		Status:               models.SoulAgentStatusPending,
		UpdatedAt:            now,
	}
	applyHostedBoundSoulPolicyDefaults(identity)
	return identity
}

func buildSoulHostedInstanceTrustIdentity(reg *models.SoulAgentRegistration, metaURI string, now time.Time) *models.SoulAgentIdentity {
	identity := &models.SoulAgentIdentity{
		AgentID:         reg.AgentID,
		Domain:          reg.DomainNormalized,
		LocalID:         reg.LocalID,
		Wallet:          "",
		AuthorityModel:  models.SoulAuthorityModelInstanceTrust,
		TokenID:         "",
		MetaURI:         metaURI,
		Capabilities:    reg.Capabilities,
		Status:          models.SoulAgentStatusPending,
		LifecycleStatus: models.SoulAgentStatusPending,
		AnchorState:     models.SoulAnchorStateHostedOffchain,
		UpdatedAt:       now,
	}
	applyHostedBoundSoulPolicyDefaults(identity)
	return identity
}

func (s *Server) replaySoulHostedInstanceTrustBegin(ctx context.Context, agentIDHex string, actor string) (soulAgentRegistrationBeginResponse, bool, *apptheory.AppTheoryError) {
	identity, err := s.getSoulAgentIdentity(ctx, agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return soulAgentRegistrationBeginResponse{}, false, nil
	}
	if err != nil {
		return soulAgentRegistrationBeginResponse{}, false, newAppTheoryError("app.internal", "internal error")
	}
	if identity == nil {
		return soulAgentRegistrationBeginResponse{}, false, nil
	}
	if strings.EqualFold(strings.TrimSpace(identity.Status), models.SoulAgentStatusActive) || identity.SelfDescriptionVersion > 0 {
		return soulAgentRegistrationBeginResponse{}, false, newAppTheoryError("app.conflict", "agent is already registered")
	}
	if normalizeSoulAuthorityModel(identity.AuthorityModel) != models.SoulAuthorityModelInstanceTrust {
		return soulAgentRegistrationBeginResponse{}, false, newAppTheoryError("app.conflict", "agent namespace is already reserved by another authority model")
	}

	promotion, err := s.getSoulAgentPromotion(ctx, agentIDHex)
	if err != nil || promotion == nil || strings.TrimSpace(promotion.RegistrationID) == "" {
		return soulAgentRegistrationBeginResponse{}, false, nil
	}
	if normalizeSoulAuthorityModel(promotion.AuthorityModel) != models.SoulAuthorityModelInstanceTrust {
		return soulAgentRegistrationBeginResponse{}, false, nil
	}
	shouldRestart, restartErr := s.hostedGenesisRestartRequiredForActor(ctx, actor, promotion.LatestConversationID)
	if restartErr != nil {
		return soulAgentRegistrationBeginResponse{}, false, restartErr
	}
	if shouldRestart {
		// The previous registration is a failed lane. Let the normal begin
		// path mint a fresh registration/conversation lane rather than replaying
		// stale pending state forever.
		return soulAgentRegistrationBeginResponse{}, false, nil
	}
	reg, replay, replayErr := s.loadSoulHostedInstanceTrustReplayRegistration(ctx, promotion.RegistrationID)
	if replayErr != nil {
		return soulAgentRegistrationBeginResponse{}, false, replayErr
	}
	if !replay {
		return soulAgentRegistrationBeginResponse{}, false, nil
	}
	return soulAgentRegistrationBeginResponse{
		Registration: *reg,
		Proofs:       []soulRegistryProofInstructions{},
		Promotion:    ptrTo(s.buildSoulAgentPromotionView(promotion)),
	}, true, nil
}

func (s *Server) loadSoulHostedInstanceTrustReplayRegistration(ctx context.Context, registrationID string) (*models.SoulAgentRegistration, bool, *apptheory.AppTheoryError) {
	reg, err := s.getSoulAgentRegistration(ctx, registrationID)
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, newAppTheoryError("app.internal", "internal error")
	}
	if reg == nil || normalizeSoulAuthorityModel(reg.AuthorityModel) != models.SoulAuthorityModelInstanceTrust {
		return nil, false, nil
	}
	return reg, true, nil
}

func (s *Server) hostedGenesisRestartRequiredForActor(ctx context.Context, actor string, conversationID string) (bool, *apptheory.AppTheoryError) {
	instanceSlug := soulInstanceSlugFromActor(actor)
	conversationID = strings.TrimSpace(conversationID)
	if instanceSlug == "" || conversationID == "" {
		return false, nil
	}
	session, err := s.store.GetHostedGenesisSession(ctx, instanceSlug, conversationID)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return false, newAppTheoryError("app.internal", "internal error")
	}
	return hostedGenesisSessionRequiresRestart(session), nil
}

func soulInstanceSlugFromActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if !strings.HasPrefix(actor, "instance:") {
		return ""
	}
	slug := strings.TrimSpace(strings.TrimPrefix(actor, "instance:"))
	if slug == "" || slug == "unknown" {
		return ""
	}
	return strings.ToLower(slug)
}

func (s *Server) reconcileSoulPendingIdentity(
	ctx context.Context,
	agentID string,
	principalAddress string,
	principalSignature string,
	principalDeclaration string,
	principalDeclaredAt string,
	now time.Time,
) *apptheory.AppTheoryError {
	existing, getErr := s.getSoulAgentIdentity(ctx, agentID)
	if getErr != nil && !theoryErrors.IsNotFound(getErr) {
		return newAppTheoryError("app.internal", "failed to load agent identity")
	}
	if existing == nil {
		return nil
	}
	if strings.TrimSpace(existing.Status) == models.SoulAgentStatusActive {
		return newAppTheoryError("app.conflict", "agent is already registered")
	}
	if appErr := validateExistingSoulPendingIdentity(existing, principalAddress, principalDeclaration, principalDeclaredAt); appErr != nil {
		return appErr
	}
	if !needsSoulPendingIdentityPrincipalUpdate(existing, principalAddress) {
		return nil
	}

	update := &models.SoulAgentIdentity{
		AgentID:              agentID,
		PrincipalAddress:     principalAddress,
		PrincipalSignature:   principalSignature,
		PrincipalDeclaration: principalDeclaration,
		PrincipalDeclaredAt:  principalDeclaredAt,
		UpdatedAt:            now,
	}
	_ = update.UpdateKeys()
	if updErr := s.store.DB.WithContext(ctx).Model(update).IfExists().Update("PrincipalAddress", "PrincipalSignature", "PrincipalDeclaration", "PrincipalDeclaredAt", "UpdatedAt"); updErr != nil {
		return newAppTheoryError("app.internal", "failed to update agent identity")
	}
	return nil
}

func validateExistingSoulPendingIdentity(existing *models.SoulAgentIdentity, principalAddress string, principalDeclaration string, principalDeclaredAt string) *apptheory.AppTheoryError {
	wantPrincipal := strings.ToLower(strings.TrimSpace(principalAddress))
	havePrincipal := strings.ToLower(strings.TrimSpace(existing.PrincipalAddress))
	if havePrincipal != "" && wantPrincipal != "" && !strings.EqualFold(havePrincipal, wantPrincipal) {
		return newAppTheoryError("app.conflict", "principal_address mismatch for existing identity")
	}
	wantDecl := strings.TrimSpace(principalDeclaration)
	haveDecl := strings.TrimSpace(existing.PrincipalDeclaration)
	if haveDecl != "" && wantDecl != "" && haveDecl != wantDecl {
		return newAppTheoryError("app.conflict", "principal_declaration mismatch for existing identity")
	}
	wantDeclaredAt := strings.TrimSpace(principalDeclaredAt)
	haveDeclaredAt := strings.TrimSpace(existing.PrincipalDeclaredAt)
	if haveDeclaredAt != "" && wantDeclaredAt != "" && haveDeclaredAt != wantDeclaredAt {
		return newAppTheoryError("app.conflict", "declared_at mismatch for existing identity")
	}
	return nil
}

func needsSoulPendingIdentityPrincipalUpdate(existing *models.SoulAgentIdentity, principalAddress string) bool {
	wantPrincipal := strings.ToLower(strings.TrimSpace(principalAddress))
	needUpdate := strings.TrimSpace(existing.PrincipalAddress) == "" ||
		strings.TrimSpace(existing.PrincipalSignature) == "" ||
		strings.TrimSpace(existing.PrincipalDeclaration) == "" ||
		strings.TrimSpace(existing.PrincipalDeclaredAt) == ""
	return wantPrincipal != "" && needUpdate
}

func (s *Server) validateSoulRegistrationVerifyPrincipalInputs(
	ctx context.Context,
	reg *models.SoulAgentRegistration,
	principalAddrRaw string,
	principalDeclarationRaw string,
	principalSigRaw string,
	declaredAtRaw string,
) (string, string, string, string, *apptheory.AppTheoryError) {
	principalAddr, principalDeclaration, declaredAtCanonical, appErr := s.normalizeSoulRegistrationPrincipalDeclarationInputs(ctx, principalAddrRaw, principalDeclarationRaw, declaredAtRaw)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	principalSig := strings.TrimSpace(principalSigRaw)
	if principalSig == "" {
		return "", "", "", "", newAppTheoryError("app.bad_request", "principal_signature is required")
	}
	principalDigest, appErr := s.computeSoulPrincipalDeclarationDigest(reg, principalAddr, principalDeclaration, declaredAtCanonical)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	if err := verifyEthereumSignatureBytes(principalAddr, principalDigest, principalSig); err != nil {
		return "", "", "", "", newAppTheoryError("app.bad_request", "invalid principal_signature")
	}
	return principalAddr, principalDeclaration, principalSig, declaredAtCanonical, nil
}

func (s *Server) normalizeSoulRegistrationPrincipalDeclarationInputs(ctx context.Context, principalAddrRaw string, principalDeclarationRaw string, declaredAtRaw string) (string, string, string, *apptheory.AppTheoryError) {
	principalAddr, appErr := s.normalizeSoulEVMAddress(ctx, principalAddrRaw, "principal_address")
	if appErr != nil {
		return "", "", "", appErr
	}
	principalDeclaration := strings.TrimSpace(principalDeclarationRaw)
	if principalDeclaration == "" || len(principalDeclaration) < 10 {
		return "", "", "", newAppTheoryError("app.bad_request", "principal_declaration is required")
	}
	if len(principalDeclaration) > 8192 {
		return "", "", "", newAppTheoryError("app.bad_request", "principal_declaration is too long")
	}
	declaredAt := strings.TrimSpace(declaredAtRaw)
	if declaredAt == "" {
		return "", "", "", newAppTheoryError("app.bad_request", "declared_at is required")
	}
	_, declaredAtCanonical, tsErr := parseSoulSignedTimestamp(declaredAt, time.Now().UTC(), "declared_at")
	if tsErr != nil {
		if tsErr.Message == "declared_at must be RFC3339" {
			tsErr.Message = "declared_at must be an RFC3339 timestamp"
		}
		return "", "", "", tsErr
	}
	return principalAddr, principalDeclaration, declaredAtCanonical, nil
}

type soulPrincipalDeclarationSigningMaterial struct {
	digest        []byte
	canonicalJSON string
}

func (s *Server) computeSoulPrincipalDeclarationDigest(reg *models.SoulAgentRegistration, principalAddr string, principalDeclaration string, declaredAt string) ([]byte, *apptheory.AppTheoryError) {
	material, appErr := s.computeSoulPrincipalDeclarationSigningMaterial(reg, principalAddr, principalDeclaration, declaredAt)
	if appErr != nil {
		return nil, appErr
	}
	return material.digest, nil
}

func (s *Server) computeSoulPrincipalDeclarationSigningMaterial(reg *models.SoulAgentRegistration, principalAddr string, principalDeclaration string, declaredAt string) (soulPrincipalDeclarationSigningMaterial, *apptheory.AppTheoryError) {
	if s == nil || reg == nil {
		return soulPrincipalDeclarationSigningMaterial{}, newAppTheoryError("app.internal", "internal error")
	}
	if s.cfg.SoulChainID <= 0 || strings.TrimSpace(s.cfg.SoulRegistryContractAddress) == "" {
		return soulPrincipalDeclarationSigningMaterial{}, newAppTheoryError("app.conflict", "soul registry is not configured")
	}
	unsigned := map[string]any{
		"kind":             "soul_principal_declaration",
		"version":          "1",
		"agentId":          strings.ToLower(strings.TrimSpace(reg.AgentID)),
		"wallet":           strings.ToLower(strings.TrimSpace(reg.Wallet)),
		"domain":           strings.ToLower(strings.TrimSpace(reg.DomainNormalized)),
		"localId":          strings.TrimSpace(reg.LocalID),
		"chainId":          strconv.FormatInt(s.cfg.SoulChainID, 10),
		"contract":         strings.ToLower(strings.TrimSpace(s.cfg.SoulRegistryContractAddress)),
		"principalAddress": strings.ToLower(strings.TrimSpace(principalAddr)),
		"declaration":      strings.TrimSpace(principalDeclaration),
		"declaredAt":       strings.TrimSpace(declaredAt),
	}
	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return soulPrincipalDeclarationSigningMaterial{}, newAppTheoryError("app.bad_request", "invalid principal declaration JSON")
	}
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		return soulPrincipalDeclarationSigningMaterial{}, newAppTheoryError("app.bad_request", "invalid principal declaration JSON")
	}
	return soulPrincipalDeclarationSigningMaterial{
		digest:        crypto.Keccak256(jcsBytes),
		canonicalJSON: string(jcsBytes),
	}, nil
}

func (s *Server) upsertSoulAgentIndexes(ctx context.Context, reg *models.SoulAgentRegistration) {
	if s == nil || s.store == nil || s.store.DB == nil || reg == nil {
		return
	}

	if strings.TrimSpace(reg.Wallet) != "" {
		wi := &models.SoulWalletAgentIndex{Wallet: reg.Wallet, AgentID: reg.AgentID}
		_ = wi.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(wi).CreateOrUpdate()
	}

	di := &models.SoulDomainAgentIndex{Domain: reg.DomainNormalized, LocalID: reg.LocalID, AgentID: reg.AgentID}
	_ = di.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(di).CreateOrUpdate()

	for _, cap := range normalizeSoulCapabilitiesLoose(reg.Capabilities) {
		ci := &models.SoulCapabilityAgentIndex{
			Capability: cap,
			ClaimLevel: soulClaimLevelSelfDeclared,
			Domain:     reg.DomainNormalized,
			LocalID:    reg.LocalID,
			AgentID:    reg.AgentID,
		}
		_ = ci.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(ci).CreateOrUpdate()
	}
}

func parseSoulAgentRegistrationVerifyInput(ctx *apptheory.Context) (id string, sig string, principalAddr string, principalDeclaration string, principalSig string, declaredAt string, err error) {
	if ctx == nil {
		return "", "", "", "", "", "", newAppTheoryError("app.internal", "internal error")
	}

	id = strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return "", "", "", "", "", "", newAppTheoryError("app.bad_request", "id is required")
	}

	var req soulAgentRegistrationVerifyRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return "", "", "", "", "", "", err
	}

	sig = strings.TrimSpace(req.Signature)
	if sig == "" {
		return "", "", "", "", "", "", newAppTheoryError("app.bad_request", "signature is required")
	}

	principalAddr = strings.TrimSpace(req.PrincipalAddress)
	principalDeclaration = strings.TrimSpace(req.PrincipalDeclaration)
	principalSig = strings.TrimSpace(req.PrincipalSignature)
	declaredAt = strings.TrimSpace(req.DeclaredAt)

	return id, sig, principalAddr, principalDeclaration, principalSig, declaredAt, nil
}

func parseSoulAgentRegistrationPrincipalDeclarationPreflightInput(ctx *apptheory.Context) (id string, principalAddr string, principalDeclaration string, declaredAt string, err error) {
	if ctx == nil {
		return "", "", "", "", newAppTheoryError("app.internal", "internal error")
	}

	id = strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return "", "", "", "", newAppTheoryError("app.bad_request", "id is required")
	}

	var req soulAgentRegistrationPrincipalDeclarationPreflightRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return "", "", "", "", err
	}

	principalAddr = strings.TrimSpace(req.PrincipalAddress)
	principalDeclaration = strings.TrimSpace(req.PrincipalDeclaration)
	declaredAt = strings.TrimSpace(req.DeclaredAt)

	return id, principalAddr, principalDeclaration, declaredAt, nil
}
