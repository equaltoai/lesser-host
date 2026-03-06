package trust

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const testENSGatewayPrivateKeyHex = "4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f8f54c9bde2b4976f7"

func testENSGatewaySignerAddress(t *testing.T) common.Address {
	t.Helper()

	key, err := crypto.HexToECDSA(testENSGatewayPrivateKeyHex)
	require.NoError(t, err)
	return crypto.PubkeyToAddress(key.PublicKey)
}

func requireENSGatewayError(t *testing.T, err error, code string, status int) {
	t.Helper()

	require.Error(t, err)

	if appErr, ok := apptheory.AsAppTheoryError(err); ok {
		require.Equal(t, code, appErr.Code)
		if status > 0 {
			require.Equal(t, status, appErr.StatusCode)
		}
		return
	}

	var appErr *apptheory.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, code, appErr.Code)
	require.Zero(t, status)
}

func buildENSGatewayInnerCall(t *testing.T, selector []byte, args []byte) []byte {
	t.Helper()

	return append(append([]byte(nil), selector...), args...)
}

func buildENSGatewayResolveCall(t *testing.T, ensName string, selector []byte, args []byte) []byte {
	t.Helper()

	encodedName, err := encodeDNSName(ensName)
	require.NoError(t, err)

	innerData := buildENSGatewayInnerCall(t, selector, args)
	callArgs, err := ensResolveInputs.Pack(encodedName, innerData)
	require.NoError(t, err)

	return append(append([]byte(nil), ensResolveSelector...), callArgs...)
}

func decodeENSGatewayResponse(t *testing.T, body []byte) ([]byte, uint64, []byte) {
	t.Helper()

	var out ensGatewayResolveJSON
	require.NoError(t, json.Unmarshal(body, &out))
	require.NotEmpty(t, out.Data)

	responseBytes, err := hexutil.Decode(out.Data)
	require.NoError(t, err)

	decoded, err := ensGatewayResponseABI.Unpack(responseBytes)
	require.NoError(t, err)
	require.Len(t, decoded, 3)

	resultBytes := testutil.RequireType[[]byte](t, decoded[0])
	expires := testutil.RequireType[uint64](t, decoded[1])
	sigCompact := testutil.RequireType[[]byte](t, decoded[2])
	return resultBytes, expires, sigCompact
}

func TestHandleENSGatewayHealth(t *testing.T) {
	t.Parallel()

	resp, err := (&Server{}).handleENSGatewayHealth(nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)
	require.Equal(t, []string{"no-store"}, resp.Headers["cache-control"])

	var out map[string]any
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, true, out["ok"])
	require.Equal(t, "ens-gateway", out["service"])
}

func TestHandleENSGatewayResolve_Guards(t *testing.T) {
	t.Parallel()

	newServer := func(cfg config.Config) *Server {
		return NewServer(cfg, store.New(newTestDBWithModelQueries()))
	}

	validResolver := common.HexToAddress("0x0000000000000000000000000000000000001234").Hex()

	tests := []struct {
		name   string
		srv    *Server
		ctx    *apptheory.Context
		code   string
		status int
	}{
		{
			name: "nil server",
			srv:  nil,
			ctx:  &apptheory.Context{},
			code: "app.internal",
		},
		{
			name: "nil ctx",
			srv: newServer(config.Config{
				SoulEnabled:                 true,
				ENSGatewaySigningPrivateKey: testENSGatewayPrivateKeyHex,
			}),
			ctx:  nil,
			code: "app.internal",
		},
		{
			name: "soul disabled",
			srv: newServer(config.Config{
				ENSGatewaySigningPrivateKey: testENSGatewayPrivateKeyHex,
			}),
			ctx:  &apptheory.Context{},
			code: "app.not_found",
		},
		{
			name: "signer missing",
			srv: newServer(config.Config{
				SoulEnabled: true,
			}),
			ctx:  &apptheory.Context{},
			code: "app.not_found",
		},
		{
			name: "invalid signer",
			srv: newServer(config.Config{
				SoulEnabled:                 true,
				ENSGatewaySigningPrivateKey: "not-hex",
			}),
			ctx:  &apptheory.Context{},
			code: "app.internal",
		},
		{
			name: "sender required",
			srv: newServer(config.Config{
				SoulEnabled:                 true,
				ENSGatewaySigningPrivateKey: testENSGatewayPrivateKeyHex,
			}),
			ctx:    &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{}}},
			code:   "ccip.bad_request",
			status: 400,
		},
		{
			name: "unsupported sender",
			srv: newServer(config.Config{
				SoulEnabled:                 true,
				ENSGatewaySigningPrivateKey: testENSGatewayPrivateKeyHex,
				ENSGatewayResolverAddress:   validResolver,
			}),
			ctx: &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
				"sender": {common.HexToAddress("0x0000000000000000000000000000000000005678").Hex()},
			}}},
			code:   "ccip.sender_unsupported",
			status: 404,
		},
		{
			name: "invalid sender",
			srv: newServer(config.Config{
				SoulEnabled:                 true,
				ENSGatewaySigningPrivateKey: testENSGatewayPrivateKeyHex,
			}),
			ctx: &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
				"sender": {"not-an-address"},
			}}},
			code:   "ccip.bad_request",
			status: 400,
		},
		{
			name: "data required",
			srv: newServer(config.Config{
				SoulEnabled:                 true,
				ENSGatewaySigningPrivateKey: testENSGatewayPrivateKeyHex,
			}),
			ctx: &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
				"sender": {validResolver},
			}}},
			code:   "ccip.bad_request",
			status: 400,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := tt.srv.handleENSGatewayResolve(tt.ctx)
			require.Nil(t, resp)
			requireENSGatewayError(t, err, tt.code, tt.status)
		})
	}
}

func TestHandleENSGatewayResolve_CompatibilityMode_Contenthash(t *testing.T) {
	t.Parallel()

	resolver := common.HexToAddress("0x0000000000000000000000000000000000004444").Hex()
	srv := NewServer(config.Config{
		SoulEnabled:                   true,
		ENSGatewayResolverAddress:     resolver,
		ENSGatewaySigningPrivateKey:   testENSGatewayPrivateKeyHex,
		ENSGatewaySignatureTTLSeconds: 45,
	}, store.New(newTestDBWithModelQueries()))

	ensName := "Agent-Case.Lessersoul.eth."
	node := ensNameHash(ensName)
	innerArgs, err := ensContenthashInputs.Pack(node)
	require.NoError(t, err)
	innerData := buildENSGatewayInnerCall(t, ensContenthashSelector, innerArgs)

	resp, err := srv.handleENSGatewayResolve(&apptheory.Context{
		Request: apptheory.Request{
			Query: map[string][]string{
				"name": {ensName},
				"data": {"0x" + hex.EncodeToString(innerData)},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)
	require.Equal(t, []string{"public, max-age=45"}, resp.Headers["cache-control"])

	resultBytes, expires, sigCompact := decodeENSGatewayResponse(t, resp.Body)
	require.NotZero(t, expires)
	require.Len(t, sigCompact, 64)

	decoded, err := ensBytesOutputs.Unpack(resultBytes)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	require.Empty(t, testutil.RequireType[[]byte](t, decoded[0]))
}

func TestHandleENSGatewayResolve_Addr_SignsAndEncodes(t *testing.T) {
	t.Parallel()

	signerAddr := testENSGatewaySignerAddress(t)

	ensName := "agent-bob.lessersoul.eth"
	agentID := "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab"
	wallet := "0x000000000000000000000000000000000000beef"

	qENS := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	db := newTestDBWithModelQueries(
		modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qENS},
		modelQueryPair{model: &models.SoulAgentIdentity{}, query: qIdentity},
	)

	qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{
			ENSName:             ensName,
			AgentID:             agentID,
			SoulRegistrationURI: "s3://bucket/registry/v1/agents/" + agentID + "/versions/1/registration.json",
			Email:               "agent-bob@lessersoul.ai",
			Description:         "hello",
			Status:              "active",
		}
		_ = dest.UpdateKeys()
	}).Once()

	qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Wallet:          wallet,
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
		}
		_ = dest.UpdateKeys()
	}).Once()

	srv := NewServer(config.Config{
		SoulEnabled:                   true,
		ENSGatewaySigningPrivateKey:   testENSGatewayPrivateKeyHex,
		ENSGatewaySignatureTTLSeconds: 300,
	}, store.New(db))

	node := ensNameHash(ensName)
	innerArgs, err := ensAddrInputs.Pack(node)
	require.NoError(t, err)
	callData := buildENSGatewayResolveCall(t, ensName, ensAddrSelector, innerArgs)

	target := common.HexToAddress("0x0000000000000000000000000000000000001234")

	resp, err := srv.handleENSGatewayResolve(&apptheory.Context{
		Request: apptheory.Request{
			Query: map[string][]string{
				"sender": {target.Hex()},
				"data":   {"0x" + hex.EncodeToString(callData)},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)

	resultBytes, expires, sigCompact := decodeENSGatewayResponse(t, resp.Body)
	require.Len(t, sigCompact, 64)

	sigHash := makeENSSignatureHash(target, expires, callData, resultBytes)
	require.Len(t, sigHash, 32)
	var digest [32]byte
	copy(digest[:], sigHash)

	sig65, err := compactToSig65(sigCompact)
	require.NoError(t, err)
	pub, err := crypto.SigToPub(digest[:], sig65)
	require.NoError(t, err)
	recovered := crypto.PubkeyToAddress(*pub)
	require.Equal(t, signerAddr, recovered)

	resolved, err := ensAddrOutputs.Unpack(resultBytes)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	require.Equal(t, common.HexToAddress(wallet), testutil.RequireType[common.Address](t, resolved[0]))
}

func TestHandleENSGatewayResolve_Text_StatusResolvesDeterministically(t *testing.T) {
	t.Parallel()

	ensName := "agent-alice.lessersoul.eth"
	agentID := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	qENS := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	db := newTestDBWithModelQueries(
		modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qENS},
		modelQueryPair{model: &models.SoulAgentIdentity{}, query: qIdentity},
	)

	qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: ensName, AgentID: agentID}
		_ = dest.UpdateKeys()
	}).Once()

	qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Wallet:          "0x000000000000000000000000000000000000dEaD",
			Status:          models.SoulAgentStatusArchived,
			LifecycleStatus: models.SoulAgentStatusArchived,
		}
		_ = dest.UpdateKeys()
	}).Once()

	srv := NewServer(config.Config{
		SoulEnabled:                 true,
		ENSGatewaySigningPrivateKey: testENSGatewayPrivateKeyHex,
	}, store.New(db))

	node := ensNameHash(ensName)
	innerArgs, err := ensTextInputs.Pack(node, "soul.status")
	require.NoError(t, err)
	callData := buildENSGatewayResolveCall(t, ensName, ensTextSelector, innerArgs)

	target := common.HexToAddress("0x0000000000000000000000000000000000001234")
	resp, err := srv.handleENSGatewayResolve(&apptheory.Context{
		Request: apptheory.Request{
			Query: map[string][]string{
				"sender": {target.Hex()},
				"data":   {"0x" + hex.EncodeToString(callData)},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	resultBytes, _, _ := decodeENSGatewayResponse(t, resp.Body)
	resolved, err := ensStringOutputs.Unpack(resultBytes)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	require.Equal(t, "archived", resolved[0])
}

func TestAnswerENSQuery_Branches(t *testing.T) {
	t.Parallel()

	ensName := "agent-query.lessersoul.eth"
	node := ensNameHash(ensName)

	t.Run("inner data too short", func(t *testing.T) {
		t.Parallel()

		_, err := (&Server{}).answerENSQuery(context.Background(), ensName, node, []byte{1, 2, 3})
		requireENSGatewayError(t, err, "ccip.bad_request", 400)
	})

	t.Run("unsupported selector", func(t *testing.T) {
		t.Parallel()

		_, err := (&Server{}).answerENSQuery(context.Background(), ensName, node, []byte{9, 8, 7, 6})
		requireENSGatewayError(t, err, "ccip.unsupported", 400)
	})

	t.Run("addr node mismatch", func(t *testing.T) {
		t.Parallel()

		args, err := ensAddrInputs.Pack(ensNameHash("someone-else.lessersoul.eth"))
		require.NoError(t, err)
		_, err = (&Server{}).answerENSQuery(context.Background(), ensName, node, buildENSGatewayInnerCall(t, ensAddrSelector, args))
		requireENSGatewayError(t, err, "ccip.bad_request", 400)
	})

	t.Run("addr missing material returns zero address", func(t *testing.T) {
		t.Parallel()

		qENS := new(ttmocks.MockQuery)
		db := newTestDBWithModelQueries(modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qENS})
		qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()

		srv := NewServer(config.Config{}, store.New(db))
		args, err := ensAddrInputs.Pack(node)
		require.NoError(t, err)

		result, err := srv.answerENSQuery(context.Background(), ensName, node, buildENSGatewayInnerCall(t, ensAddrSelector, args))
		require.NoError(t, err)

		decoded, err := ensAddrOutputs.Unpack(result)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Equal(t, common.Address{}, testutil.RequireType[common.Address](t, decoded[0]))
	})

	t.Run("addr coin non eth returns empty bytes", func(t *testing.T) {
		t.Parallel()

		args, err := ensAddrCoinInputs.Pack(node, big.NewInt(61))
		require.NoError(t, err)

		result, err := (&Server{}).answerENSQuery(context.Background(), ensName, node, buildENSGatewayInnerCall(t, ensAddrCoinSelector, args))
		require.NoError(t, err)

		decoded, err := ensBytesOutputs.Unpack(result)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Empty(t, testutil.RequireType[[]byte](t, decoded[0]))
	})

	t.Run("addr coin eth returns wallet bytes", func(t *testing.T) {
		t.Parallel()

		agentID := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		wallet := "0x000000000000000000000000000000000000c0de"

		qENS := new(ttmocks.MockQuery)
		qIdentity := new(ttmocks.MockQuery)
		db := newTestDBWithModelQueries(
			modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qENS},
			modelQueryPair{model: &models.SoulAgentIdentity{}, query: qIdentity},
		)

		qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
			*dest = models.SoulAgentENSResolution{ENSName: ensName, AgentID: agentID}
		}).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: agentID, Wallet: wallet, LifecycleStatus: models.SoulAgentStatusActive}
		}).Once()

		srv := NewServer(config.Config{}, store.New(db))
		args, err := ensAddrCoinInputs.Pack(node, big.NewInt(60))
		require.NoError(t, err)

		result, err := srv.answerENSQuery(context.Background(), ensName, node, buildENSGatewayInnerCall(t, ensAddrCoinSelector, args))
		require.NoError(t, err)

		decoded, err := ensBytesOutputs.Unpack(result)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Equal(t, common.HexToAddress(wallet).Bytes(), testutil.RequireType[[]byte](t, decoded[0]))
	})
}

func TestLoadENSGatewayMaterial_CacheAndFallbacks(t *testing.T) {
	t.Parallel()

	t.Run("nil store errors", func(t *testing.T) {
		t.Parallel()

		_, ok, err := (&Server{}).loadENSGatewayMaterial(context.Background(), "agent.lessersoul.eth")
		require.Error(t, err)
		require.False(t, ok)
	})

	t.Run("blank name returns miss", func(t *testing.T) {
		t.Parallel()

		srv := NewServer(config.Config{}, store.New(newTestDBWithModelQueries()))
		material, ok, err := srv.loadENSGatewayMaterial(context.Background(), " ")
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, ensGatewayMaterial{}, material)
	})

	t.Run("resolution not found caches miss", func(t *testing.T) {
		t.Parallel()

		qENS := new(ttmocks.MockQuery)
		db := newTestDBWithModelQueries(modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qENS})
		qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()

		srv := NewServer(config.Config{ENSGatewaySignatureTTLSeconds: 5}, store.New(db))

		first, ok, err := srv.loadENSGatewayMaterial(context.Background(), "agent.lessersoul.eth")
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, ensGatewayMaterial{}, first)

		second, ok, err := srv.loadENSGatewayMaterial(context.Background(), "agent.lessersoul.eth")
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, ensGatewayMaterial{}, second)
	})

	t.Run("blank agent id caches miss", func(t *testing.T) {
		t.Parallel()

		qENS := new(ttmocks.MockQuery)
		db := newTestDBWithModelQueries(modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qENS})
		qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
			*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: " "}
		}).Once()

		srv := NewServer(config.Config{}, store.New(db))
		_, ok, err := srv.loadENSGatewayMaterial(context.Background(), "agent.lessersoul.eth")
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("identity miss caches miss", func(t *testing.T) {
		t.Parallel()

		qENS := new(ttmocks.MockQuery)
		qIdentity := new(ttmocks.MockQuery)
		db := newTestDBWithModelQueries(
			modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qENS},
			modelQueryPair{model: &models.SoulAgentIdentity{}, query: qIdentity},
		)

		qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
			*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: "0xabc"}
		}).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

		srv := NewServer(config.Config{}, store.New(db))
		_, ok, err := srv.loadENSGatewayMaterial(context.Background(), "agent.lessersoul.eth")
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("success caches material and successor", func(t *testing.T) {
		t.Parallel()

		ensName := "agent.lessersoul.eth"
		agentID := "0xabc"
		successorID := "0xdef"

		qENS := new(ttmocks.MockQuery)
		qIdentity := new(ttmocks.MockQuery)
		qChannel := new(ttmocks.MockQuery)
		db := newTestDBWithModelQueries(
			modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qENS},
			modelQueryPair{model: &models.SoulAgentIdentity{}, query: qIdentity},
			modelQueryPair{model: &models.SoulAgentChannel{}, query: qChannel},
		)

		qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
			*dest = models.SoulAgentENSResolution{
				ENSName:             ensName,
				AgentID:             agentID,
				SoulRegistrationURI: " s3://bucket/agent.json ",
				MCPEndpoint:         " https://mcp.example/agent ",
				ActivityPubURI:      " https://ap.example/@agent ",
				Email:               " hello@example.com ",
				Phone:               " +15551234567 ",
				Description:         " hello world ",
				Status:              " archived ",
			}
		}).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:          agentID,
				Wallet:           " 0x000000000000000000000000000000000000beef ",
				Status:           models.SoulAgentStatusSuspended,
				SuccessorAgentID: " " + successorID + " ",
			}
		}).Once()
		qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
			*dest = models.SoulAgentChannel{Identifier: " Next.Lessersoul.eth. "}
		}).Once()

		srv := NewServer(config.Config{}, store.New(db))

		first, ok, err := srv.loadENSGatewayMaterial(context.Background(), ensName)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "0x000000000000000000000000000000000000beef", first.Wallet)
		require.Equal(t, models.SoulAgentStatusSuspended, first.Status)
		require.Equal(t, "s3://bucket/agent.json", first.RegistrationURI)
		require.Equal(t, "https://mcp.example/agent", first.MCPEndpoint)
		require.Equal(t, "https://ap.example/@agent", first.ActivityPubURI)
		require.Equal(t, "hello@example.com", first.Email)
		require.Equal(t, "+15551234567", first.Phone)
		require.Equal(t, "hello world", first.Description)
		require.Equal(t, successorID, first.SuccessorAgentID)
		require.True(t, first.SuccessorENSOK)
		require.Equal(t, "next.lessersoul.eth", first.SuccessorENSName)

		second, ok, err := srv.loadENSGatewayMaterial(context.Background(), ensName)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, first, second)
	})
}

func TestBestEffortSuccessorENSName(t *testing.T) {
	t.Parallel()

	t.Run("blank input returns miss", func(t *testing.T) {
		t.Parallel()

		name, ok, err := (&Server{}).bestEffortSuccessorENSName(context.Background(), " ")
		require.NoError(t, err)
		require.False(t, ok)
		require.Empty(t, name)
	})

	t.Run("not found returns miss", func(t *testing.T) {
		t.Parallel()

		qChannel := new(ttmocks.MockQuery)
		db := newTestDBWithModelQueries(modelQueryPair{model: &models.SoulAgentChannel{}, query: qChannel})
		qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()

		srv := &Server{store: store.New(db)}
		name, ok, err := srv.bestEffortSuccessorENSName(context.Background(), "0xabc")
		require.NoError(t, err)
		require.False(t, ok)
		require.Empty(t, name)
	})

	t.Run("query error returns error", func(t *testing.T) {
		t.Parallel()

		qChannel := new(ttmocks.MockQuery)
		db := newTestDBWithModelQueries(modelQueryPair{model: &models.SoulAgentChannel{}, query: qChannel})
		qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(errors.New("boom")).Once()

		srv := &Server{store: store.New(db)}
		_, ok, err := srv.bestEffortSuccessorENSName(context.Background(), "0xabc")
		require.Error(t, err)
		require.False(t, ok)
	})
}

func TestParseENSGatewayRequest(t *testing.T) {
	t.Parallel()

	ensName := "Agent.Parse.Lessersoul.eth."
	node := ensNameHash(ensName)
	innerArgs, err := ensContenthashInputs.Pack(node)
	require.NoError(t, err)
	innerData := buildENSGatewayInnerCall(t, ensContenthashSelector, innerArgs)
	callData := buildENSGatewayResolveCall(t, ensName, ensContenthashSelector, innerArgs)

	t.Run("primary calldata mode", func(t *testing.T) {
		t.Parallel()

		gotCallData, gotName, gotInner, err := parseENSGatewayRequest("ignored", "0x"+hex.EncodeToString(callData))
		require.NoError(t, err)
		require.Equal(t, callData, gotCallData)
		require.Equal(t, innerData, gotInner)

		decodedName, err := decodeDNSName(gotName)
		require.NoError(t, err)
		require.Equal(t, "agent.parse.lessersoul.eth", decodedName)
	})

	t.Run("invalid data", func(t *testing.T) {
		t.Parallel()

		_, _, _, err := parseENSGatewayRequest("", "0x123")
		requireENSGatewayError(t, err, "ccip.bad_request", 400)
	})

	t.Run("invalid calldata", func(t *testing.T) {
		t.Parallel()

		_, _, _, err := parseENSGatewayRequest("", "0x"+hex.EncodeToString(ensResolveSelector))
		requireENSGatewayError(t, err, "ccip.bad_request", 400)
	})

	t.Run("compatibility mode requires name", func(t *testing.T) {
		t.Parallel()

		_, _, _, err := parseENSGatewayRequest("", "0x"+hex.EncodeToString(innerData))
		requireENSGatewayError(t, err, "ccip.bad_request", 400)
	})

	t.Run("compatibility mode rejects invalid hex name", func(t *testing.T) {
		t.Parallel()

		_, _, _, err := parseENSGatewayRequest("0xz", "0x"+hex.EncodeToString(innerData))
		requireENSGatewayError(t, err, "ccip.bad_request", 400)
	})

	t.Run("compatibility mode packs plain name", func(t *testing.T) {
		t.Parallel()

		gotCallData, gotName, gotInner, err := parseENSGatewayRequest(" "+ensName+" ", "0x"+hex.EncodeToString(innerData))
		require.NoError(t, err)
		require.Equal(t, innerData, gotInner)

		decodedName, err := decodeDNSName(gotName)
		require.NoError(t, err)
		require.Equal(t, "agent.parse.lessersoul.eth", decodedName)

		require.Equal(t, ensResolveSelector, gotCallData[:4])
		decoded, err := ensResolveInputs.Unpack(gotCallData[4:])
		require.NoError(t, err)
		require.Len(t, decoded, 2)
		require.Equal(t, gotName, testutil.RequireType[[]byte](t, decoded[0]))
		require.Equal(t, gotInner, testutil.RequireType[[]byte](t, decoded[1]))
	})
}

func TestENSGatewayDNSAndTruncateHelpers(t *testing.T) {
	t.Parallel()

	encoded, err := encodeDNSName("Agent.Example.eth.")
	require.NoError(t, err)

	decoded, err := decodeDNSName(encoded)
	require.NoError(t, err)
	require.Equal(t, "agent.example.eth", decoded)

	_, err = encodeDNSName("agent..example.eth")
	require.Error(t, err)

	_, err = encodeDNSName(strings.Repeat("a", 64) + ".eth")
	require.Error(t, err)

	_, err = decodeDNSName([]byte{1, 'a'})
	require.Error(t, err)

	_, err = decodeDNSName([]byte{64})
	require.Error(t, err)

	_, err = decodeDNSName([]byte{2, 'a'})
	require.Error(t, err)

	require.Equal(t, "", truncateUTF8("hello", 0))
	require.Equal(t, "abc", truncateUTF8("abc\U0001f642def", 5))
	require.Equal(t, "\u00e9\u00e9", truncateUTF8("\u00e9\u00e9\u00e9", 4))
}

func TestENSTextValueAndCacheHelpers(t *testing.T) {
	t.Parallel()

	longDescription := strings.Repeat("0123456789", 30)
	material := ensGatewayMaterial{
		AgentID:          " agent-1 ",
		RegistrationURI:  " s3://bucket/agent.json ",
		MCPEndpoint:      " https://mcp.example/agent ",
		ActivityPubURI:   " https://ap.example/@agent ",
		Email:            " hello@example.com ",
		Phone:            " +15551234567 ",
		Description:      longDescription,
		Status:           " active ",
		SuccessorAgentID: " successor ",
		SuccessorENSName: " next.lessersoul.eth ",
		SuccessorENSOK:   true,
	}

	require.Equal(t, "agent-1", ensTextValue(material, "soul.agentId", context.Background()))
	require.Equal(t, "s3://bucket/agent.json", ensTextValue(material, "soul.registration", context.Background()))
	require.Equal(t, "https://mcp.example/agent", ensTextValue(material, "soul.mcp", context.Background()))
	require.Equal(t, "https://ap.example/@agent", ensTextValue(material, "soul.activitypub", context.Background()))
	require.Equal(t, "hello@example.com", ensTextValue(material, "email", context.Background()))
	require.Equal(t, "+15551234567", ensTextValue(material, "phone", context.Background()))
	require.Equal(t, "active", ensTextValue(material, "soul.status", context.Background()))
	require.Equal(t, "next.lessersoul.eth", ensTextValue(material, "soul.successor", context.Background()))
	require.Len(t, ensTextValue(material, "description", context.Background()), 256)
	require.Empty(t, ensTextValue(ensGatewayMaterial{SuccessorAgentID: "successor"}, "soul.successor", context.Background()))
	require.Empty(t, ensTextValue(material, "unknown", context.Background()))

	var cache ensGatewayCache
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)

	_, _, hit := cache.get("agent", now)
	require.False(t, hit)

	cache.put("agent", ensGatewayMaterial{AgentID: "agent-1"}, true, now.Add(2*time.Second))
	got, ok, hit := cache.get("agent", now)
	require.True(t, hit)
	require.True(t, ok)
	require.Equal(t, "agent-1", got.AgentID)

	_, _, hit = cache.get("agent", now.Add(3*time.Second))
	require.False(t, hit)
}

func TestCacheENSGatewayMaterial_ConfiguredTTL(t *testing.T) {
	t.Parallel()

	var nilServer *Server
	nilServer.cacheENSGatewayMaterial("agent", ensGatewayMaterial{AgentID: "x"}, true, time.Now())
	(&Server{}).cacheENSGatewayMaterial("agent", ensGatewayMaterial{AgentID: "x"}, true, time.Now())

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	srv := &Server{
		cfg:      config.Config{ENSGatewaySignatureTTLSeconds: 5},
		ensCache: &ensGatewayCache{},
	}
	srv.cacheENSGatewayMaterial("agent", ensGatewayMaterial{AgentID: "cached"}, true, now)

	_, _, hit := srv.ensCache.get("agent", now.Add(4*time.Second))
	require.True(t, hit)

	_, _, hit = srv.ensCache.get("agent", now.Add(6*time.Second))
	require.False(t, hit)
}
