package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/tips"
)

const (
	tipRegistryProofPrefix = "_lesser-host-tip-registry."
	tipRegistryProofValue  = "lesser-host-tip-registry="
	tipRegistryWellKnown   = "/.well-known/lesser-host-tip-registry"
)

type tipHostRegistrationBeginRequest struct {
	Kind       string `json:"kind,omitempty"` // register_host|update_host
	Domain     string `json:"domain"`
	WalletAddr string `json:"wallet_address"`
	HostFeeBps int64  `json:"host_fee_bps"`
}

type tipRegistryProofInstructions struct {
	Method    string `json:"method"`
	DNSName   string `json:"dns_name,omitempty"`
	DNSValue  string `json:"dns_value,omitempty"`
	HTTPSURL  string `json:"https_url,omitempty"`
	HTTPSBody string `json:"https_body,omitempty"`
}

type tipHostRegistrationBeginResponse struct {
	Registration models.TipHostRegistration     `json:"registration"`
	Wallet       walletChallengeResponse        `json:"wallet"`
	Proofs       []tipRegistryProofInstructions `json:"proofs"`
}

type tipHostRegistrationVerifyRequest struct {
	Signature string   `json:"signature"`
	Proofs    []string `json:"proofs,omitempty"` // dns_txt|https_well_known
}

type tipHostRegistrationVerifyResponse struct {
	Registration models.TipHostRegistration  `json:"registration"`
	Operation    models.TipRegistryOperation `json:"operation"`

	SafeTx *safeTxPayload `json:"safe_tx,omitempty"`
}

type safeTxPayload struct {
	SafeAddress string `json:"safe_address"`
	To          string `json:"to"`
	Value       string `json:"value"`
	Data        string `json:"data"`
}

func (s *Server) handleTipHostRegistrationBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if !s.cfg.TipEnabled {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}
	if s.cfg.TipChainID <= 0 || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}

	var req tipHostRegistrationBeginRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = models.TipRegistryOperationKindRegisterHost
	}
	switch kind {
	case models.TipRegistryOperationKindRegisterHost, models.TipRegistryOperationKindUpdateHost:
		// ok
	default:
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid kind"}
	}

	rawDomain := strings.TrimSpace(req.Domain)
	domainNormalized, err := domains.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	walletAddr := strings.TrimSpace(req.WalletAddr)
	if walletAddr == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "wallet_address is required"}
	}
	if !common.IsHexAddress(walletAddr) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid wallet_address"}
	}
	walletAddr = strings.ToLower(walletAddr)

	if req.HostFeeBps < 0 || req.HostFeeBps > 500 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "host_fee_bps must be between 0 and 500"}
	}

	token, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create proof token"}
	}
	proofValue := tipRegistryProofValue + token

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

	msg := buildTipRegistryWalletMessage(domainNormalized, walletAddr, s.cfg.TipChainID, req.HostFeeBps, proofValue, nonce, now, expiresAt)

	hostID := tips.HostIDFromDomain(domainNormalized)

	reg := &models.TipHostRegistration{
		ID:               id,
		Kind:             kind,
		DomainRaw:        rawDomain,
		DomainNormalized: domainNormalized,
		HostIDHex:        strings.ToLower(hostID.Hex()),
		ChainID:          s.cfg.TipChainID,
		WalletType:       "ethereum",
		WalletAddr:       walletAddr,
		HostFeeBps:       req.HostFeeBps,
		TxMode:           s.cfg.TipTxMode,
		SafeAddress:      strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress)),
		WalletNonce:      nonce,
		WalletMessage:    msg,
		DNSToken:         token,
		HTTPToken:        token,
		Status:           models.TipHostRegistrationStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        expiresAt,
	}
	_ = reg.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(reg).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create registration"}
	}

	audit := &models.AuditLogEntry{
		Actor:     fmt.Sprintf("external_wallet:%s", walletAddr),
		Action:    "tip_registry.registration.begin",
		Target:    fmt.Sprintf("tip_host_registration:%s", reg.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	dnsName := tipRegistryProofPrefix + domainNormalized
	httpsURL := "https://" + domainNormalized + tipRegistryWellKnown

	return apptheory.JSON(http.StatusCreated, tipHostRegistrationBeginResponse{
		Registration: *reg,
		Wallet: walletChallengeResponse{
			ID:        reg.ID,
			Username:  "",
			Address:   walletAddr,
			ChainID:   int(s.cfg.TipChainID),
			Nonce:     nonce,
			Message:   msg,
			IssuedAt:  now,
			ExpiresAt: expiresAt,
		},
		Proofs: []tipRegistryProofInstructions{
			{Method: "dns_txt", DNSName: dnsName, DNSValue: proofValue},
			{Method: "https_well_known", HTTPSURL: httpsURL, HTTPSBody: proofValue},
		},
	})
}

func buildTipRegistryWalletMessage(domainNormalized, walletAddr string, chainID int64, hostFeeBps int64, proofValue, nonce string, issuedAt, expiresAt time.Time) string {
	var sb strings.Builder

	sb.WriteString("lesser.host requests you to manage tip host registry settings for:\n")
	sb.WriteString(domainNormalized)
	sb.WriteString("\n\n")
	sb.WriteString("Wallet: ")
	sb.WriteString(strings.ToLower(strings.TrimSpace(walletAddr)))
	sb.WriteString("\nChain ID: ")
	sb.WriteString(fmt.Sprintf("%d", chainID))
	sb.WriteString("\nHost fee (bps): ")
	sb.WriteString(fmt.Sprintf("%d", hostFeeBps))
	sb.WriteString("\nProof value: ")
	sb.WriteString(proofValue)
	sb.WriteString("\nNonce: ")
	sb.WriteString(nonce)
	sb.WriteString("\nIssued At: ")
	sb.WriteString(issuedAt.UTC().Format(time.RFC3339))
	sb.WriteString("\nExpiration Time: ")
	sb.WriteString(expiresAt.UTC().Format(time.RFC3339))

	return sb.String()
}

func (s *Server) handleTipHostRegistrationVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if !s.cfg.TipEnabled {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}
	if s.cfg.TipChainID <= 0 || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	var req tipHostRegistrationVerifyRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}
	sig := strings.TrimSpace(req.Signature)
	if sig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}

	reg, err := s.getTipHostRegistration(ctx, id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "registration not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if !reg.ExpiresAt.IsZero() && time.Now().After(reg.ExpiresAt) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "registration expired"}
	}
	if reg.Status == models.TipHostRegistrationStatusCompleted {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration already completed"}
	}

	if err := verifyEthereumSignature(reg.WalletAddr, reg.WalletMessage, sig); err != nil {
		return nil, &apptheory.AppError{Code: "app.forbidden", Message: "invalid signature"}
	}

	requiredProofs, err := parseTipRegistryProofs(req.Proofs)
	if err != nil {
		return nil, err
	}

	proofValue := tipRegistryProofValue + strings.TrimSpace(reg.DNSToken)
	verifiedDNS := reg.DNSVerified
	verifiedHTTPS := reg.HTTPSVerified

	if requiredProofs.requireDNS {
		if ok := verifyTipRegistryDNS(ctx.Context(), reg.DomainNormalized, proofValue); !ok {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "dns proof not found"}
		}
		verifiedDNS = true
	}
	if requiredProofs.requireHTTPS {
		if ok := verifyTipRegistryHTTPS(ctx.Context(), reg.DomainNormalized, proofValue); !ok {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "https proof not found"}
		}
		verifiedHTTPS = true
	}

	// Higher-assurance updates: require BOTH DNS + HTTPS when wallet/fee increases.
	if strings.ToLower(strings.TrimSpace(reg.Kind)) == models.TipRegistryOperationKindUpdateHost {
		requireBoth, why, err := s.tipRegistryUpdateRequiresBothProofs(ctx.Context(), reg)
		if err != nil {
			return nil, err
		}
		if requireBoth && (!verifiedDNS || !verifiedHTTPS) {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "update requires both dns and https proof: " + why}
		}
	}

	op, safeTx, err := s.createTipRegistryOperationForRegistration(ctx.Context(), reg)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	update := &models.TipHostRegistration{
		ID:               reg.ID,
		Kind:             reg.Kind,
		DomainRaw:        reg.DomainRaw,
		DomainNormalized: reg.DomainNormalized,
		HostIDHex:        reg.HostIDHex,
		ChainID:          reg.ChainID,
		WalletType:       reg.WalletType,
		WalletAddr:       reg.WalletAddr,
		HostFeeBps:       reg.HostFeeBps,
		TxMode:           reg.TxMode,
		SafeAddress:      reg.SafeAddress,
		WalletNonce:      reg.WalletNonce,
		WalletMessage:    reg.WalletMessage,
		DNSToken:         reg.DNSToken,
		HTTPToken:        reg.HTTPToken,
		DNSVerified:      verifiedDNS,
		HTTPSVerified:    verifiedHTTPS,
		WalletVerified:   true,
		VerifiedAt:       now,
		Status:           models.TipHostRegistrationStatusCompleted,
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

	audit := &models.AuditLogEntry{
		Actor:     fmt.Sprintf("external_wallet:%s", strings.TrimSpace(reg.WalletAddr)),
		Action:    "tip_registry.registration.verify",
		Target:    fmt.Sprintf("tip_host_registration:%s", reg.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, tipHostRegistrationVerifyResponse{
		Registration: *update,
		Operation:    *op,
		SafeTx:       safeTx,
	})
}

func (s *Server) getTipHostRegistration(ctx *apptheory.Context, id string) (*models.TipHostRegistration, error) {
	var reg models.TipHostRegistration
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.TipHostRegistration{}).
		Where("PK", "=", fmt.Sprintf("TIP_HOST_REG#%s", strings.TrimSpace(id))).
		Where("SK", "=", "REG").
		First(&reg)
	if err != nil {
		return nil, err
	}
	return &reg, nil
}

type requiredProofSet struct {
	requireDNS   bool
	requireHTTPS bool
}

func parseTipRegistryProofs(proofs []string) (requiredProofSet, error) {
	if len(proofs) == 0 {
		return requiredProofSet{requireDNS: true}, nil
	}
	var out requiredProofSet
	for _, p := range proofs {
		p = strings.ToLower(strings.TrimSpace(p))
		switch p {
		case "dns_txt":
			out.requireDNS = true
		case "https_well_known":
			out.requireHTTPS = true
		case "":
			continue
		default:
			return requiredProofSet{}, &apptheory.AppError{Code: "app.bad_request", Message: "invalid proof: " + p}
		}
	}
	if !out.requireDNS && !out.requireHTTPS {
		return requiredProofSet{}, &apptheory.AppError{Code: "app.bad_request", Message: "at least one proof is required"}
	}
	return out, nil
}

func verifyTipRegistryDNS(ctx context.Context, domainNormalized, proofValue string) bool {
	domainNormalized = strings.TrimSpace(domainNormalized)
	proofValue = strings.TrimSpace(proofValue)
	if domainNormalized == "" || proofValue == "" {
		return false
	}

	txtName := tipRegistryProofPrefix + domainNormalized
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

func verifyTipRegistryHTTPS(ctx context.Context, domainNormalized, proofValue string) bool {
	domainNormalized = strings.TrimSpace(domainNormalized)
	proofValue = strings.TrimSpace(proofValue)
	if domainNormalized == "" || proofValue == "" {
		return false
	}

	lookupCtx := ctx
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	rc, cancel := context.WithTimeout(lookupCtx, 4*time.Second)
	defer cancel()
	if err := validateOutboundHost(rc, domainNormalized); err != nil {
		return false
	}

	u := &url.URL{
		Scheme: "https",
		Host:   domainNormalized,
		Path:   path.Clean(tipRegistryWellKnown),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects not allowed")
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(body)) == proofValue
}

func validateOutboundHost(ctx context.Context, host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return errors.New("host is required")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("host is not allowed")
	}

	if ip := net.ParseIP(host); ip != nil {
		if isDeniedIP(ip) {
			return errors.New("ip is not allowed")
		}
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return errors.New("failed to resolve host")
	}
	for _, ipAddr := range ips {
		if isDeniedIP(ipAddr.IP) {
			return errors.New("host resolves to blocked ip")
		}
	}
	return nil
}

func isDeniedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()

	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}

	for _, pfx := range deniedIPRanges() {
		if pfx.Contains(addr) {
			return true
		}
	}

	// Also block RFC1918 + ULA via stdlib helpers.
	if ip.IsPrivate() {
		return true
	}

	return false
}

func deniedIPRanges() []netip.Prefix {
	// Keep this small and explicit; add ranges as SSRF regressions are found.
	return []netip.Prefix{
		mustPrefix("0.0.0.0/8"),
		mustPrefix("10.0.0.0/8"),
		mustPrefix("100.64.0.0/10"), // CGNAT
		mustPrefix("127.0.0.0/8"),
		mustPrefix("169.254.0.0/16"), // link-local + metadata
		mustPrefix("172.16.0.0/12"),
		mustPrefix("192.0.0.0/24"), // IETF protocol assignments
		mustPrefix("192.0.2.0/24"), // TEST-NET-1
		mustPrefix("192.168.0.0/16"),
		mustPrefix("198.18.0.0/15"),   // benchmark
		mustPrefix("198.51.100.0/24"), // TEST-NET-2
		mustPrefix("203.0.113.0/24"),  // TEST-NET-3
		mustPrefix("224.0.0.0/4"),     // multicast
		mustPrefix("240.0.0.0/4"),     // reserved

		mustPrefix("::/128"),
		mustPrefix("::1/128"),
		mustPrefix("fc00::/7"),      // ULA
		mustPrefix("fe80::/10"),     // link-local
		mustPrefix("ff00::/8"),      // multicast
		mustPrefix("2001:db8::/32"), // documentation
	}
}

func mustPrefix(cidr string) netip.Prefix {
	pfx, err := netip.ParsePrefix(cidr)
	if err != nil {
		panic(err)
	}
	return pfx
}

func (s *Server) tipRegistryUpdateRequiresBothProofs(ctx context.Context, reg *models.TipHostRegistration) (bool, string, error) {
	if reg == nil {
		return false, "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(s.cfg.TipRPCURL) == "" || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return false, "", &apptheory.AppError{Code: "app.conflict", Message: "tip rpc not configured"}
	}
	hostID := common.HexToHash(strings.TrimSpace(reg.HostIDHex))
	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.TipContractAddress))

	client, err := dialEthClient(ctx, s.cfg.TipRPCURL)
	if err != nil {
		return false, "", &apptheory.AppError{Code: "app.internal", Message: "failed to connect to rpc"}
	}
	defer client.Close()

	host, err := tipSplitterGetHost(ctx, client, contractAddr, hostID)
	if err != nil {
		return false, "", &apptheory.AppError{Code: "app.internal", Message: "failed to read host state"}
	}
	if (host.Wallet == common.Address{}) {
		return false, "", &apptheory.AppError{Code: "app.bad_request", Message: "host is not registered"}
	}

	newWallet := common.HexToAddress(strings.TrimSpace(reg.WalletAddr))
	increaseWallet := newWallet != host.Wallet
	increaseFee := reg.HostFeeBps > int64(host.FeeBps)

	if increaseWallet || increaseFee {
		why := "wallet or fee increase"
		if increaseWallet && !increaseFee {
			why = "wallet change"
		} else if !increaseWallet && increaseFee {
			why = "fee increase"
		}
		return true, why, nil
	}

	return false, "", nil
}

func (s *Server) createTipRegistryOperationForRegistration(ctx context.Context, reg *models.TipHostRegistration) (*models.TipRegistryOperation, *safeTxPayload, error) {
	if reg == nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	contractAddrRaw := strings.TrimSpace(s.cfg.TipContractAddress)
	if !common.IsHexAddress(contractAddrRaw) {
		return nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}
	contractAddr := common.HexToAddress(contractAddrRaw)

	walletAddrRaw := strings.TrimSpace(reg.WalletAddr)
	if !common.IsHexAddress(walletAddrRaw) {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid wallet address"}
	}
	walletAddr := common.HexToAddress(walletAddrRaw)

	if reg.HostFeeBps < 0 || reg.HostFeeBps > 500 {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "host_fee_bps must be between 0 and 500"}
	}

	hostID := common.HexToHash(strings.TrimSpace(reg.HostIDHex))
	fee := uint16(reg.HostFeeBps) //nolint:gosec // bounded (0..500) validated above

	kind := strings.ToLower(strings.TrimSpace(reg.Kind))
	var data []byte
	var err error
	switch kind {
	case models.TipRegistryOperationKindRegisterHost:
		data, err = tips.EncodeRegisterHostCall(hostID, walletAddr, fee)
	case models.TipRegistryOperationKindUpdateHost:
		data, err = tips.EncodeUpdateHostCall(hostID, walletAddr, fee)
	default:
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid kind"}
	}
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	txTo := strings.ToLower(contractAddr.Hex())
	txData := "0x" + hex.EncodeToString(data)
	txValue := "0"

	safeAddr := strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress))
	if s.cfg.TipTxMode == tipTxModeSafe && !common.IsHexAddress(safeAddr) {
		return nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry safe is not configured"}
	}

	opID := tipRegistryOpID(kind, s.cfg.TipChainID, txTo, hostID.Hex(), walletAddr.Hex(), reg.HostFeeBps, "", nil, nil)
	now := time.Now().UTC()

	op := &models.TipRegistryOperation{
		ID:               opID,
		Kind:             kind,
		ChainID:          s.cfg.TipChainID,
		ContractAddress:  txTo,
		TxMode:           s.cfg.TipTxMode,
		SafeAddress:      safeAddr,
		DomainRaw:        reg.DomainRaw,
		DomainNormalized: reg.DomainNormalized,
		HostIDHex:        strings.ToLower(hostID.Hex()),
		WalletAddr:       strings.ToLower(walletAddr.Hex()),
		HostFeeBps:       reg.HostFeeBps,
		TxTo:             txTo,
		TxData:           txData,
		TxValue:          txValue,
		Status:           models.TipRegistryOperationStatusProposed,
		CreatedAt:        now,
		UpdatedAt:        now,
		ProposedAt:       now,
	}
	_ = op.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(op).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			// Return the existing record.
			existing, getErr := s.getTipRegistryOperation(ctx, opID)
			if getErr == nil && existing != nil {
				op = existing
			}
		} else {
			return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
	}

	return op, &safeTxPayload{
		SafeAddress: safeAddr,
		To:          txTo,
		Value:       txValue,
		Data:        txData,
	}, nil
}

func tipRegistryOpID(kind string, chainID int64, contractTo, hostIDHex, walletAddr string, feeBps int64, tokenAddr string, active, tokenAllowed *bool) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	contractTo = strings.ToLower(strings.TrimSpace(contractTo))
	hostIDHex = strings.ToLower(strings.TrimSpace(hostIDHex))
	walletAddr = strings.ToLower(strings.TrimSpace(walletAddr))
	tokenAddr = strings.ToLower(strings.TrimSpace(tokenAddr))

	var sb strings.Builder
	sb.WriteString(kind)
	sb.WriteString("|")
	sb.WriteString(fmt.Sprintf("%d", chainID))
	sb.WriteString("|")
	sb.WriteString(contractTo)
	sb.WriteString("|")
	sb.WriteString(hostIDHex)
	sb.WriteString("|")
	sb.WriteString(walletAddr)
	sb.WriteString("|")
	sb.WriteString(fmt.Sprintf("%d", feeBps))
	sb.WriteString("|")
	sb.WriteString(tokenAddr)
	if active != nil {
		sb.WriteString("|active=")
		if *active {
			sb.WriteString("1")
		} else {
			sb.WriteString("0")
		}
	}
	if tokenAllowed != nil {
		sb.WriteString("|allowed=")
		if *tokenAllowed {
			sb.WriteString("1")
		} else {
			sb.WriteString("0")
		}
	}

	sum := sha256.Sum256([]byte(sb.String()))
	return "tipop_" + hex.EncodeToString(sum[:16])
}

func (s *Server) getTipRegistryOperation(ctx context.Context, id string) (*models.TipRegistryOperation, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("id is required")
	}
	var op models.TipRegistryOperation
	err := s.store.DB.WithContext(ctx).
		Model(&models.TipRegistryOperation{}).
		Where("PK", "=", fmt.Sprintf("TIPREG_OP#%s", id)).
		Where("SK", "=", models.SKMetadata).
		First(&op)
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func dialEthClient(ctx context.Context, rpcURL string) (*ethclient.Client, error) {
	rpcURL = strings.TrimSpace(rpcURL)
	if rpcURL == "" {
		return nil, errors.New("rpc url is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rc, err := rpc.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, err
	}
	return ethclient.NewClient(rc), nil
}

func tipSplitterGetHost(ctx context.Context, client *ethclient.Client, contract common.Address, hostID common.Hash) (*tips.HostConfig, error) {
	data, err := tips.EncodeGetHostCall(hostID)
	if err != nil {
		return nil, err
	}
	ret, err := client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return tips.DecodeGetHostResult(ret)
}

// --- Admin endpoints (operations + reconciliation) ---

type listTipRegistryOperationsResponse struct {
	Operations []models.TipRegistryOperation `json:"operations"`
	Count      int                           `json:"count"`
}

func (s *Server) handleListTipRegistryOperations(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	status := ""
	if ctx != nil && len(ctx.Request.Query) > 0 {
		if v := ctx.Request.Query["status"]; len(v) > 0 {
			status = v[0]
		}
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = models.TipRegistryOperationStatusPending
	}

	var items []*models.TipRegistryOperation
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.TipRegistryOperation{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("TIPREG_OP_STATUS#%s", status)).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list operations"}
	}

	out := make([]models.TipRegistryOperation, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}

	return apptheory.JSON(http.StatusOK, listTipRegistryOperationsResponse{Operations: out, Count: len(out)})
}

func (s *Server) handleGetTipRegistryOperation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	op, err := s.getTipRegistryOperation(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "operation not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, op)
}

type recordTipRegistryExecutionRequest struct {
	ExecTxHash string `json:"exec_tx_hash"`
}

func (s *Server) handleRecordTipRegistryOperationExecution(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if strings.TrimSpace(s.cfg.TipRPCURL) == "" || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip rpc not configured"}
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	var req recordTipRegistryExecutionRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	txHash := strings.TrimSpace(req.ExecTxHash)
	if !isHexHash32(txHash) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "exec_tx_hash is required"}
	}

	op, err := s.getTipRegistryOperation(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "operation not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	client, err := dialEthClient(ctx.Context(), s.cfg.TipRPCURL)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to connect to rpc"}
	}
	defer client.Close()

	receipt, err := client.TransactionReceipt(ctx.Context(), common.HexToHash(txHash))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "receipt not found"}
	}

	receiptJSON := tipRegistryReceiptSnapshotJSON(txHash, receipt)
	snapshotJSON := s.tipRegistryOperationSnapshotJSON(ctx.Context(), client, op)

	now := time.Now().UTC()
	success := receipt.Status == 1
	blockNum := tipRegistryBlockNumber(receipt)
	update := &models.TipRegistryOperation{
		ID:               op.ID,
		Kind:             op.Kind,
		ChainID:          op.ChainID,
		ContractAddress:  op.ContractAddress,
		TxMode:           op.TxMode,
		SafeAddress:      op.SafeAddress,
		DomainRaw:        op.DomainRaw,
		DomainNormalized: op.DomainNormalized,
		HostIDHex:        op.HostIDHex,
		WalletAddr:       op.WalletAddr,
		HostFeeBps:       op.HostFeeBps,
		Active:           op.Active,
		TokenAddress:     op.TokenAddress,
		TokenAllowed:     op.TokenAllowed,
		TxTo:             op.TxTo,
		TxData:           op.TxData,
		TxValue:          op.TxValue,
		SafeTxHash:       op.SafeTxHash,
		ExecTxHash:       strings.ToLower(txHash),
		ExecBlockNumber:  blockNum,
		ExecSuccess:      &success,
		ReceiptJSON:      receiptJSON,
		SnapshotJSON:     snapshotJSON,
		Status: func() string {
			if success {
				return models.TipRegistryOperationStatusExecuted
			}
			return models.TipRegistryOperationStatusFailed
		}(),
		CreatedAt:  op.CreatedAt,
		UpdatedAt:  now,
		ProposedAt: op.ProposedAt,
		ExecutedAt: now,
	}
	_ = update.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update(
		"ExecTxHash",
		"ExecBlockNumber",
		"ExecSuccess",
		"ReceiptJSON",
		"SnapshotJSON",
		"Status",
		"UpdatedAt",
		"ExecutedAt",
	); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update operation"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "tip_registry.operation.record_execution",
		Target:    fmt.Sprintf("tip_registry_operation:%s", op.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, update)
}

func tipRegistryReceiptSnapshotJSON(txHash string, receipt *types.Receipt) string {
	if receipt == nil {
		return ""
	}
	snap := map[string]any{
		"tx_hash":          strings.TrimSpace(txHash),
		"block_number":     receipt.BlockNumber.Uint64(),
		"status":           receipt.Status,
		"gas_used":         receipt.GasUsed,
		"contract_address": strings.ToLower(receipt.ContractAddress.Hex()),
		"logs":             len(receipt.Logs),
	}
	if receipt.EffectiveGasPrice != nil {
		snap["effective_gas_price_wei"] = receipt.EffectiveGasPrice.String()
	}
	b, _ := json.Marshal(snap)
	return string(b)
}

func tipRegistryBlockNumber(receipt *types.Receipt) int64 {
	if receipt == nil || receipt.BlockNumber == nil {
		return 0
	}
	if receipt.BlockNumber.Sign() < 0 || receipt.BlockNumber.BitLen() > 63 {
		return 0
	}
	return receipt.BlockNumber.Int64()
}

func (s *Server) tipRegistryOperationSnapshotJSON(ctx context.Context, client *ethclient.Client, op *models.TipRegistryOperation) string {
	if s == nil || client == nil || op == nil {
		return ""
	}
	kind := strings.ToLower(strings.TrimSpace(op.Kind))
	switch kind {
	case models.TipRegistryOperationKindRegisterHost, models.TipRegistryOperationKindUpdateHost, models.TipRegistryOperationKindSetHostActive:
		return s.tipRegistryHostSnapshotJSON(ctx, client, op)
	case models.TipRegistryOperationKindSetToken:
		return s.tipRegistryTokenSnapshotJSON(ctx, client, op)
	default:
		return ""
	}
}

func (s *Server) tipRegistryHostSnapshotJSON(ctx context.Context, client *ethclient.Client, op *models.TipRegistryOperation) string {
	hostID := common.HexToHash(strings.TrimSpace(op.HostIDHex))
	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.TipContractAddress))
	h, err := tipSplitterGetHost(ctx, client, contractAddr, hostID)
	if err != nil || h == nil {
		return ""
	}

	now := time.Now().UTC()
	hostSnap := map[string]any{
		"host_id":     strings.ToLower(hostID.Hex()),
		"wallet":      strings.ToLower(h.Wallet.Hex()),
		"fee_bps":     h.FeeBps,
		"is_active":   h.IsActive,
		"observed_at": now.Format(time.RFC3339Nano),
	}
	b, _ := json.Marshal(hostSnap)

	state := &models.TipHostState{
		ChainID:          s.cfg.TipChainID,
		ContractAddress:  strings.ToLower(strings.TrimSpace(s.cfg.TipContractAddress)),
		DomainNormalized: strings.TrimSpace(op.DomainNormalized),
		HostIDHex:        strings.ToLower(hostID.Hex()),
		WalletAddr:       strings.ToLower(h.Wallet.Hex()),
		HostFeeBps:       int64(h.FeeBps),
		IsActive:         h.IsActive,
		ObservedAt:       now,
		UpdatedAt:        now,
	}
	_ = state.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(state).CreateOrUpdate()

	return string(b)
}

func (s *Server) tipRegistryTokenSnapshotJSON(ctx context.Context, client *ethclient.Client, op *models.TipRegistryOperation) string {
	token := common.HexToAddress(strings.TrimSpace(op.TokenAddress))
	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.TipContractAddress))

	data, err := tips.EncodeIsTokenAllowedCall(token)
	if err != nil {
		return ""
	}
	ret, err := client.CallContract(ctx, ethereum.CallMsg{To: &contractAddr, Data: data}, nil)
	if err != nil {
		return ""
	}
	allowed, err := tips.DecodeIsTokenAllowedResult(ret)
	if err != nil {
		return ""
	}

	now := time.Now().UTC()
	tokenSnap := map[string]any{
		"token":       strings.ToLower(token.Hex()),
		"allowed":     allowed,
		"observed_at": now.Format(time.RFC3339Nano),
	}
	b, _ := json.Marshal(tokenSnap)
	return string(b)
}

func isHexHash32(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	value = strings.TrimPrefix(value, "0x")
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type createTipRegistryOperationResponse struct {
	Operation models.TipRegistryOperation `json:"operation"`
	SafeTx    *safeTxPayload              `json:"safe_tx,omitempty"`
}

type setTipRegistryHostActiveRequest struct {
	Active bool `json:"active"`
}

func (s *Server) handleSetTipRegistryHostActive(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.TipEnabled || s.cfg.TipChainID <= 0 || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}
	if s.cfg.TipTxMode == tipTxModeSafe && !common.IsHexAddress(strings.TrimSpace(s.cfg.TipAdminSafeAddress)) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry safe is not configured"}
	}

	domainNormalized, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var req setTipRegistryHostActiveRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	hostID := tips.HostIDFromDomain(domainNormalized)
	data, err := tips.EncodeSetHostActiveCall(hostID, req.Active)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.TipContractAddress))
	txTo := strings.ToLower(contractAddr.Hex())
	txData := "0x" + hex.EncodeToString(data)

	active := req.Active
	opID := tipRegistryOpID(models.TipRegistryOperationKindSetHostActive, s.cfg.TipChainID, txTo, hostID.Hex(), "", 0, "", &active, nil)
	now := time.Now().UTC()

	op := &models.TipRegistryOperation{
		ID:               opID,
		Kind:             models.TipRegistryOperationKindSetHostActive,
		ChainID:          s.cfg.TipChainID,
		ContractAddress:  txTo,
		TxMode:           s.cfg.TipTxMode,
		SafeAddress:      strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress)),
		DomainNormalized: domainNormalized,
		HostIDHex:        strings.ToLower(hostID.Hex()),
		Active:           &active,
		TxTo:             txTo,
		TxData:           txData,
		TxValue:          "0",
		Status:           models.TipRegistryOperationStatusProposed,
		CreatedAt:        now,
		UpdatedAt:        now,
		ProposedAt:       now,
	}
	_ = op.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(op).IfNotExists().Create(); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
		if existing, getErr := s.getTipRegistryOperation(ctx.Context(), opID); getErr == nil && existing != nil {
			op = existing
		}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "tip_registry.host.set_active",
		Target:    fmt.Sprintf("tip_registry_operation:%s", op.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, createTipRegistryOperationResponse{
		Operation: *op,
		SafeTx: &safeTxPayload{
			SafeAddress: strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress)),
			To:          txTo,
			Value:       "0",
			Data:        txData,
		},
	})
}

type setTipRegistryTokenAllowedRequest struct {
	TokenAddress string `json:"token_address"`
	Allowed      bool   `json:"allowed"`
}

func (s *Server) handleSetTipRegistryTokenAllowed(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.TipEnabled || s.cfg.TipChainID <= 0 || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}
	if s.cfg.TipTxMode == tipTxModeSafe && !common.IsHexAddress(strings.TrimSpace(s.cfg.TipAdminSafeAddress)) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry safe is not configured"}
	}

	var req setTipRegistryTokenAllowedRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	tokenAddr := strings.TrimSpace(req.TokenAddress)
	if !common.IsHexAddress(tokenAddr) || strings.EqualFold(tokenAddr, "0x0000000000000000000000000000000000000000") {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid token_address"}
	}
	token := common.HexToAddress(tokenAddr)

	data, err := tips.EncodeSetTokenAllowedCall(token, req.Allowed)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.TipContractAddress))
	txTo := strings.ToLower(contractAddr.Hex())
	txData := "0x" + hex.EncodeToString(data)

	allowed := req.Allowed
	opID := tipRegistryOpID(models.TipRegistryOperationKindSetToken, s.cfg.TipChainID, txTo, "", "", 0, token.Hex(), nil, &allowed)
	now := time.Now().UTC()

	op := &models.TipRegistryOperation{
		ID:              opID,
		Kind:            models.TipRegistryOperationKindSetToken,
		ChainID:         s.cfg.TipChainID,
		ContractAddress: txTo,
		TxMode:          s.cfg.TipTxMode,
		SafeAddress:     strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress)),
		TokenAddress:    strings.ToLower(token.Hex()),
		TokenAllowed:    &allowed,
		TxTo:            txTo,
		TxData:          txData,
		TxValue:         "0",
		Status:          models.TipRegistryOperationStatusProposed,
		CreatedAt:       now,
		UpdatedAt:       now,
		ProposedAt:      now,
	}
	_ = op.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(op).IfNotExists().Create(); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
		if existing, getErr := s.getTipRegistryOperation(ctx.Context(), opID); getErr == nil && existing != nil {
			op = existing
		}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "tip_registry.token.set_allowed",
		Target:    fmt.Sprintf("tip_registry_operation:%s", op.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, createTipRegistryOperationResponse{
		Operation: *op,
		SafeTx: &safeTxPayload{
			SafeAddress: strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress)),
			To:          txTo,
			Value:       "0",
			Data:        txData,
		},
	})
}

func (s *Server) handleEnsureTipRegistryHost(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.TipEnabled || s.cfg.TipChainID <= 0 || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}
	if s.cfg.TipTxMode == tipTxModeSafe && !common.IsHexAddress(strings.TrimSpace(s.cfg.TipAdminSafeAddress)) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry safe is not configured"}
	}
	if !common.IsHexAddress(strings.TrimSpace(s.cfg.TipDefaultHostWalletAddress)) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip default host wallet is not configured"}
	}
	if s.cfg.TipDefaultHostFeeBps > 500 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "tip default host fee is not configured"}
	}

	domainNormalized, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	op, safeTx, err := s.ensureTipRegistryHostOperation(ctx.Context(), domainNormalized, "", strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID)
	if err != nil {
		return nil, err
	}
	if op == nil {
		return apptheory.JSON(http.StatusOK, map[string]any{
			"noop":              true,
			"domain_normalized": domainNormalized,
		})
	}

	return apptheory.JSON(http.StatusOK, createTipRegistryOperationResponse{Operation: *op, SafeTx: safeTx})
}

func (s *Server) ensureTipRegistryHostOperation(ctx context.Context, domainNormalized, domainRaw, actor, requestID string) (*models.TipRegistryOperation, *safeTxPayload, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.TipEnabled || s.cfg.TipChainID <= 0 || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}

	domainNormalized = strings.TrimSpace(domainNormalized)
	domainRaw = strings.TrimSpace(domainRaw)

	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.TipContractAddress))
	txTo := strings.ToLower(contractAddr.Hex())

	desiredWallet := common.HexToAddress(strings.TrimSpace(s.cfg.TipDefaultHostWalletAddress))
	desiredFee := s.cfg.TipDefaultHostFeeBps

	hostID := tips.HostIDFromDomain(domainNormalized)

	opKind := models.TipRegistryOperationKindRegisterHost
	if strings.TrimSpace(s.cfg.TipRPCURL) != "" {
		if client, err := dialEthClient(ctx, s.cfg.TipRPCURL); err == nil {
			if host, err := tipSplitterGetHost(ctx, client, contractAddr, hostID); err == nil && host != nil && host.Wallet != (common.Address{}) {
				switch {
				case host.Wallet == desiredWallet && host.FeeBps == desiredFee && host.IsActive:
					opKind = ""
				case host.Wallet != desiredWallet || host.FeeBps != desiredFee:
					opKind = models.TipRegistryOperationKindUpdateHost
				default:
					opKind = models.TipRegistryOperationKindSetHostActive
				}
			}
			client.Close()
		}
	}
	if opKind == "" {
		return nil, nil, nil
	}

	var data []byte
	var err error
	var active *bool
	var walletAddr string
	hostFeeBps := int64(s.cfg.TipDefaultHostFeeBps)

	switch opKind {
	case models.TipRegistryOperationKindRegisterHost:
		walletAddr = strings.ToLower(desiredWallet.Hex())
		data, err = tips.EncodeRegisterHostCall(hostID, desiredWallet, desiredFee)
	case models.TipRegistryOperationKindUpdateHost:
		walletAddr = strings.ToLower(desiredWallet.Hex())
		data, err = tips.EncodeUpdateHostCall(hostID, desiredWallet, desiredFee)
	case models.TipRegistryOperationKindSetHostActive:
		v := true
		active = &v
		hostFeeBps = 0
		data, err = tips.EncodeSetHostActiveCall(hostID, true)
	default:
		err = fmt.Errorf("unsupported tip registry op kind")
	}
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	txData := "0x" + hex.EncodeToString(data)
	opID := tipRegistryOpID(opKind, s.cfg.TipChainID, txTo, hostID.Hex(), walletAddr, hostFeeBps, "", active, nil)
	now := time.Now().UTC()

	safeAddr := strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress))
	op := &models.TipRegistryOperation{
		ID:               opID,
		Kind:             opKind,
		ChainID:          s.cfg.TipChainID,
		ContractAddress:  txTo,
		TxMode:           s.cfg.TipTxMode,
		SafeAddress:      safeAddr,
		DomainRaw:        domainRaw,
		DomainNormalized: domainNormalized,
		HostIDHex:        strings.ToLower(hostID.Hex()),
		WalletAddr:       walletAddr,
		HostFeeBps:       hostFeeBps,
		Active:           active,
		TxTo:             txTo,
		TxData:           txData,
		TxValue:          "0",
		Status:           models.TipRegistryOperationStatusProposed,
		CreatedAt:        now,
		UpdatedAt:        now,
		ProposedAt:       now,
	}
	_ = op.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(op).IfNotExists().Create(); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
		if existing, getErr := s.getTipRegistryOperation(ctx, opID); getErr == nil && existing != nil {
			op = existing
		}
	}

	if strings.TrimSpace(actor) != "" {
		audit := &models.AuditLogEntry{
			Actor:     strings.TrimSpace(actor),
			Action:    "tip_registry.host.ensure",
			Target:    fmt.Sprintf("tip_registry_operation:%s", op.ID),
			RequestID: strings.TrimSpace(requestID),
			CreatedAt: now,
		}
		_ = audit.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(audit).Create()
	}

	return op, &safeTxPayload{
		SafeAddress: safeAddr,
		To:          txTo,
		Value:       "0",
		Data:        txData,
	}, nil
}
