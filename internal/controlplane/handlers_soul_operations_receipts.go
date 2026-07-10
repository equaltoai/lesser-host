package controlplane

import (
	"bytes"
	"context"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/soulattestations"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const safeExecTransactionABI = `[
  {"type":"function","name":"execTransaction","stateMutability":"payable","inputs":[{"name":"to","type":"address"},{"name":"value","type":"uint256"},{"name":"data","type":"bytes"},{"name":"operation","type":"uint8"},{"name":"safeTxGas","type":"uint256"},{"name":"baseGas","type":"uint256"},{"name":"gasPrice","type":"uint256"},{"name":"gasToken","type":"address"},{"name":"refundReceiver","type":"address"},{"name":"signatures","type":"bytes"}],"outputs":[{"name":"success","type":"bool"}]}
]`

var (
	soulRegistryReceiptABI    = mustControlPlaneABI(soul.SoulRegistryABI)
	rootAttestationReceiptABI = mustControlPlaneABI(soulattestations.RootAttestationABI)
	safeExecReceiptABI        = mustControlPlaneABI(safeExecTransactionABI)

	soulMintedTopic           = crypto.Keccak256Hash([]byte("SoulMinted(uint256,address,string)"))
	principalDeclaredTopic    = crypto.Keccak256Hash([]byte("PrincipalDeclared(uint256,address)"))
	walletRotatedTopic        = crypto.Keccak256Hash([]byte("WalletRotated(uint256,address,address,uint256)"))
	soulBurnedTopic           = crypto.Keccak256Hash([]byte("SoulBurned(uint256,address)"))
	rootPublishedTopic        = crypto.Keccak256Hash([]byte("RootPublished(bytes32,bytes32,uint256,uint256,uint256)"))
	safeExecutionSuccessTopic = crypto.Keccak256Hash([]byte("ExecutionSuccess(bytes32,uint256)"))
	safeExecutionFailureTopic = crypto.Keccak256Hash([]byte("ExecutionFailure(bytes32,uint256)"))
)

type soulOperationReceiptExpectation struct {
	Payload *safeTxPayload
	To      common.Address
	Value   *big.Int
	Data    []byte
}

type soulOperationExecutionValidation struct {
	Success bool
}

func mustControlPlaneABI(raw string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return parsed
}

func (s *Server) validateSoulOperationExecutionReceipt(ctx context.Context, client ethRPCClient, op *models.SoulOperation, txHash string, receipt *types.Receipt) (soulOperationExecutionValidation, *apptheory.AppTheoryError) {
	if s == nil || client == nil || op == nil || receipt == nil {
		return soulOperationExecutionValidation{}, newAppTheoryError("app.internal", "internal error")
	}
	expect, appErr := parseSoulOperationReceiptExpectation(op)
	if appErr != nil {
		return soulOperationExecutionValidation{}, appErr
	}
	tx, pending, err := client.TransactionByHash(ctx, common.HexToHash(txHash))
	if err != nil || tx == nil || pending {
		return soulOperationExecutionValidation{}, soulOperationReceiptMismatch()
	}
	appErr = validateSoulExecutionTransactionMatchesPayload(tx, expect)
	if appErr != nil {
		return soulOperationExecutionValidation{}, appErr
	}

	success, appErr := soulExecutionReceiptSuccess(receipt, expect)
	if appErr != nil {
		return soulOperationExecutionValidation{}, appErr
	}
	if !success {
		return soulOperationExecutionValidation{Success: false}, nil
	}
	appErr = s.validateSoulOperationSuccessEffect(ctx, client, op, expect, receipt)
	if appErr != nil {
		return soulOperationExecutionValidation{}, appErr
	}
	return soulOperationExecutionValidation{Success: true}, nil
}

func soulExecutionReceiptSuccess(receipt *types.Receipt, expect soulOperationReceiptExpectation) (bool, *apptheory.AppTheoryError) {
	if receipt == nil || expect.Payload == nil {
		return false, soulOperationReceiptMismatch()
	}
	if receipt.Status != 1 {
		return false, nil
	}
	safeAddress := strings.TrimSpace(expect.Payload.SafeAddress)
	if safeAddress == "" {
		return true, nil
	}
	if !common.IsHexAddress(safeAddress) {
		return false, soulOperationReceiptMismatch()
	}
	return safeExecutionReceiptSuccess(receipt, common.HexToAddress(safeAddress))
}

func safeExecutionReceiptSuccess(receipt *types.Receipt, safeAddress common.Address) (bool, *apptheory.AppTheoryError) {
	hasSuccess := false
	hasFailure := false
	for _, lg := range receiptLogs(receipt) {
		if lg == nil || !addressesEqual(lg.Address, safeAddress) || len(lg.Topics) == 0 {
			continue
		}
		switch lg.Topics[0] {
		case safeExecutionSuccessTopic:
			hasSuccess = true
		case safeExecutionFailureTopic:
			hasFailure = true
		}
	}
	if hasSuccess == hasFailure {
		return false, soulOperationReceiptMismatch()
	}
	return hasSuccess, nil
}

func parseSoulOperationReceiptExpectation(op *models.SoulOperation) (soulOperationReceiptExpectation, *apptheory.AppTheoryError) {
	payload := parseSafeTxPayload(op.SafePayloadJSON)
	if payload == nil {
		return soulOperationReceiptExpectation{}, soulOperationReceiptMismatch()
	}
	toRaw := strings.TrimSpace(payload.To)
	if !common.IsHexAddress(toRaw) {
		return soulOperationReceiptExpectation{}, soulOperationReceiptMismatch()
	}
	value, ok := parseSoulOperationPayloadValue(payload.Value)
	if !ok {
		return soulOperationReceiptExpectation{}, soulOperationReceiptMismatch()
	}
	data, err := hexutil.Decode(strings.TrimSpace(payload.Data))
	if err != nil {
		return soulOperationReceiptExpectation{}, soulOperationReceiptMismatch()
	}
	return soulOperationReceiptExpectation{Payload: payload, To: common.HexToAddress(toRaw), Value: value, Data: data}, nil
}

func parseSoulOperationPayloadValue(raw string) (*big.Int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return new(big.Int), true
	}
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || value.Sign() < 0 {
		return nil, false
	}
	return value, true
}

func validateSoulExecutionTransactionMatchesPayload(tx *types.Transaction, expect soulOperationReceiptExpectation) *apptheory.AppTheoryError {
	if tx == nil || expect.Value == nil || expect.Payload == nil || tx.To() == nil {
		return soulOperationReceiptMismatch()
	}
	safeAddress := strings.TrimSpace(expect.Payload.SafeAddress)
	if safeAddress == "" {
		return validateDirectSoulExecutionTransaction(tx, expect)
	}
	return validateSafeSoulExecutionTransaction(tx, safeAddress, expect)
}

func validateDirectSoulExecutionTransaction(tx *types.Transaction, expect soulOperationReceiptExpectation) *apptheory.AppTheoryError {
	if !addressesEqual(*tx.To(), expect.To) || !bigIntsEqual(tx.Value(), expect.Value) || !bytes.Equal(tx.Data(), expect.Data) {
		return soulOperationReceiptMismatch()
	}
	return nil
}

func validateSafeSoulExecutionTransaction(tx *types.Transaction, safeAddress string, expect soulOperationReceiptExpectation) *apptheory.AppTheoryError {
	if !common.IsHexAddress(safeAddress) || !addressesEqual(*tx.To(), common.HexToAddress(safeAddress)) {
		return soulOperationReceiptMismatch()
	}
	innerTo, innerValue, innerData, appErr := decodeSafeExecTransaction(tx.Data())
	if appErr != nil {
		return appErr
	}
	if !addressesEqual(innerTo, expect.To) || !bigIntsEqual(innerValue, expect.Value) || !bytes.Equal(innerData, expect.Data) {
		return soulOperationReceiptMismatch()
	}
	return nil
}

func decodeSafeExecTransaction(data []byte) (common.Address, *big.Int, []byte, *apptheory.AppTheoryError) {
	method, args, ok := unpackCallData(safeExecReceiptABI, data)
	if !ok || method.Name != "execTransaction" || len(args) < 4 {
		return common.Address{}, nil, nil, soulOperationReceiptMismatch()
	}
	to, ok := args[0].(common.Address)
	if !ok {
		return common.Address{}, nil, nil, soulOperationReceiptMismatch()
	}
	value, ok := args[1].(*big.Int)
	if !ok || value == nil {
		return common.Address{}, nil, nil, soulOperationReceiptMismatch()
	}
	innerData, ok := args[2].([]byte)
	if !ok || !abiOperationIsCall(args[3]) {
		return common.Address{}, nil, nil, soulOperationReceiptMismatch()
	}
	return to, value, innerData, nil
}

func (s *Server) validateSoulOperationSuccessEffect(ctx context.Context, client ethRPCClient, op *models.SoulOperation, expect soulOperationReceiptExpectation, receipt *types.Receipt) *apptheory.AppTheoryError {
	switch strings.ToLower(strings.TrimSpace(op.Kind)) {
	case models.SoulOperationKindMint:
		return s.validateSoulMintExecutionEffect(ctx, client, op, expect, receipt)
	case models.SoulOperationKindRotateWallet:
		return s.validateSoulRotateWalletExecutionEffect(ctx, client, op, expect, receipt)
	case models.SoulOperationKindBurn:
		return s.validateSoulBurnExecutionEffect(ctx, client, op, expect, receipt)
	case models.SoulOperationKindPublishReputationRoot, models.SoulOperationKindPublishValidationRoot:
		return validateSoulPublishRootExecutionEffect(op, expect, receipt)
	default:
		return nil
	}
}

func (s *Server) validateSoulMintExecutionEffect(ctx context.Context, client ethRPCClient, op *models.SoulOperation, expect soulOperationReceiptExpectation, receipt *types.Receipt) *apptheory.AppTheoryError {
	mint, appErr := decodeSoulMintEffect(op, expect.Data)
	if appErr != nil {
		return appErr
	}
	if !receiptHasIndexedLog(receipt, expect.To, soulMintedTopic, topicBig(mint.agentID), topicAddress(mint.to)) {
		return soulOperationReceiptMismatch()
	}
	wallet, err := s.soulRegistryGetAgentWallet(ctx, client, expect.To, mint.agentID)
	if err != nil || !addressesEqual(wallet, mint.to) {
		return soulOperationReceiptMismatch()
	}
	if mint.principal == (common.Address{}) {
		return nil
	}
	if !receiptHasIndexedLog(receipt, expect.To, principalDeclaredTopic, topicBig(mint.agentID), topicAddress(mint.principal)) {
		return soulOperationReceiptMismatch()
	}
	onChainPrincipal, err := s.soulRegistryPrincipalOf(ctx, client, expect.To, mint.agentID)
	if err != nil || !addressesEqual(onChainPrincipal, mint.principal) {
		return soulOperationReceiptMismatch()
	}
	return nil
}

type soulMintExecutionEffect struct {
	to        common.Address
	agentID   *big.Int
	principal common.Address
}

func decodeSoulMintEffect(op *models.SoulOperation, data []byte) (soulMintExecutionEffect, *apptheory.AppTheoryError) {
	method, args, ok := unpackCallData(soulRegistryReceiptABI, data)
	if !ok || !isSoulMintMethod(method.Name) || len(args) < 3 {
		return soulMintExecutionEffect{}, soulOperationReceiptMismatch()
	}
	to, ok := args[0].(common.Address)
	if !ok {
		return soulMintExecutionEffect{}, soulOperationReceiptMismatch()
	}
	agentID, ok := args[1].(*big.Int)
	if !ok || !soulOperationAgentMatches(op, agentID) {
		return soulMintExecutionEffect{}, soulOperationReceiptMismatch()
	}
	principal, appErr := decodeSelfMintPrincipal(method.Name, args)
	if appErr != nil {
		return soulMintExecutionEffect{}, appErr
	}
	return soulMintExecutionEffect{to: to, agentID: agentID, principal: principal}, nil
}

func decodeSelfMintPrincipal(methodName string, args []any) (common.Address, *apptheory.AppTheoryError) {
	if methodName != "selfMintSoul" {
		return common.Address{}, nil
	}
	if len(args) < 5 {
		return common.Address{}, soulOperationReceiptMismatch()
	}
	principal, ok := args[4].(common.Address)
	if !ok {
		return common.Address{}, soulOperationReceiptMismatch()
	}
	return principal, nil
}

func (s *Server) validateSoulRotateWalletExecutionEffect(ctx context.Context, client ethRPCClient, op *models.SoulOperation, expect soulOperationReceiptExpectation, receipt *types.Receipt) *apptheory.AppTheoryError {
	method, args, ok := unpackCallData(soulRegistryReceiptABI, expect.Data)
	if !ok || method.Name != "rotateWallet" || len(args) < 2 {
		return soulOperationReceiptMismatch()
	}
	agentID, ok := args[0].(*big.Int)
	if !ok || !soulOperationAgentMatches(op, agentID) {
		return soulOperationReceiptMismatch()
	}
	newWallet, ok := args[1].(common.Address)
	if !ok {
		return soulOperationReceiptMismatch()
	}
	if !receiptHasLogWithTopic(receipt, expect.To, walletRotatedTopic, 1, topicBig(agentID)) || !receiptHasLogWithTopic(receipt, expect.To, walletRotatedTopic, 3, topicAddress(newWallet)) {
		return soulOperationReceiptMismatch()
	}
	onChainWallet, err := s.soulRegistryGetAgentWallet(ctx, client, expect.To, agentID)
	if err != nil || !addressesEqual(onChainWallet, newWallet) {
		return soulOperationReceiptMismatch()
	}
	return nil
}

func (s *Server) validateSoulBurnExecutionEffect(ctx context.Context, client ethRPCClient, op *models.SoulOperation, expect soulOperationReceiptExpectation, receipt *types.Receipt) *apptheory.AppTheoryError {
	method, args, ok := unpackCallData(soulRegistryReceiptABI, expect.Data)
	if !ok || method.Name != "burnSoul" || len(args) < 1 {
		return soulOperationReceiptMismatch()
	}
	agentID, ok := args[0].(*big.Int)
	if !ok || !soulOperationAgentMatches(op, agentID) {
		return soulOperationReceiptMismatch()
	}
	if !receiptHasLogWithTopic(receipt, expect.To, soulBurnedTopic, 1, topicBig(agentID)) {
		return soulOperationReceiptMismatch()
	}
	onChainWallet, err := s.soulRegistryGetAgentWallet(ctx, client, expect.To, agentID)
	if err != nil || onChainWallet != (common.Address{}) {
		return soulOperationReceiptMismatch()
	}
	return nil
}

func (s *Server) soulRegistryPrincipalOf(ctx context.Context, client ethRPCClient, contract common.Address, agentID *big.Int) (common.Address, error) {
	data, err := soul.EncodePrincipalOfCall(agentID)
	if err != nil {
		return common.Address{}, err
	}
	ret, err := client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: data}, nil)
	if err != nil {
		return common.Address{}, err
	}
	return soul.DecodePrincipalOfResult(ret)
}

func validateSoulPublishRootExecutionEffect(op *models.SoulOperation, expect soulOperationReceiptExpectation, receipt *types.Receipt) *apptheory.AppTheoryError {
	method, args, ok := unpackCallData(rootAttestationReceiptABI, expect.Data)
	if !ok || method.Name != "publishRoot" || len(args) < 1 {
		return soulOperationReceiptMismatch()
	}
	root, ok := args[0].([32]byte)
	if !ok {
		return soulOperationReceiptMismatch()
	}
	if !receiptHasLogWithTopic(receipt, expect.To, rootPublishedTopic, 1, common.BytesToHash(root[:])) {
		return soulOperationReceiptMismatch()
	}
	return nil
}

func unpackCallData(parsed abi.ABI, data []byte) (*abi.Method, []any, bool) {
	if len(data) < 4 {
		return nil, nil, false
	}
	method, err := parsed.MethodById(data[:4])
	if err != nil || method == nil {
		return nil, nil, false
	}
	args, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		return nil, nil, false
	}
	return method, args, true
}

func isSoulMintMethod(name string) bool {
	switch name {
	case "mintSoul", "mintSoulOwner", "selfMintSoul":
		return true
	default:
		return false
	}
}

func soulOperationAgentMatches(op *models.SoulOperation, agentID *big.Int) bool {
	if op == nil || agentID == nil {
		return false
	}
	expected, ok := new(big.Int).SetString(strings.TrimPrefix(strings.TrimSpace(op.AgentID), "0x"), 16)
	return ok && expected.Cmp(agentID) == 0
}

func receiptHasIndexedLog(receipt *types.Receipt, address common.Address, topic0 common.Hash, indexed ...common.Hash) bool {
	for _, lg := range receiptLogs(receipt) {
		if !addressesEqual(lg.Address, address) || len(lg.Topics) < 1+len(indexed) || lg.Topics[0] != topic0 {
			continue
		}
		matched := true
		for i, want := range indexed {
			if lg.Topics[i+1] != want {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func receiptHasLogWithTopic(receipt *types.Receipt, address common.Address, topic0 common.Hash, topicIndex int, topic common.Hash) bool {
	for _, lg := range receiptLogs(receipt) {
		if !addressesEqual(lg.Address, address) || len(lg.Topics) <= topicIndex || lg.Topics[0] != topic0 {
			continue
		}
		if lg.Topics[topicIndex] == topic {
			return true
		}
	}
	return false
}

func receiptLogs(receipt *types.Receipt) []*types.Log {
	if receipt == nil {
		return nil
	}
	return receipt.Logs
}

func topicBig(v *big.Int) common.Hash {
	if v == nil {
		return common.Hash{}
	}
	return common.BigToHash(v)
}

func topicAddress(v common.Address) common.Hash {
	return common.BytesToHash(v.Bytes())
}

func addressesEqual(a common.Address, b common.Address) bool {
	return bytes.Equal(a.Bytes(), b.Bytes())
}

func bigIntsEqual(a *big.Int, b *big.Int) bool {
	if a == nil {
		a = new(big.Int)
	}
	if b == nil {
		b = new(big.Int)
	}
	return a.Cmp(b) == 0
}

func abiOperationIsCall(v any) bool {
	switch n := v.(type) {
	case uint8:
		return n == 0
	case *big.Int:
		return n != nil && n.Sign() == 0
	default:
		return false
	}
}

func soulOperationReceiptMismatch() *apptheory.AppTheoryError {
	return newAppTheoryError("app.bad_request", "execution receipt does not match operation")
}
