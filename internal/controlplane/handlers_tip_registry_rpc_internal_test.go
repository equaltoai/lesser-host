package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/tips"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

func newTipRegistryRPCTestServer(t *testing.T, hostCallHex string, hostResultHex string, tokenCallHex string, tokenResultHex string, receipt *types.Receipt) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		var req jsonRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch strings.TrimSpace(req.Method) {
		case "eth_call":
			var params []any
			_ = json.Unmarshal(req.Params, &params)

			data := ""
			if len(params) > 0 {
				if callObj, ok := params[0].(map[string]any); ok {
					if v, ok := callObj["input"].(string); ok {
						data = strings.ToLower(strings.TrimSpace(v))
					} else if v, ok := callObj["data"].(string); ok {
						data = strings.ToLower(strings.TrimSpace(v))
					}
				}
			}

			result := "0x"
			switch data {
			case strings.ToLower(strings.TrimSpace(hostCallHex)):
				result = strings.TrimSpace(hostResultHex)
			case strings.ToLower(strings.TrimSpace(tokenCallHex)):
				result = strings.TrimSpace(tokenResultHex)
			}

			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  result,
			})
			return

		case "eth_getTransactionReceipt":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  receipt,
			})
			return

		default:
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: map[string]any{
					"code":    -32601,
					"message": "method not found",
				},
			})
			return
		}
	}))
}

func TestTipRegistryUpdateRequiresBothProofs_UsesRPCState(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(tips.TipSplitterABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	hostID := common.HexToHash("0x" + strings.Repeat("11", 32))
	hostCall, _ := tips.EncodeGetHostCall(hostID)
	hostCallHex := "0x" + hex.EncodeToString(hostCall)

	hostWallet := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	ret, err := parsedABI.Methods["hosts"].Outputs.Pack(hostWallet, uint16(10), true)
	if err != nil {
		t.Fatalf("pack hosts outputs: %v", err)
	}
	hostResultHex := "0x" + hex.EncodeToString(ret)

	rpcSrv := newTipRegistryRPCTestServer(t, hostCallHex, hostResultHex, "", "", nil)
	t.Cleanup(rpcSrv.Close)

	s := &Server{cfg: config.Config{
		TipRPCURL:          rpcSrv.URL,
		TipContractAddress: "0x0000000000000000000000000000000000000001",
	}}

	reg := &models.TipHostRegistration{
		Kind:      models.TipRegistryOperationKindUpdateHost,
		HostIDHex: hostID.Hex(),
		WalletAddr: "0x00000000000000000000000000000000000000bb", // wallet change
		HostFeeBps: 10,
	}
	requireBoth, why, err2 := s.tipRegistryUpdateRequiresBothProofs(context.Background(), reg)
	if err2 != nil {
		t.Fatalf("unexpected err: %v", err2)
	}
	if !requireBoth || !strings.Contains(why, "wallet") {
		t.Fatalf("expected wallet change to require both proofs, got requireBoth=%v why=%q", requireBoth, why)
	}
}

func TestHandleRecordTipRegistryOperationExecution_RecordsReceiptAndSnapshots(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(tips.TipSplitterABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	hostID := common.HexToHash("0x" + strings.Repeat("11", 32))
	hostCall, _ := tips.EncodeGetHostCall(hostID)
	hostCallHex := "0x" + hex.EncodeToString(hostCall)

	hostWallet := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	ret, err := parsedABI.Methods["hosts"].Outputs.Pack(hostWallet, uint16(10), true)
	if err != nil {
		t.Fatalf("pack hosts outputs: %v", err)
	}
	hostResultHex := "0x" + hex.EncodeToString(ret)

	token := common.HexToAddress("0x00000000000000000000000000000000000000cc")
	tokenCall, _ := tips.EncodeIsTokenAllowedCall(token)
	tokenCallHex := "0x" + hex.EncodeToString(tokenCall)

	tokenRet, err := parsedABI.Methods["allowedTokens"].Outputs.Pack(true)
	if err != nil {
		t.Fatalf("pack allowedTokens outputs: %v", err)
	}
	tokenResultHex := "0x" + hex.EncodeToString(tokenRet)

	txHash := "0x" + strings.Repeat("a", 64)
	receipt := &types.Receipt{
		Status:            1,
		CumulativeGasUsed: 123,
		Bloom:             types.Bloom{},
		Logs:              []*types.Log{},
		TxHash:            common.HexToHash(txHash),
		ContractAddress:   common.HexToAddress("0x0000000000000000000000000000000000000001"),
		GasUsed:           456,
		EffectiveGasPrice: big.NewInt(1000),
		BlockNumber:       big.NewInt(99),
	}

	rpcSrv := newTipRegistryRPCTestServer(t, hostCallHex, hostResultHex, tokenCallHex, tokenResultHex, receipt)
	t.Cleanup(rpcSrv.Close)

	tdb := newTipRegistryTestDB()
	qState := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.TipHostState")).Return(qState).Maybe()
	qState.On("CreateOrUpdate").Return(nil).Maybe()
	qState.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qState).Maybe()

	op := models.TipRegistryOperation{
		ID:               "op1",
		Kind:             models.TipRegistryOperationKindRegisterHost,
		ChainID:          1,
		ContractAddress:  "0x0000000000000000000000000000000000000001",
		DomainNormalized: "example.com",
		HostIDHex:        hostID.Hex(),
		TxTo:             "0x0000000000000000000000000000000000000001",
		TxData:           "0x",
		TxValue:          "0",
		Status:           models.TipRegistryOperationStatusPending,
		CreatedAt:        time.Now().UTC().Add(-1 * time.Minute),
		ProposedAt:       time.Now().UTC().Add(-1 * time.Minute),
	}
	_ = op.UpdateKeys()

	tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.TipRegistryOperation)
		*dest = op
	}).Once()

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:         true,
			TipChainID:         1,
			TipContractAddress: "0x0000000000000000000000000000000000000001",
			TipRPCURL:          rpcSrv.URL,
		},
	}

	ctx := adminCtx()
	ctx.Params = map[string]string{"id": "op1"}
	ctx.Request.Body = []byte(`{"exec_tx_hash":"` + txHash + `"}`)

	resp, err := s.handleRecordTipRegistryOperationExecution(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var out models.TipRegistryOperation
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ExecTxHash != strings.ToLower(txHash) || out.ReceiptJSON == "" || out.SnapshotJSON == "" {
		t.Fatalf("unexpected operation output: %#v", out)
	}
	if out.ExecSuccess == nil || !*out.ExecSuccess {
		t.Fatalf("expected exec success true, got %#v", out.ExecSuccess)
	}
}

func TestHandleRecordTipRegistryOperationExecution_RecordsFailureStatus(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(tips.TipSplitterABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	token := common.HexToAddress("0x00000000000000000000000000000000000000cc")
	tokenCall, _ := tips.EncodeIsTokenAllowedCall(token)
	tokenCallHex := "0x" + hex.EncodeToString(tokenCall)

	tokenRet, err := parsedABI.Methods["allowedTokens"].Outputs.Pack(false)
	if err != nil {
		t.Fatalf("pack allowedTokens outputs: %v", err)
	}
	tokenResultHex := "0x" + hex.EncodeToString(tokenRet)

	txHash := "0x" + strings.Repeat("b", 64)
	receipt := &types.Receipt{
		Status:            0,
		CumulativeGasUsed: 123,
		Bloom:             types.Bloom{},
		Logs:              []*types.Log{},
		TxHash:            common.HexToHash(txHash),
		ContractAddress:   common.HexToAddress("0x0000000000000000000000000000000000000001"),
		GasUsed:           456,
		BlockNumber:       big.NewInt(100),
	}

	rpcSrv := newTipRegistryRPCTestServer(t, "", "", tokenCallHex, tokenResultHex, receipt)
	t.Cleanup(rpcSrv.Close)

	tdb := newTipRegistryTestDB()

	op := models.TipRegistryOperation{
		ID:               "op2",
		Kind:             models.TipRegistryOperationKindSetToken,
		ChainID:          1,
		ContractAddress:  "0x0000000000000000000000000000000000000001",
		DomainNormalized: "example.com",
		TokenAddress:     strings.ToLower(token.Hex()),
		TxTo:             "0x0000000000000000000000000000000000000001",
		TxData:           "0x",
		TxValue:          "0",
		Status:           models.TipRegistryOperationStatusPending,
		CreatedAt:        time.Now().UTC().Add(-1 * time.Minute),
		ProposedAt:       time.Now().UTC().Add(-1 * time.Minute),
	}
	_ = op.UpdateKeys()

	tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.TipRegistryOperation)
		*dest = op
	}).Once()

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:         true,
			TipChainID:         1,
			TipContractAddress: "0x0000000000000000000000000000000000000001",
			TipRPCURL:          rpcSrv.URL,
		},
	}

	ctx := adminCtx()
	ctx.Params = map[string]string{"id": "op2"}
	ctx.Request.Body = []byte(`{"exec_tx_hash":"` + txHash + `"}`)

	resp, err := s.handleRecordTipRegistryOperationExecution(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var out models.TipRegistryOperation
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ExecSuccess == nil || *out.ExecSuccess {
		t.Fatalf("expected exec success false, got %#v", out.ExecSuccess)
	}
	if out.Status != models.TipRegistryOperationStatusFailed {
		t.Fatalf("expected failed status, got %q", out.Status)
	}
	if strings.TrimSpace(out.SnapshotJSON) == "" || strings.TrimSpace(out.ReceiptJSON) == "" {
		t.Fatalf("expected snapshot and receipt json set, got %#v", out)
	}
}
