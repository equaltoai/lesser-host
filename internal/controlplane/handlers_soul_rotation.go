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
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("username is required")
	}
	return getSoulAgentItemBySK[models.SoulWalletRotationRequest](s, ctx, agentID, fmt.Sprintf("ROTATION#%s", username))
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
	sb.WriteString(strconv.FormatInt(chainID, 10))
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
	sb.WriteString(strconv.FormatInt(deadline, 10))

	sum := sha256.Sum256([]byte(sb.String()))
	return "soulop_" + hex.EncodeToString(sum[:16])
}

func (s *Server) handleSoulAgentRotateWalletBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulWalletRotationPrereqs(ctx); appErr != nil {
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

	newWallet, appErr := s.normalizeSoulRotateWalletBeginNewWallet(ctx, identity)
	if appErr != nil {
		return nil, appErr
	}

	resp, ok, existingErr := s.existingSoulWalletRotationBeginResponse(ctx, agentIDHex, agentInt, newWallet)
	if existingErr != nil {
		return nil, existingErr
	}
	if ok {
		return resp, nil
	}

	resp, appErr = s.createSoulWalletRotationBeginResponse(ctx, agentIDHex, agentInt, identity, newWallet)
	if appErr != nil {
		return nil, appErr
	}
	return resp, nil
}

func (s *Server) requireSoulWalletRotationPrereqs(ctx *apptheory.Context) *apptheory.AppError {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return appErr
	}
	if appErr := s.requireSoulRPCConfigured(); appErr != nil {
		return appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return appErr
	}
	return nil
}

func (s *Server) existingSoulWalletRotationBeginResponse(ctx *apptheory.Context, agentIDHex string, agentInt *big.Int, newWallet string) (*apptheory.Response, bool, *apptheory.AppError) {
	existing, getErr := s.getSoulWalletRotationRequest(ctx.Context(), agentIDHex, strings.TrimSpace(ctx.AuthIdentity))
	if getErr != nil || existing == nil {
		return nil, false, nil
	}

	now := time.Now().UTC()
	if existing.Spent || (!existing.ExpiresAt.IsZero() && now.After(existing.ExpiresAt)) {
		return nil, false, nil
	}
	if !strings.EqualFold(existing.NewWallet, newWallet) {
		return nil, false, nil
	}

	nonceInt, ok := new(big.Int).SetString(strings.TrimSpace(existing.Nonce), 10)
	if !ok || nonceInt == nil {
		return nil, false, nil
	}

	typed, _, typedErr := soulRotationTypedData(s.cfg.SoulChainID, strings.TrimSpace(s.cfg.SoulRegistryContractAddress), agentInt, existing.CurrentWallet, existing.NewWallet, nonceInt, existing.Deadline)
	if typedErr != nil {
		return nil, false, nil
	}

	resp, err := apptheory.JSON(http.StatusOK, soulRotateWalletBeginResponse{Rotation: *existing, Typed: typed})
	if err != nil {
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return resp, true, nil
}

func (s *Server) requireSoulAgentWalletInSync(ctx context.Context, client ethRPCClient, contractAddr common.Address, agentInt *big.Int, expectedWallet string) (common.Address, *apptheory.AppError) {
	onChainWallet, err := s.soulRegistryGetAgentWallet(ctx, client, contractAddr, agentInt)
	if err != nil {
		return common.Address{}, &apptheory.AppError{Code: "app.internal", Message: "failed to read agent wallet"}
	}
	if (onChainWallet == common.Address{}) {
		return common.Address{}, &apptheory.AppError{Code: "app.conflict", Message: "agent is not minted"}
	}
	if !strings.EqualFold(onChainWallet.Hex(), strings.TrimSpace(expectedWallet)) {
		return common.Address{}, &apptheory.AppError{Code: "app.conflict", Message: "agent wallet is out of sync; record operation execution first"}
	}
	return onChainWallet, nil
}

func (s *Server) handleSoulAgentRotateWalletConfirm(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulWalletRotationPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	agentIDHex, agentInt, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	if _, agentErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex); agentErr != nil {
		return nil, agentErr
	}

	currentSigHex, newSigHex, appErr := parseSoulRotateWalletConfirmRequest(ctx)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	rot, appErr := s.loadSoulWalletRotationRequestForConfirm(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	confirmErr := validateSoulWalletRotationConfirmRequest(rot, now)
	if confirmErr != nil {
		return nil, confirmErr
	}

	nonceInt, deadlineInt, currentSigContract, newSigContract, appErr := validateSoulWalletRotationSignatures(rot, currentSigHex, newSigHex)
	if appErr != nil {
		return nil, appErr
	}

	contractAddr, txTo, appErr := s.soulRegistryContractAddress()
	if appErr != nil {
		return nil, appErr
	}

	client, appErr := s.dialSoulRPCClient(ctx.Context())
	if appErr != nil {
		return nil, appErr
	}
	defer client.Close()

	onChainErr := s.verifySoulWalletRotationOnChainState(ctx.Context(), client, contractAddr, agentInt, rot, nonceInt)
	if onChainErr != nil {
		return nil, onChainErr
	}

	resp, appErr := s.createSoulWalletRotationConfirmResponse(ctx, agentIDHex, agentInt, txTo, rot, nonceInt, deadlineInt, currentSigContract, newSigContract, now)
	if appErr != nil {
		return nil, appErr
	}
	return resp, nil
}

func (s *Server) normalizeSoulRotateWalletBeginNewWallet(ctx *apptheory.Context, identity *models.SoulAgentIdentity) (string, *apptheory.AppError) {
	if s == nil || ctx == nil || identity == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req soulRotateWalletBeginRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		if appErr, ok := parseErr.(*apptheory.AppError); ok {
			return "", appErr
		}
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid request"}
	}

	newWallet, appErr := s.normalizeSoulWalletAddress(ctx.Context(), req.NewWalletAddress)
	if appErr != nil {
		return "", appErr
	}
	if strings.EqualFold(newWallet, strings.TrimSpace(identity.Wallet)) {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "new_wallet_address must differ from current wallet"}
	}
	return newWallet, nil
}

func (s *Server) createSoulWalletRotationBeginResponse(ctx *apptheory.Context, agentIDHex string, agentInt *big.Int, identity *models.SoulAgentIdentity, newWallet string) (*apptheory.Response, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || identity == nil || agentInt == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	contractAddr, verifyingContract, appErr := s.soulRegistryContractAddress()
	if appErr != nil {
		return nil, appErr
	}

	client, appErr := s.dialSoulRPCClient(ctx.Context())
	if appErr != nil {
		return nil, appErr
	}
	defer client.Close()

	onChainWallet, appErr := s.requireSoulAgentWalletInSync(ctx.Context(), client, contractAddr, agentInt, strings.TrimSpace(identity.Wallet))
	if appErr != nil {
		return nil, appErr
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

	if createErr := s.store.DB.WithContext(ctx.Context()).Model(r).CreateOrUpdate(); createErr != nil {
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

	resp, err := apptheory.JSON(http.StatusCreated, soulRotateWalletBeginResponse{Rotation: *r, Typed: typed})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return resp, nil
}

func (s *Server) createSoulWalletRotationConfirmResponse(
	ctx *apptheory.Context,
	agentIDHex string,
	agentInt *big.Int,
	txTo string,
	rot *models.SoulWalletRotationRequest,
	nonceInt *big.Int,
	deadlineInt *big.Int,
	currentSigContract []byte,
	newSigContract []byte,
	now time.Time,
) (*apptheory.Response, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || agentInt == nil || rot == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	newWalletAddr := common.HexToAddress(strings.TrimSpace(rot.NewWallet))
	data, err := soul.EncodeRotateWalletCall(agentInt, newWalletAddr, nonceInt, deadlineInt, currentSigContract, newSigContract)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	safeAddr, appErr := s.soulRegistrySafeAddress()
	if appErr != nil {
		return nil, appErr
	}

	opID := soulRotationOpID(s.cfg.SoulChainID, txTo, agentIDHex, rot.CurrentWallet, rot.NewWallet, rot.Nonce, rot.Deadline)
	payload := &safeTxPayload{
		SafeAddress: safeAddr,
		To:          txTo,
		Value:       "0",
		Data:        "0x" + hex.EncodeToString(data),
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

	if createErr := s.store.DB.WithContext(ctx.Context()).Model(op).IfNotExists().Create(); createErr != nil {
		if theoryErrors.IsConditionFailed(createErr) {
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

	resp, err := apptheory.JSON(http.StatusOK, soulRotateWalletConfirmResponse{Operation: *op, SafeTx: payload})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return resp, nil
}

func parseSoulRotateWalletConfirmRequest(ctx *apptheory.Context) (currentSigHex string, newSigHex string, appErr *apptheory.AppError) {
	if ctx == nil {
		return "", "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req soulRotateWalletConfirmRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		appErr, ok := parseErr.(*apptheory.AppError)
		if ok {
			return "", "", appErr
		}
		return "", "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid request"}
	}
	currentSigHex = strings.TrimSpace(req.CurrentSignature)
	newSigHex = strings.TrimSpace(req.NewSignature)
	if currentSigHex == "" || newSigHex == "" {
		return "", "", &apptheory.AppError{Code: "app.bad_request", Message: "signatures are required"}
	}
	return currentSigHex, newSigHex, nil
}

func (s *Server) loadSoulWalletRotationRequestForConfirm(ctx *apptheory.Context, agentIDHex string) (*models.SoulWalletRotationRequest, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	rot, err := s.getSoulWalletRotationRequest(ctx.Context(), agentIDHex, strings.TrimSpace(ctx.AuthIdentity))
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "no pending rotation"}
	}
	if err != nil || rot == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return rot, nil
}

func validateSoulWalletRotationConfirmRequest(rot *models.SoulWalletRotationRequest, now time.Time) *apptheory.AppError {
	if rot == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if rot.Spent {
		return &apptheory.AppError{Code: "app.conflict", Message: "rotation already confirmed"}
	}
	if !rot.ExpiresAt.IsZero() && now.After(rot.ExpiresAt) {
		return &apptheory.AppError{Code: "app.bad_request", Message: "rotation request expired"}
	}
	return nil
}

func validateSoulWalletRotationSignatures(
	rot *models.SoulWalletRotationRequest,
	currentSigHex string,
	newSigHex string,
) (nonceInt *big.Int, deadlineInt *big.Int, currentSigContract []byte, newSigContract []byte, appErr *apptheory.AppError) {
	if rot == nil {
		return nil, nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	digestBytes, err := hexutil.Decode(strings.TrimSpace(rot.DigestHex))
	if err != nil || len(digestBytes) != 32 {
		return nil, nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "rotation request is invalid"}
	}

	currentSigRecovery, currentSigContract, appErr := decodeEthSignature(currentSigHex)
	if appErr != nil {
		return nil, nil, nil, nil, appErr
	}
	newSigRecovery, newSigContract, appErr := decodeEthSignature(newSigHex)
	if appErr != nil {
		return nil, nil, nil, nil, appErr
	}

	recoveredCurrent, err := recoverAddressFromDigest(digestBytes, currentSigRecovery)
	if err != nil {
		return nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid current_signature"}
	}
	recoveredNew, err := recoverAddressFromDigest(digestBytes, newSigRecovery)
	if err != nil {
		return nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid new_signature"}
	}

	if !strings.EqualFold(recoveredCurrent.Hex(), strings.TrimSpace(rot.CurrentWallet)) {
		return nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "current_signature does not match current wallet"}
	}
	if !strings.EqualFold(recoveredNew.Hex(), strings.TrimSpace(rot.NewWallet)) {
		return nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "new_signature does not match new wallet"}
	}

	nonceInt, ok := new(big.Int).SetString(strings.TrimSpace(rot.Nonce), 10)
	if !ok || nonceInt == nil {
		return nil, nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "rotation request is invalid"}
	}
	deadlineInt = big.NewInt(rot.Deadline)

	return nonceInt, deadlineInt, currentSigContract, newSigContract, nil
}

func (s *Server) verifySoulWalletRotationOnChainState(
	ctx context.Context,
	client ethRPCClient,
	contractAddr common.Address,
	agentInt *big.Int,
	rot *models.SoulWalletRotationRequest,
	nonceInt *big.Int,
) *apptheory.AppError {
	if s == nil || client == nil || agentInt == nil || rot == nil || nonceInt == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	onChainWallet, err := s.soulRegistryGetAgentWallet(ctx, client, contractAddr, agentInt)
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to read agent wallet"}
	}
	if !strings.EqualFold(onChainWallet.Hex(), strings.TrimSpace(rot.CurrentWallet)) {
		return &apptheory.AppError{Code: "app.conflict", Message: "agent wallet changed; begin rotation again"}
	}

	onChainNonce, err := s.soulRegistryGetAgentNonce(ctx, client, contractAddr, agentInt)
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to read agent nonce"}
	}
	if onChainNonce == nil {
		onChainNonce = new(big.Int)
	}
	if onChainNonce.Cmp(nonceInt) != 0 {
		return &apptheory.AppError{Code: "app.conflict", Message: "agent nonce changed; begin rotation again"}
	}

	return nil
}
