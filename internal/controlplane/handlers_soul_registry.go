package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
	Domain       string   `json:"domain"`
	LocalID      string   `json:"local_id"`
	Wallet       string   `json:"wallet_address"`
	Capabilities []string `json:"capabilities,omitempty"`
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
	Wallet       walletChallengeResponse         `json:"wallet"`
	Proofs       []soulRegistryProofInstructions `json:"proofs"`
}

type soulAgentRegistrationVerifyRequest struct {
	Signature string `json:"signature"`
}

type soulAgentRegistrationVerifyResponse struct {
	Registration models.SoulAgentRegistration `json:"registration"`
	Operation    models.SoulOperation         `json:"operation"`
	SafeTx       *safeTxPayload               `json:"safe_tx,omitempty"`
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

func normalizeSoulCapabilitiesStrict(supported []string, requested []string) ([]string, *apptheory.AppError) {
	out := normalizeSoulCapabilitiesLoose(requested)
	if len(out) == 0 {
		return nil, nil
	}

	allowed := normalizeSoulCapabilitiesLoose(supported)
	if len(allowed) == 0 {
		return out, nil
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, c := range allowed {
		allowedSet[c] = struct{}{}
	}
	for _, c := range out {
		if _, ok := allowedSet[c]; !ok {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "unsupported capability: " + c}
		}
	}
	return out, nil
}

func (s *Server) normalizeSoulWalletAddress(ctx context.Context, walletAddr string) (string, *apptheory.AppError) {
	walletAddr = strings.TrimSpace(walletAddr)
	if walletAddr == "" {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "wallet_address is required"}
	}
	if !common.IsHexAddress(walletAddr) {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid wallet_address"}
	}
	walletAddr = strings.ToLower(walletAddr)
	if appErr := validateNotReservedWalletAddress(walletAddr, "wallet_address"); appErr != nil {
		return "", appErr
	}
	if appErr := s.validateNotPrivilegedWalletAddress(ctx, walletTypeEthereum, walletAddr, "wallet_address"); appErr != nil {
		return "", appErr
	}
	return walletAddr, nil
}

func (s *Server) soulRegistryContractAddress() (common.Address, string, *apptheory.AppError) {
	contractAddrRaw := strings.TrimSpace(s.cfg.SoulRegistryContractAddress)
	if !common.IsHexAddress(contractAddrRaw) {
		return common.Address{}, "", &apptheory.AppError{Code: "app.conflict", Message: "soul registry is not configured"}
	}
	contractAddr := common.HexToAddress(contractAddrRaw)
	txTo := strings.ToLower(contractAddr.Hex())
	return contractAddr, txTo, nil
}

func (s *Server) soulRegistrySafeAddress() (string, *apptheory.AppError) {
	safeAddr := strings.ToLower(strings.TrimSpace(s.cfg.SoulAdminSafeAddress))
	if strings.ToLower(strings.TrimSpace(s.cfg.SoulTxMode)) == tipTxModeSafe && !common.IsHexAddress(safeAddr) {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "soul registry safe is not configured"}
	}
	return safeAddr, nil
}

func (s *Server) requireSoulPortalPrereqs(ctx *apptheory.Context) *apptheory.AppError {
	if err := requireAuthenticated(ctx); err != nil {
		if appErr, ok := err.(*apptheory.AppError); ok {
			return appErr
		}
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if appErr := s.requirePortalApproved(ctx); appErr != nil {
		return appErr
	}
	return nil
}

func (s *Server) requireSoulDomainAccess(ctx *apptheory.Context, normalizedDomain string) (*models.Domain, *models.Instance, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	normalizedDomain = strings.ToLower(strings.TrimSpace(normalizedDomain))
	if normalizedDomain == "" {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "domain is required"}
	}

	var d models.Domain
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", normalizedDomain)).
		Where("SK", "=", models.SKMetadata).
		First(&d)
	if theoryErrors.IsNotFound(err) {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "domain is not registered"}
	}
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !domainIsVerifiedOrActive(d.Status) {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "domain is not verified"}
	}

	inst, instErr := s.requireInstanceAccess(ctx, strings.TrimSpace(d.InstanceSlug))
	if instErr != nil {
		if appErr, ok := instErr.(*apptheory.AppError); ok {
			return nil, nil, appErr
		}
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return &d, inst, nil
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
	domainNormalized, err := domains.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, domainNormalized); accessErr != nil {
		return nil, accessErr
	}

	rawLocal := strings.TrimSpace(req.LocalID)
	local, err := soul.NormalizeLocalAgentID(rawLocal)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	wallet, appErr := s.normalizeSoulWalletAddress(ctx.Context(), req.Wallet)
	if appErr != nil {
		return nil, appErr
	}

	caps, appErr := normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, req.Capabilities)
	if appErr != nil {
		return nil, appErr
	}

	agentIDHex, err := soul.DeriveAgentIDHex(domainNormalized, local)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to derive agent_id"}
	}

	// Block re-registration of an active agent.
	existing, getErr := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if getErr == nil && existing != nil && strings.TrimSpace(existing.Status) == models.SoulAgentStatusActive {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent is already registered"}
	}
	if getErr != nil && !theoryErrors.IsNotFound(getErr) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	proofToken, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create proof token"}
	}
	proofValue := soulRegistryProofValue + proofToken

	nonce, err := generateNonce()
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create nonce"}
	}

	id, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create registration id"}
	}

	now := time.Now().UTC()
	expiresAt := now.Add(30 * time.Minute)

	msg := buildSoulRegistryWalletMessage(domainNormalized, local, agentIDHex, wallet, s.cfg.SoulChainID, caps, proofValue, nonce, now, expiresAt)

	reg := &models.SoulAgentRegistration{
		ID:               id,
		Username:         strings.TrimSpace(ctx.AuthIdentity),
		DomainRaw:        rawDomain,
		DomainNormalized: domainNormalized,
		LocalIDRaw:       rawLocal,
		LocalID:          local,
		AgentID:          agentIDHex,
		Wallet:           wallet,
		Capabilities:     caps,
		WalletNonce:      nonce,
		WalletMessage:    msg,
		ProofToken:       proofToken,
		Status:           models.SoulAgentRegistrationStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        expiresAt,
	}
	_ = reg.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(reg).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create registration"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.registration.begin",
		Target:    fmt.Sprintf("soul_agent_registration:%s", reg.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	dnsName := soulRegistryProofPrefix + domainNormalized
	httpsURL := "https://" + domainNormalized + path.Clean(soulRegistryWellKnown)

	return apptheory.JSON(http.StatusCreated, soulAgentRegistrationBeginResponse{
		Registration: *reg,
		Wallet: walletChallengeResponse{
			ID:        reg.ID,
			Username:  strings.TrimSpace(ctx.AuthIdentity),
			Address:   wallet,
			ChainID:   int(s.cfg.SoulChainID),
			Nonce:     nonce,
			Message:   msg,
			IssuedAt:  now,
			ExpiresAt: expiresAt,
		},
		Proofs: []soulRegistryProofInstructions{
			{Method: "dns_txt", DNSName: dnsName, DNSValue: proofValue},
			{Method: "https_well_known", HTTPSURL: httpsURL, HTTPSBody: proofValue},
		},
	})
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
	sb.WriteString(fmt.Sprintf("%d", chainID))
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

func (s *Server) loadSoulAgentRegistrationForVerify(ctx *apptheory.Context, id string) (*models.SoulAgentRegistration, *apptheory.AppError) {
	reg, err := s.getSoulAgentRegistration(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "registration not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if !reg.ExpiresAt.IsZero() && time.Now().After(reg.ExpiresAt) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "registration expired"}
	}
	if reg.Status == models.SoulAgentRegistrationStatusCompleted {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration already completed"}
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

func verifySoulRegistryHTTPS(ctx context.Context, domainNormalized, proofValue string) bool {
	return verifyWellKnownHTTPS(ctx, domainNormalized, soulRegistryWellKnown, proofValue)
}

func verifySoulAgentRegistrationWallet(reg *models.SoulAgentRegistration, signature string) *apptheory.AppError {
	if reg == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if verifyErr := verifyEthereumSignature(reg.Wallet, reg.WalletMessage, strings.TrimSpace(signature)); verifyErr != nil {
		return &apptheory.AppError{Code: "app.forbidden", Message: "invalid signature"}
	}
	return nil
}

func verifySoulAgentRegistrationProofs(ctx context.Context, reg *models.SoulAgentRegistration) (bool, bool, *apptheory.AppError) {
	if reg == nil {
		return false, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	proofValue := soulRegistryProofValue + strings.TrimSpace(reg.ProofToken)
	verifiedDNS := reg.DNSVerified
	verifiedHTTPS := reg.HTTPSVerified

	if !verifiedDNS {
		if ok := verifySoulRegistryDNS(ctx, reg.DomainNormalized, proofValue); !ok {
			return false, false, &apptheory.AppError{Code: "app.bad_request", Message: "dns proof not found"}
		}
		verifiedDNS = true
	}
	if !verifiedHTTPS {
		if ok := verifySoulRegistryHTTPS(ctx, reg.DomainNormalized, proofValue); !ok {
			return false, false, &apptheory.AppError{Code: "app.bad_request", Message: "https proof not found"}
		}
		verifiedHTTPS = true
	}

	return verifiedDNS, verifiedHTTPS, nil
}

func (s *Server) completeSoulAgentRegistration(ctx *apptheory.Context, reg *models.SoulAgentRegistration, verifiedDNS bool, verifiedHTTPS bool, now time.Time) (*models.SoulAgentRegistration, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil || reg == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
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
		"VerifiedAt",
		"Status",
		"UpdatedAt",
		"CompletedAt",
	); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update registration"}
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

func soulOpID(kind string, chainID int64, txTo string, agentID string, wallet string, metaURI string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	txTo = strings.ToLower(strings.TrimSpace(txTo))
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	wallet = strings.ToLower(strings.TrimSpace(wallet))
	metaURI = strings.TrimSpace(metaURI)

	var sb strings.Builder
	sb.WriteString(kind)
	sb.WriteString("|")
	sb.WriteString(fmt.Sprintf("%d", chainID))
	sb.WriteString("|")
	sb.WriteString(txTo)
	sb.WriteString("|")
	sb.WriteString(agentID)
	sb.WriteString("|")
	sb.WriteString(wallet)
	sb.WriteString("|")
	sb.WriteString(metaURI)

	sum := sha256.Sum256([]byte(sb.String()))
	return "soulop_" + hex.EncodeToString(sum[:16])
}

func (s *Server) soulMetaURI(agentIDHex string) string {
	host := strings.TrimSpace(s.cfg.WebAuthnRPID)
	if host == "" {
		host = "lesser.host"
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if agentIDHex == "" {
		return ""
	}
	return "https://" + host + "/api/v1/soul/agents/" + url.PathEscape(agentIDHex) + "/registration"
}

func (s *Server) createSoulMintOperation(ctx context.Context, reg *models.SoulAgentRegistration) (*models.SoulOperation, *safeTxPayload, string, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, nil, "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if reg == nil {
		return nil, nil, "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	_, txTo, appErr := s.soulRegistryContractAddress()
	if appErr != nil {
		return nil, nil, "", appErr
	}

	metaURI := s.soulMetaURI(reg.AgentID)
	if metaURI == "" {
		return nil, nil, "", &apptheory.AppError{Code: "app.internal", Message: "failed to derive meta_uri"}
	}

	if !common.IsHexAddress(reg.Wallet) {
		return nil, nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid wallet address"}
	}
	to := common.HexToAddress(reg.Wallet)

	agentInt, ok := new(big.Int).SetString(strings.TrimPrefix(strings.TrimSpace(reg.AgentID), "0x"), 16)
	if !ok {
		return nil, nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid agent_id"}
	}

	data, err := soul.EncodeMintSoulCall(to, agentInt, metaURI)
	if err != nil {
		return nil, nil, "", &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	txData := "0x" + hex.EncodeToString(data)
	txValue := "0"
	safeAddr, appErr := s.soulRegistrySafeAddress()
	if appErr != nil {
		return nil, nil, "", appErr
	}

	opID := soulOpID(models.SoulOperationKindMint, s.cfg.SoulChainID, txTo, reg.AgentID, reg.Wallet, metaURI)
	now := time.Now().UTC()

	payload := &safeTxPayload{
		SafeAddress: safeAddr,
		To:          txTo,
		Value:       txValue,
		Data:        txData,
	}
	payloadJSON, _ := json.Marshal(payload)

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

	if err := s.store.DB.WithContext(ctx).Model(op).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			// Return the existing record.
			existing, getErr := s.getSoulOperation(ctx, opID)
			if getErr == nil && existing != nil {
				op = existing
			}
		} else {
			return nil, nil, "", &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
	}

	identity := &models.SoulAgentIdentity{
		AgentID:      reg.AgentID,
		Domain:       reg.DomainNormalized,
		LocalID:      reg.LocalID,
		Wallet:       reg.Wallet,
		TokenID:      reg.AgentID,
		MetaURI:      metaURI,
		Capabilities: reg.Capabilities,
		Status:       models.SoulAgentStatusPending,
		UpdatedAt:    now,
	}
	_ = identity.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(identity).IfNotExists().Create(); err != nil && !theoryErrors.IsConditionFailed(err) {
		return nil, nil, "", &apptheory.AppError{Code: "app.internal", Message: "failed to create agent identity"}
	}

	// Materialized indexes (best-effort; idempotent).
	wi := &models.SoulWalletAgentIndex{Wallet: reg.Wallet, AgentID: reg.AgentID}
	_ = wi.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(wi).CreateOrUpdate()

	di := &models.SoulDomainAgentIndex{Domain: reg.DomainNormalized, LocalID: reg.LocalID, AgentID: reg.AgentID}
	_ = di.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(di).CreateOrUpdate()

	for _, cap := range normalizeSoulCapabilitiesLoose(reg.Capabilities) {
		ci := &models.SoulCapabilityAgentIndex{Capability: cap, Domain: reg.DomainNormalized, LocalID: reg.LocalID, AgentID: reg.AgentID}
		_ = ci.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(ci).CreateOrUpdate()
	}

	return op, payload, metaURI, nil
}

func (s *Server) handleSoulAgentRegistrationVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	var req soulAgentRegistrationVerifyRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	sig := strings.TrimSpace(req.Signature)
	if sig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}

	reg, appErr := s.loadSoulAgentRegistrationForVerify(ctx, id)
	if appErr != nil {
		return nil, appErr
	}

	// Block re-mint attempts for an already active agent.
	existing, getErr := s.getSoulAgentIdentity(ctx.Context(), reg.AgentID)
	if getErr == nil && existing != nil && strings.TrimSpace(existing.Status) == models.SoulAgentStatusActive {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent is already registered"}
	}
	if getErr != nil && !theoryErrors.IsNotFound(getErr) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
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

	op, safeTx, _, opErr := s.createSoulMintOperation(ctx.Context(), reg)
	if opErr != nil {
		return nil, opErr
	}

	now := time.Now().UTC()
	update, compErr := s.completeSoulAgentRegistration(ctx, reg, verifiedDNS, verifiedHTTPS, now)
	if compErr != nil {
		return nil, compErr
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.registration.verify",
		Target:    fmt.Sprintf("soul_agent_registration:%s", reg.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, soulAgentRegistrationVerifyResponse{
		Registration: *update,
		Operation:    *op,
		SafeTx:       safeTx,
	})
}
