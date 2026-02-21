package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulRotateWalletBeginRequest struct {
	NewWalletAddress string `json:"new_wallet_address"`
}

type soulWalletRotationTypedDataType struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type soulWalletRotationTypedDataDomain struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	ChainID           int64  `json:"chainId"`
	VerifyingContract string `json:"verifyingContract"`
}

type soulWalletRotationTypedDataMessage struct {
	AgentID       string `json:"agentId"`
	CurrentWallet string `json:"currentWallet"`
	NewWallet     string `json:"newWallet"`
	Nonce         string `json:"nonce"`
	Deadline      string `json:"deadline"`
}

type soulWalletRotationTypedData struct {
	Types       map[string][]soulWalletRotationTypedDataType `json:"types"`
	PrimaryType string                                       `json:"primaryType"`
	Domain      soulWalletRotationTypedDataDomain            `json:"domain"`
	Message     soulWalletRotationTypedDataMessage           `json:"message"`
	DigestHex   string                                       `json:"digest_hex"`
}

type soulRotateWalletBeginResponse struct {
	Rotation models.SoulWalletRotationRequest `json:"rotation"`
	Typed    soulWalletRotationTypedData      `json:"typed_data"`
}

type soulRotateWalletConfirmRequest struct {
	CurrentSignature string `json:"current_signature"`
	NewSignature     string `json:"new_signature"`
}

type soulRotateWalletConfirmResponse struct {
	Operation models.SoulOperation `json:"operation"`
	SafeTx    *safeTxPayload       `json:"safe_tx,omitempty"`
}

func parseSoulAgentIDHex(agentID string) (string, *big.Int, *apptheory.AppError) {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return "", nil, &apptheory.AppError{Code: "app.bad_request", Message: "agent_id is required"}
	}
	if !strings.HasPrefix(agentID, "0x") {
		return "", nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid agent_id"}
	}
	if len(agentID) != 66 {
		return "", nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid agent_id"}
	}
	raw := strings.TrimPrefix(agentID, "0x")
	if _, err := hex.DecodeString(raw); err != nil {
		return "", nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid agent_id"}
	}
	agentInt, ok := new(big.Int).SetString(raw, 16)
	if !ok {
		return "", nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid agent_id"}
	}
	return agentID, agentInt, nil
}

func (s *Server) getSoulWalletRotationRequest(ctx context.Context, agentID string, username string) (*models.SoulWalletRotationRequest, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	username = strings.TrimSpace(username)
	if agentID == "" || username == "" {
		return nil, errors.New("agent id and username are required")
	}

	var item models.SoulWalletRotationRequest
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulWalletRotationRequest{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", fmt.Sprintf("ROTATION#%s", username)).
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Server) soulRegistryGetAgentWallet(ctx context.Context, client ethRPCClient, contract common.Address, agentID *big.Int) (common.Address, error) {
	data, err := soul.EncodeGetAgentWalletCall(agentID)
	if err != nil {
		return common.Address{}, err
	}
	ret, err := client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: data}, nil)
	if err != nil {
		return common.Address{}, err
	}
	return soul.DecodeGetAgentWalletResult(ret)
}

func (s *Server) soulRegistryGetAgentNonce(ctx context.Context, client ethRPCClient, contract common.Address, agentID *big.Int) (*big.Int, error) {
	data, err := soul.EncodeAgentNoncesCall(agentID)
	if err != nil {
		return nil, err
	}
	ret, err := client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return soul.DecodeAgentNoncesResult(ret)
}

func soulRotationTypedData(chainID int64, verifyingContract string, agentID *big.Int, currentWallet string, newWallet string, nonce *big.Int, deadline int64) (soulWalletRotationTypedData, []byte, *apptheory.AppError) {
	if agentID == nil || nonce == nil {
		return soulWalletRotationTypedData{}, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	types := map[string][]soulWalletRotationTypedDataType{
		"EIP712Domain": {
			{Name: "name", Type: "string"},
			{Name: "version", Type: "string"},
			{Name: "chainId", Type: "uint256"},
			{Name: "verifyingContract", Type: "address"},
		},
		"WalletRotationProposal": {
			{Name: "agentId", Type: "uint256"},
			{Name: "currentWallet", Type: "address"},
			{Name: "newWallet", Type: "address"},
			{Name: "nonce", Type: "uint256"},
			{Name: "deadline", Type: "uint256"},
		},
	}

	msg := soulWalletRotationTypedDataMessage{
		AgentID:       agentID.String(),
		CurrentWallet: strings.ToLower(strings.TrimSpace(currentWallet)),
		NewWallet:     strings.ToLower(strings.TrimSpace(newWallet)),
		Nonce:         nonce.String(),
		Deadline:      strconv.FormatInt(deadline, 10),
	}

	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"WalletRotationProposal": {
				{Name: "agentId", Type: "uint256"},
				{Name: "currentWallet", Type: "address"},
				{Name: "newWallet", Type: "address"},
				{Name: "nonce", Type: "uint256"},
				{Name: "deadline", Type: "uint256"},
			},
		},
		PrimaryType: "WalletRotationProposal",
		Domain: apitypes.TypedDataDomain{
			Name:              "LesserSoul",
			Version:           "1",
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: strings.ToLower(strings.TrimSpace(verifyingContract)),
		},
		Message: apitypes.TypedDataMessage{
			"agentId":       msg.AgentID,
			"currentWallet": msg.CurrentWallet,
			"newWallet":     msg.NewWallet,
			"nonce":         msg.Nonce,
			"deadline":      msg.Deadline,
		},
	}

	digest, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return soulWalletRotationTypedData{}, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to build rotation digest"}
	}

	out := soulWalletRotationTypedData{
		Types:       types,
		PrimaryType: "WalletRotationProposal",
		Domain: soulWalletRotationTypedDataDomain{
			Name:              "LesserSoul",
			Version:           "1",
			ChainID:           chainID,
			VerifyingContract: strings.ToLower(strings.TrimSpace(verifyingContract)),
		},
		Message:   msg,
		DigestHex: strings.ToLower(hexutil.Encode(digest)),
	}

	return out, digest, nil
}

func decodeEthSignature(signatureHex string) ([]byte, []byte, *apptheory.AppError) {
	raw, err := hexutil.Decode(signatureHex)
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid signature"}
	}
	if len(raw) != 65 {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid signature"}
	}

	recovery := append([]byte(nil), raw...)
	if recovery[64] == 27 || recovery[64] == 28 {
		recovery[64] -= 27
	}

	contract := append([]byte(nil), raw...)
	if contract[64] == 0 || contract[64] == 1 {
		contract[64] += 27
	}

	return recovery, contract, nil
}

func recoverAddressFromDigest(digest []byte, sigRecovery []byte) (common.Address, error) {
	if len(digest) != 32 {
		return common.Address{}, errors.New("digest must be 32 bytes")
	}
	if len(sigRecovery) != 65 {
		return common.Address{}, errors.New("signature must be 65 bytes")
	}

	pubKey, err := crypto.SigToPub(digest, sigRecovery)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pubKey), nil
}

func soulRotationOpID(chainID int64, txTo string, agentID string, currentWallet string, newWallet string, nonce string, deadline int64) string {
	kind := models.SoulOperationKindRotateWallet
	txTo = strings.ToLower(strings.TrimSpace(txTo))
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	currentWallet = strings.ToLower(strings.TrimSpace(currentWallet))
	newWallet = strings.ToLower(strings.TrimSpace(newWallet))
	nonce = strings.TrimSpace(nonce)

	var sb strings.Builder
	sb.WriteString(kind)
	sb.WriteString("|")
	sb.WriteString(fmt.Sprintf("%d", chainID))
	sb.WriteString("|")
	sb.WriteString(txTo)
	sb.WriteString("|")
	sb.WriteString(agentID)
	sb.WriteString("|current=")
	sb.WriteString(currentWallet)
	sb.WriteString("|new=")
	sb.WriteString(newWallet)
	sb.WriteString("|nonce=")
	sb.WriteString(nonce)
	sb.WriteString("|deadline=")
	sb.WriteString(fmt.Sprintf("%d", deadline))

	sum := sha256.Sum256([]byte(sb.String()))
	return "soulop_" + hex.EncodeToString(sum[:16])
}

func (s *Server) handleSoulAgentRotateWalletBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var req soulRotateWalletBeginRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	newWallet, appErr := s.normalizeSoulWalletAddress(ctx.Context(), req.NewWalletAddress)
	if appErr != nil {
		return nil, appErr
	}
	if strings.EqualFold(newWallet, strings.TrimSpace(identity.Wallet)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "new_wallet_address must differ from current wallet"}
	}

	existing, getErr := s.getSoulWalletRotationRequest(ctx.Context(), agentIDHex, strings.TrimSpace(ctx.AuthIdentity))
	if getErr == nil && existing != nil {
		now := time.Now().UTC()
		if !existing.Spent && (existing.ExpiresAt.IsZero() || now.Before(existing.ExpiresAt)) && strings.EqualFold(existing.NewWallet, newWallet) {
			nonceInt, ok := new(big.Int).SetString(strings.TrimSpace(existing.Nonce), 10)
			if ok && nonceInt != nil {
				typed, _, appErr := soulRotationTypedData(s.cfg.SoulChainID, strings.TrimSpace(s.cfg.SoulRegistryContractAddress), agentInt, existing.CurrentWallet, existing.NewWallet, nonceInt, existing.Deadline)
				if appErr == nil {
					return apptheory.JSON(http.StatusOK, soulRotateWalletBeginResponse{Rotation: *existing, Typed: typed})
				}
			}
		}
	}

	contractAddr, verifyingContract, appErr := s.soulRegistryContractAddress()
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
	if !strings.EqualFold(onChainWallet.Hex(), strings.TrimSpace(identity.Wallet)) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent wallet is out of sync; record operation execution first"}
	}

	nonce, err := s.soulRegistryGetAgentNonce(ctx.Context(), client, contractAddr, agentInt)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to read agent nonce"}
	}
	if nonce == nil {
		nonce = new(big.Int)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(30 * time.Minute)
	deadline := expiresAt.Unix()

	typed, _, appErr := soulRotationTypedData(s.cfg.SoulChainID, verifyingContract, agentInt, onChainWallet.Hex(), newWallet, nonce, deadline)
	if appErr != nil {
		return nil, appErr
	}

	r := &models.SoulWalletRotationRequest{
		AgentID:       agentIDHex,
		Username:      strings.TrimSpace(ctx.AuthIdentity),
		CurrentWallet: strings.ToLower(onChainWallet.Hex()),
		NewWallet:     newWallet,
		Nonce:         nonce.String(),
		Deadline:      deadline,
		DigestHex:     typed.DigestHex,
		Spent:         false,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     expiresAt,
	}
	_ = r.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(r).CreateOrUpdate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create rotation request"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.rotation.begin",
		Target:    fmt.Sprintf("soul_wallet_rotation:%s", agentIDHex),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusCreated, soulRotateWalletBeginResponse{Rotation: *r, Typed: typed})
}

func (s *Server) handleSoulAgentRotateWalletConfirm(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var req soulRotateWalletConfirmRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	currentSigHex := strings.TrimSpace(req.CurrentSignature)
	newSigHex := strings.TrimSpace(req.NewSignature)
	if currentSigHex == "" || newSigHex == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signatures are required"}
	}

	rot, err := s.getSoulWalletRotationRequest(ctx.Context(), agentIDHex, strings.TrimSpace(ctx.AuthIdentity))
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "no pending rotation"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	now := time.Now().UTC()
	if rot.Spent {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "rotation already confirmed"}
	}
	if !rot.ExpiresAt.IsZero() && now.After(rot.ExpiresAt) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "rotation request expired"}
	}

	digestBytes, err := hexutil.Decode(strings.TrimSpace(rot.DigestHex))
	if err != nil || len(digestBytes) != 32 {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "rotation request is invalid"}
	}

	currentSigRecovery, currentSigContract, appErr := decodeEthSignature(currentSigHex)
	if appErr != nil {
		return nil, appErr
	}
	newSigRecovery, newSigContract, appErr := decodeEthSignature(newSigHex)
	if appErr != nil {
		return nil, appErr
	}

	recoveredCurrent, err := recoverAddressFromDigest(digestBytes, currentSigRecovery)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid current_signature"}
	}
	recoveredNew, err := recoverAddressFromDigest(digestBytes, newSigRecovery)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid new_signature"}
	}

	if !strings.EqualFold(recoveredCurrent.Hex(), strings.TrimSpace(rot.CurrentWallet)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "current_signature does not match current wallet"}
	}
	if !strings.EqualFold(recoveredNew.Hex(), strings.TrimSpace(rot.NewWallet)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "new_signature does not match new wallet"}
	}

	nonceInt, ok := new(big.Int).SetString(strings.TrimSpace(rot.Nonce), 10)
	if !ok {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "rotation request is invalid"}
	}
	deadlineInt := big.NewInt(rot.Deadline)

	contractAddr, txTo, appErr := s.soulRegistryContractAddress()
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
	if !strings.EqualFold(onChainWallet.Hex(), strings.TrimSpace(rot.CurrentWallet)) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent wallet changed; begin rotation again"}
	}

	onChainNonce, err := s.soulRegistryGetAgentNonce(ctx.Context(), client, contractAddr, agentInt)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to read agent nonce"}
	}
	if onChainNonce == nil {
		onChainNonce = new(big.Int)
	}
	if onChainNonce.Cmp(nonceInt) != 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent nonce changed; begin rotation again"}
	}

	newWalletAddr := common.HexToAddress(strings.TrimSpace(rot.NewWallet))
	data, err := soul.EncodeRotateWalletCall(agentInt, newWalletAddr, nonceInt, deadlineInt, currentSigContract, newSigContract)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	txData := "0x" + hex.EncodeToString(data)
	txValue := "0"
	safeAddr, appErr := s.soulRegistrySafeAddress()
	if appErr != nil {
		return nil, appErr
	}

	opID := soulRotationOpID(s.cfg.SoulChainID, txTo, agentIDHex, rot.CurrentWallet, rot.NewWallet, rot.Nonce, rot.Deadline)
	payload := &safeTxPayload{
		SafeAddress: safeAddr,
		To:          txTo,
		Value:       txValue,
		Data:        txData,
	}
	payloadJSON, _ := json.Marshal(payload)

	op := &models.SoulOperation{
		OperationID:     opID,
		Kind:            models.SoulOperationKindRotateWallet,
		AgentID:         agentIDHex,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: strings.TrimSpace(string(payloadJSON)),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_ = op.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(op).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			existing, getErr := s.getSoulOperation(ctx.Context(), opID)
			if getErr == nil && existing != nil {
				op = existing
			}
		} else {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
	}

	rot.Spent = true
	rot.ConfirmedAt = now
	rot.UpdatedAt = now
	_ = rot.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(rot).IfExists().Update("Spent", "ConfirmedAt", "UpdatedAt")

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.rotation.confirm",
		Target:    fmt.Sprintf("soul_operation:%s", opID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, soulRotateWalletConfirmResponse{Operation: *op, SafeTx: payload})
}
