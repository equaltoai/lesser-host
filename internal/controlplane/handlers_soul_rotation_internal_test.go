package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type stubEthRPCClient struct {
	t            *testing.T
	callContract func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
}

func (c *stubEthRPCClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if c.callContract == nil {
		c.t.Fatalf("unexpected CallContract")
	}
	return c.callContract(ctx, msg, blockNumber)
}

func (c *stubEthRPCClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	c.t.Fatalf("unexpected TransactionReceipt(%s)", txHash.Hex())
	return nil, nil
}

func (c *stubEthRPCClient) Close() {}

func TestParseSoulAgentIDHex(t *testing.T) {
	t.Parallel()

	_, _, err := parseSoulAgentIDHex(" ")
	require.NotNil(t, err)
	require.Equal(t, "app.bad_request", err.Code)

	_, _, err = parseSoulAgentIDHex("1234")
	require.NotNil(t, err)
	require.Equal(t, "app.bad_request", err.Code)

	_, _, err = parseSoulAgentIDHex("0x1234")
	require.NotNil(t, err)
	require.Equal(t, "app.bad_request", err.Code)

	_, _, err = parseSoulAgentIDHex("0x" + strings.Repeat("zz", 32))
	require.NotNil(t, err)
	require.Equal(t, "app.bad_request", err.Code)

	valid := "0x" + strings.Repeat("ab", 32)
	gotHex, gotInt, err := parseSoulAgentIDHex(" " + strings.ToUpper(valid) + " ")
	require.Nil(t, err)
	require.Equal(t, strings.ToLower(valid), gotHex)
	require.NotNil(t, gotInt)

	wantInt, ok := new(big.Int).SetString(strings.Repeat("ab", 32), 16)
	require.True(t, ok)
	require.Equal(t, 0, gotInt.Cmp(wantInt))
}

func TestDecodeEthSignature_AdjustsVForRecoveryAndContract(t *testing.T) {
	t.Parallel()

	// invalid
	_, _, err := decodeEthSignature("0x1234")
	require.NotNil(t, err)
	require.Equal(t, "app.bad_request", err.Code)

	// v=27 -> recovery v becomes 0, contract v stays 27
	raw := make([]byte, 65)
	raw[64] = 27
	sigHex := hexutil.Encode(raw)
	recovery, contract, err := decodeEthSignature(sigHex)
	require.Nil(t, err)
	require.Equal(t, byte(0), recovery[64])
	require.Equal(t, byte(27), contract[64])

	// v=0 -> contract v becomes 27
	raw2 := make([]byte, 65)
	raw2[64] = 0
	sigHex2 := hexutil.Encode(raw2)
	recovery, contract, err = decodeEthSignature(sigHex2)
	require.Nil(t, err)
	require.Equal(t, byte(0), recovery[64])
	require.Equal(t, byte(27), contract[64])
}

func TestRecoverAddressFromDigest(t *testing.T) {
	t.Parallel()

	_, err := recoverAddressFromDigest(nil, make([]byte, 65))
	require.Error(t, err)
	_, err = recoverAddressFromDigest(make([]byte, 32), nil)
	require.Error(t, err)

	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	digest := crypto.Keccak256([]byte("rotation-test"))

	sig, err := crypto.Sign(digest, key)
	require.NoError(t, err)

	addr, err := recoverAddressFromDigest(digest, sig)
	require.NoError(t, err)
	require.Equal(t, crypto.PubkeyToAddress(key.PublicKey), addr)
}

func TestSoulRotationTypedData_ProducesDigest(t *testing.T) {
	t.Parallel()

	agentID := big.NewInt(123)
	nonce := big.NewInt(7)
	deadline := int64(1700000000)
	typed, digest, appErr := soulRotationTypedData(1, " 0x0000000000000000000000000000000000000001 ", agentID, "0x000000000000000000000000000000000000dEaD", "0x000000000000000000000000000000000000bEEF", nonce, deadline)
	require.Nil(t, appErr)
	require.Len(t, digest, 32)
	require.Equal(t, "WalletRotationProposal", typed.PrimaryType)
	require.Equal(t, "LesserSoul", typed.Domain.Name)
	require.Equal(t, int64(1), typed.Domain.ChainID)
	require.Equal(t, strings.ToLower("0x0000000000000000000000000000000000000001"), typed.Domain.VerifyingContract)
	require.True(t, strings.HasPrefix(typed.DigestHex, "0x"))
	require.Len(t, typed.DigestHex, 66)
}

func TestParseSoulRotateWalletConfirmRequest(t *testing.T) {
	t.Parallel()

	_, _, err := parseSoulRotateWalletConfirmRequest(nil)
	require.NotNil(t, err)
	require.Equal(t, "app.internal", err.Code)

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{"current_signature":"","new_signature":""}`)}}
	_, _, err = parseSoulRotateWalletConfirmRequest(ctx)
	require.NotNil(t, err)
	require.Equal(t, "app.bad_request", err.Code)
}

func TestValidateSoulWalletRotationConfirmRequest(t *testing.T) {
	t.Parallel()

	require.NotNil(t, validateSoulWalletRotationConfirmRequest(nil, time.Now().UTC()))

	now := time.Now().UTC()
	require.NotNil(t, validateSoulWalletRotationConfirmRequest(&models.SoulWalletRotationRequest{Spent: true}, now))
	require.NotNil(t, validateSoulWalletRotationConfirmRequest(&models.SoulWalletRotationRequest{ExpiresAt: now.Add(-1 * time.Minute)}, now))
	require.Nil(t, validateSoulWalletRotationConfirmRequest(&models.SoulWalletRotationRequest{ExpiresAt: now.Add(1 * time.Minute)}, now))
}

func TestValidateSoulWalletRotationSignatures_SuccessAndMismatch(t *testing.T) {
	t.Parallel()

	keyCurrent, err := crypto.GenerateKey()
	require.NoError(t, err)
	keyNew, err := crypto.GenerateKey()
	require.NoError(t, err)

	currentAddr := crypto.PubkeyToAddress(keyCurrent.PublicKey)
	newAddr := crypto.PubkeyToAddress(keyNew.PublicKey)

	agentID := big.NewInt(123)
	nonce := big.NewInt(9)
	deadline := int64(1700000000)
	typed, digest, appErr := soulRotationTypedData(1, "0x0000000000000000000000000000000000000001", agentID, currentAddr.Hex(), newAddr.Hex(), nonce, deadline)
	require.Nil(t, appErr)

	currentSig, err := crypto.Sign(digest, keyCurrent)
	require.NoError(t, err)
	newSig, err := crypto.Sign(digest, keyNew)
	require.NoError(t, err)

	rot := &models.SoulWalletRotationRequest{
		DigestHex:     typed.DigestHex,
		CurrentWallet: strings.ToLower(currentAddr.Hex()),
		NewWallet:     strings.ToLower(newAddr.Hex()),
		Nonce:         nonce.String(),
		Deadline:      deadline,
	}

	gotNonce, gotDeadline, currentContract, newContract, sigAppErr := validateSoulWalletRotationSignatures(rot, hexutil.Encode(currentSig), hexutil.Encode(newSig))
	require.Nil(t, sigAppErr)
	require.Equal(t, 0, gotNonce.Cmp(nonce))
	require.Equal(t, big.NewInt(deadline), gotDeadline)
	require.Len(t, currentContract, 65)
	require.Len(t, newContract, 65)
	require.True(t, currentContract[64] == 27 || currentContract[64] == 28)
	require.True(t, newContract[64] == 27 || newContract[64] == 28)

	// mismatch: current signature from new wallet should fail
	_, _, _, _, sigAppErr = validateSoulWalletRotationSignatures(rot, hexutil.Encode(newSig), hexutil.Encode(newSig))
	require.NotNil(t, sigAppErr)
	require.Equal(t, "app.bad_request", sigAppErr.Code)
}

func TestVerifySoulWalletRotationOnChainState(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	require.NoError(t, err)

	agentID := big.NewInt(123)
	walletCall, err := soul.EncodeGetAgentWalletCall(agentID)
	require.NoError(t, err)
	nonceCall, err := soul.EncodeAgentNoncesCall(agentID)
	require.NoError(t, err)

	onChainWallet := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	onChainNonce := big.NewInt(9)

	walletRet, err := parsedABI.Methods["getAgentWallet"].Outputs.Pack(onChainWallet)
	require.NoError(t, err)
	nonceRet, err := parsedABI.Methods["agentNonces"].Outputs.Pack(onChainNonce)
	require.NoError(t, err)

	s := &Server{}
	client := &stubEthRPCClient{
		t: t,
		callContract: func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
			switch {
			case bytes.Equal(msg.Data, walletCall):
				return walletRet, nil
			case bytes.Equal(msg.Data, nonceCall):
				return nonceRet, nil
			default:
				t.Fatalf("unexpected call data: %x", msg.Data)
				return nil, nil
			}
		},
	}

	rot := &models.SoulWalletRotationRequest{CurrentWallet: strings.ToLower(onChainWallet.Hex())}
	require.Nil(t, s.verifySoulWalletRotationOnChainState(context.Background(), client, common.Address{}, agentID, rot, onChainNonce))

	rot2 := &models.SoulWalletRotationRequest{CurrentWallet: "0x00000000000000000000000000000000000000bb"}
	appErr := s.verifySoulWalletRotationOnChainState(context.Background(), client, common.Address{}, agentID, rot2, onChainNonce)
	require.NotNil(t, appErr)
	require.Equal(t, "app.conflict", appErr.Code)
}

func TestExistingSoulWalletRotationBeginResponse_Branches(t *testing.T) {
	t.Parallel()

	agentIDHex := "0x" + strings.Repeat("11", 32)
	agentInt := big.NewInt(123)
	newWallet := "0x00000000000000000000000000000000000000aa"
	currentWallet := "0x00000000000000000000000000000000000000bb"
	now := time.Now().UTC()

	base := models.SoulWalletRotationRequest{
		AgentID:       agentIDHex,
		Username:      "alice",
		CurrentWallet: currentWallet,
		NewWallet:     newWallet,
		Nonce:         "7",
		Deadline:      now.Add(10 * time.Minute).Unix(),
		DigestHex:     "0x" + strings.Repeat("11", 32),
		Spent:         false,
		ExpiresAt:     now.Add(10 * time.Minute),
	}

	tests := []struct {
		name       string
		firstErr   error
		existing   models.SoulWalletRotationRequest
		agentInt   *big.Int
		newWallet  string
		wantOK     bool
		wantStatus int
	}{
		{name: "not_found", firstErr: theoryErrors.ErrItemNotFound, agentInt: agentInt, newWallet: newWallet, wantOK: false},
		{name: "spent", existing: func() models.SoulWalletRotationRequest { r := base; r.Spent = true; return r }(), agentInt: agentInt, newWallet: newWallet, wantOK: false},
		{name: "expired", existing: func() models.SoulWalletRotationRequest { r := base; r.ExpiresAt = now.Add(-1 * time.Minute); return r }(), agentInt: agentInt, newWallet: newWallet, wantOK: false},
		{name: "wallet_mismatch", existing: base, agentInt: agentInt, newWallet: "0x00000000000000000000000000000000000000cc", wantOK: false},
		{name: "nonce_invalid", existing: func() models.SoulWalletRotationRequest { r := base; r.Nonce = "nope"; return r }(), agentInt: agentInt, newWallet: newWallet, wantOK: false},
		{name: "typed_error_agent_nil", existing: base, agentInt: nil, newWallet: newWallet, wantOK: false},
		{name: "success", existing: base, agentInt: agentInt, newWallet: newWallet, wantOK: true, wantStatus: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := ttmocks.NewMockExtendedDB()
			qRot := new(ttmocks.MockQuery)

			db.On("WithContext", mock.Anything).Return(db).Maybe()
			db.On("Model", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(qRot).Maybe()
			qRot.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qRot).Maybe()

			if tt.firstErr != nil {
				qRot.On("First", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(tt.firstErr).Once()
			} else {
				qRot.On("First", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(nil).Run(func(args mock.Arguments) {
					dest := testutil.RequireMockArg[*models.SoulWalletRotationRequest](t, args, 0)
					*dest = tt.existing
					_ = dest.UpdateKeys()
				}).Once()
			}

			s := &Server{
				store: store.New(db),
				cfg: config.Config{
					SoulChainID:                 1,
					SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
				},
			}
			ctx := &apptheory.Context{AuthIdentity: "alice"}

			resp, ok, appErr := s.existingSoulWalletRotationBeginResponse(ctx, agentIDHex, tt.agentInt, tt.newWallet)
			require.Nil(t, appErr)
			require.Equal(t, tt.wantOK, ok)

			if !tt.wantOK {
				require.Nil(t, resp)
				return
			}

			require.NotNil(t, resp)
			require.Equal(t, tt.wantStatus, resp.Status)

			var out soulRotateWalletBeginResponse
			require.NoError(t, json.Unmarshal(resp.Body, &out))
			require.Equal(t, strings.ToLower(agentIDHex), strings.ToLower(out.Rotation.AgentID))
			require.NotEmpty(t, out.Typed.DigestHex)
		})
	}
}

func TestGetSoulWalletRotationRequest_RequiresUsername(t *testing.T) {
	t.Parallel()

	s := &Server{}
	rot, err := s.getSoulWalletRotationRequest(context.Background(), "0x"+strings.Repeat("11", 32), " ")
	require.Error(t, err)
	require.Nil(t, rot)
}

func TestSoulRotationTypedData_NilInputs(t *testing.T) {
	t.Parallel()

	_, _, appErr := soulRotationTypedData(1, "0x0000000000000000000000000000000000000001", nil, "0x0", "0x0", big.NewInt(1), 123)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	_, _, appErr = soulRotationTypedData(1, "0x0000000000000000000000000000000000000001", big.NewInt(1), "0x0", "0x0", nil, 123)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestRequireSoulAgentWalletInSync_Branches(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	require.NoError(t, err)

	agentInt := big.NewInt(1)
	callData, err := soul.EncodeGetAgentWalletCall(agentInt)
	require.NoError(t, err)

	walletOK := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	retOK, err := parsedABI.Methods["getAgentWallet"].Outputs.Pack(walletOK)
	require.NoError(t, err)
	retZero, err := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.Address{})
	require.NoError(t, err)

	s := &Server{}

	// client error
	clientErr := &stubEthRPCClient{t: t, callContract: func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return nil, errors.New("boom")
	}}
	_, appErr := s.requireSoulAgentWalletInSync(context.Background(), clientErr, common.Address{}, agentInt, walletOK.Hex())
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	// zero wallet => not minted
	clientZero := &stubEthRPCClient{t: t, callContract: func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if bytes.Equal(msg.Data, callData) {
			return retZero, nil
		}
		return nil, ethereum.NotFound
	}}
	_, appErr = s.requireSoulAgentWalletInSync(context.Background(), clientZero, common.Address{}, agentInt, walletOK.Hex())
	require.NotNil(t, appErr)
	require.Equal(t, "app.conflict", appErr.Code)

	// mismatch wallet
	clientMismatch := &stubEthRPCClient{t: t, callContract: func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if bytes.Equal(msg.Data, callData) {
			return retOK, nil
		}
		return nil, ethereum.NotFound
	}}
	_, appErr = s.requireSoulAgentWalletInSync(context.Background(), clientMismatch, common.Address{}, agentInt, "0x00000000000000000000000000000000000000bb")
	require.NotNil(t, appErr)
	require.Equal(t, "app.conflict", appErr.Code)
}

func TestCreateSoulWalletRotationConfirmResponse_CreateConditionFailedLoadsExisting(t *testing.T) {
	t.Parallel()

	agentIDHex := "0x" + strings.Repeat("11", 32)
	agentInt := big.NewInt(123)
	nonceInt := big.NewInt(7)
	deadlineInt := big.NewInt(1700000000)
	now := time.Unix(123, 0).UTC()

	db := ttmocks.NewMockExtendedDB()
	qOp := new(ttmocks.MockQuery)
	qRot := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(qRot).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	qOp.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qOp).Maybe()
	qOp.On("IfNotExists").Return(qOp).Maybe()
	qOp.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = models.SoulOperation{OperationID: "existing", Kind: models.SoulOperationKindRotateWallet}
		_ = dest.UpdateKeys()
	}).Once()

	qRot.On("IfExists").Return(qRot).Maybe()
	qRot.On("Update", mock.Anything).Return(nil).Maybe()
	qAudit.On("Create").Return(nil).Maybe()

	s := &Server{
		store: store.New(db),
		cfg: config.Config{
			SoulChainID:          1,
			SoulTxMode:           tipTxModeSafe,
			SoulAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "r1"}
	rot := &models.SoulWalletRotationRequest{
		AgentID:       agentIDHex,
		Username:      "alice",
		CurrentWallet: "0x00000000000000000000000000000000000000aa",
		NewWallet:     "0x00000000000000000000000000000000000000bb",
		Nonce:         nonceInt.String(),
		Deadline:      deadlineInt.Int64(),
	}
	_ = rot.UpdateKeys()

	sigA := make([]byte, 65)
	sigB := make([]byte, 65)

	resp, appErr := s.createSoulWalletRotationConfirmResponse(ctx, agentIDHex, agentInt, "0x0000000000000000000000000000000000000001", rot, nonceInt, deadlineInt, sigA, sigB, now)
	require.Nil(t, appErr)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)

	require.True(t, rot.Spent)
	require.False(t, rot.ConfirmedAt.IsZero())

	var out soulRotateWalletConfirmResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, "existing", out.Operation.OperationID)
}

func TestCreateSoulWalletRotationConfirmResponse_CreateErrorFails(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qOp := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	qOp.On("IfNotExists").Return(qOp).Maybe()
	qOp.On("Create").Return(errors.New("boom")).Once()

	s := &Server{
		store: store.New(db),
		cfg: config.Config{
			SoulChainID:          1,
			SoulTxMode:           tipTxModeSafe,
			SoulAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	rot := &models.SoulWalletRotationRequest{NewWallet: "0x00000000000000000000000000000000000000bb", CurrentWallet: "0x00000000000000000000000000000000000000aa", Nonce: "7", Deadline: 1700000000}
	appResp, appErr := s.createSoulWalletRotationConfirmResponse(ctx, "0x"+strings.Repeat("11", 32), big.NewInt(1), "0x0000000000000000000000000000000000000001", rot, big.NewInt(7), big.NewInt(1700000000), make([]byte, 65), make([]byte, 65), time.Now().UTC())
	require.NotNil(t, appErr)
	require.Nil(t, appResp)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestCreateSoulWalletRotationConfirmResponse_DirectModeOmitsSafeAddress(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qOp := new(ttmocks.MockQuery)
	qRot := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(qRot).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	qOp.On("IfNotExists").Return(qOp).Maybe()
	qOp.On("Create").Return(nil).Once()
	qRot.On("IfExists").Return(qRot).Maybe()
	qRot.On("Update", mock.Anything).Return(nil).Maybe()
	qAudit.On("Create").Return(nil).Maybe()

	s := &Server{
		store: store.New(db),
		cfg: config.Config{
			SoulChainID: 1,
			SoulTxMode:  "direct",
		},
	}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "r1"}
	rot := &models.SoulWalletRotationRequest{
		AgentID:       "0x" + strings.Repeat("11", 32),
		Username:      "alice",
		CurrentWallet: "0x00000000000000000000000000000000000000aa",
		NewWallet:     "0x00000000000000000000000000000000000000bb",
		Nonce:         "7",
		Deadline:      1700000000,
	}
	_ = rot.UpdateKeys()

	resp, appErr := s.createSoulWalletRotationConfirmResponse(
		ctx,
		rot.AgentID,
		big.NewInt(1),
		"0x0000000000000000000000000000000000000001",
		rot,
		big.NewInt(7),
		big.NewInt(1700000000),
		make([]byte, 65),
		make([]byte, 65),
		time.Now().UTC(),
	)
	require.Nil(t, appErr)
	require.NotNil(t, resp)

	var out soulRotateWalletConfirmResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.NotNil(t, out.SafeTx)
	require.Empty(t, out.SafeTx.SafeAddress)
}
