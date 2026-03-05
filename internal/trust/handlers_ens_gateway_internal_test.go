package trust

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleENSGatewayResolve_Addr_SignsAndEncodes(t *testing.T) {
	t.Parallel()

	// Use a local signing key in unit tests.
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	privateKeyHex := hex.EncodeToString(crypto.FromECDSA(key))
	signerAddr := crypto.PubkeyToAddress(key.PublicKey)

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
		ENSGatewaySigningPrivateKey:   privateKeyHex,
		ENSGatewaySignatureTTLSeconds: 300,
	}, store.New(db))

	encodedName, err := encodeDNSName(ensName)
	require.NoError(t, err)
	node := ensNameHash(ensName)

	innerArgs, err := ensAddrInputs.Pack(node)
	require.NoError(t, err)
	innerData := append(append([]byte(nil), ensAddrSelector...), innerArgs...)

	callArgs, err := ensResolveInputs.Pack(encodedName, innerData)
	require.NoError(t, err)
	callData := append(append([]byte(nil), ensResolveSelector...), callArgs...)

	target := common.HexToAddress("0x0000000000000000000000000000000000001234")

	ctx := &apptheory.Context{
		Request: apptheory.Request{
			Query: map[string][]string{
				"sender": {target.Hex()},
				"data":   {"0x" + hex.EncodeToString(callData)},
			},
		},
	}

	resp, err := srv.handleENSGatewayResolve(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)

	var out ensGatewayResolveJSON
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.NotEmpty(t, out.Data)

	responseBytes, err := hexutil.Decode(out.Data)
	require.NoError(t, err)

	decoded, err := ensGatewayResponseABI.Unpack(responseBytes)
	require.NoError(t, err)
	require.Len(t, decoded, 3)

	resultBytes, ok := decoded[0].([]byte)
	require.True(t, ok)

	expires, ok := decoded[1].(uint64)
	require.True(t, ok)

	sigCompact, ok := decoded[2].([]byte)
	require.True(t, ok)
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

	// Ensure the embedded resolver result decodes to the wallet address.
	resolved, err := ensAddrOutputs.Unpack(resultBytes)
	require.NoError(t, err)
	require.Len(t, resolved, 1)

	gotAddr, ok := resolved[0].(common.Address)
	require.True(t, ok)
	require.Equal(t, common.HexToAddress(wallet), gotAddr)
}

func TestHandleENSGatewayResolve_Text_StatusResolvesDeterministically(t *testing.T) {
	t.Parallel()

	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	privateKeyHex := hex.EncodeToString(crypto.FromECDSA(key))

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
		ENSGatewaySigningPrivateKey: privateKeyHex,
	}, store.New(db))

	encodedName, err := encodeDNSName(ensName)
	require.NoError(t, err)
	node := ensNameHash(ensName)

	innerArgs, err := ensTextInputs.Pack(node, "soul.status")
	require.NoError(t, err)
	innerData := append(append([]byte(nil), ensTextSelector...), innerArgs...)

	callArgs, err := ensResolveInputs.Pack(encodedName, innerData)
	require.NoError(t, err)
	callData := append(append([]byte(nil), ensResolveSelector...), callArgs...)

	target := common.HexToAddress("0x0000000000000000000000000000000000001234")

	ctx := &apptheory.Context{
		Request: apptheory.Request{
			Query: map[string][]string{
				"sender": {target.Hex()},
				"data":   {"0x" + hex.EncodeToString(callData)},
			},
		},
	}

	resp, err := srv.handleENSGatewayResolve(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out ensGatewayResolveJSON
	require.NoError(t, json.Unmarshal(resp.Body, &out))

	responseBytes, err := hexutil.Decode(out.Data)
	require.NoError(t, err)
	decoded, err := ensGatewayResponseABI.Unpack(responseBytes)
	require.NoError(t, err)

	resultBytes := testutil.RequireType[[]byte](t, decoded[0])
	resolved, err := ensStringOutputs.Unpack(resultBytes)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	require.Equal(t, "archived", resolved[0])
}
