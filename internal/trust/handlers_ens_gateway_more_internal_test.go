package trust

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type ensGatewayTestDB struct {
	db       *ttmocks.MockExtendedDB
	qRes     *ttmocks.MockQuery
	qID      *ttmocks.MockQuery
	qChannel *ttmocks.MockQuery
}

const ensGatewaySuccessorName = "next.lessersoul.eth"

func newENSGatewayTestDB() ensGatewayTestDB {
	qRes := new(ttmocks.MockQuery)
	qID := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	db := newTestDBWithModelQueries(
		modelQueryPair{model: &models.SoulAgentENSResolution{}, query: qRes},
		modelQueryPair{model: &models.SoulAgentIdentity{}, query: qID},
		modelQueryPair{model: &models.SoulAgentChannel{}, query: qChannel},
	)
	return ensGatewayTestDB{db: db, qRes: qRes, qID: qID, qChannel: qChannel}
}

func TestHandleENSGatewayHealthAndHelpers(t *testing.T) {
	t.Parallel()

	s := &Server{}
	resp, err := s.handleENSGatewayHealth(&apptheory.Context{})
	if err != nil {
		t.Fatalf("handleENSGatewayHealth: %v", err)
	}
	if resp.Status != 200 || resp.Headers["cache-control"][0] != "no-store" {
		t.Fatalf("unexpected health response: %#v", resp)
	}

	if got := truncateUTF8("hello", 4); got != "hell" {
		t.Fatalf("unexpected ASCII truncation: %q", got)
	}
	if got := truncateUTF8("åäö", 2); got != "å" {
		t.Fatalf("unexpected UTF-8 truncation: %q", got)
	}

	material := ensGatewayMaterial{
		AgentID:          "0xabc",
		RegistrationURI:  "s3://bucket/reg.json",
		MCPEndpoint:      "https://example.com/mcp",
		ActivityPubURI:   "https://example.com/ap",
		Email:            "agent@example.com",
		Phone:            "+14155550123",
		Description:      strings.Repeat("x", 300),
		Status:           models.SoulAgentStatusActive,
		SuccessorAgentID: "0xdef",
		SuccessorENSName: ensGatewaySuccessorName,
		SuccessorENSOK:   true,
	}
	if got := ensTextValue(material, "soul.agentId", context.Background()); got != "0xabc" {
		t.Fatalf("unexpected agent id text value: %q", got)
	}
	if got := ensTextValue(material, "description", context.Background()); len(got) != 256 {
		t.Fatalf("expected truncated description, got len=%d", len(got))
	}
	if got := ensTextValue(material, "soul.successor", context.Background()); got != ensGatewaySuccessorName {
		t.Fatalf("unexpected successor text value: %q", got)
	}
	if got := ensTextValue(material, "unknown", context.Background()); got != "" {
		t.Fatalf("expected empty unknown key, got %q", got)
	}

	cache := &ensGatewayCache{}
	now := time.Unix(10, 0).UTC()
	cache.put("agent.eth", material, true, now.Add(time.Minute))
	if got, ok, hit := cache.get("agent.eth", now); !hit || !ok || got.AgentID != "0xabc" {
		t.Fatalf("unexpected cache hit: material=%#v ok=%v hit=%v", got, ok, hit)
	}
	if _, _, hit := cache.get("agent.eth", now.Add(2*time.Minute)); hit {
		t.Fatalf("expected expired cache miss")
	}
}

func TestLoadENSGatewayMaterialAndSuccessorLookup(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("11", 32)
	successorID := "0x" + strings.Repeat("22", 32)

	t.Run("success_and_cache_hit", func(t *testing.T) {
		t.Parallel()

		tdb := newENSGatewayTestDB()
		s := &Server{
			cfg:      config.Config{SoulEnabled: true, ENSGatewaySignatureTTLSeconds: 15},
			store:    store.New(tdb.db),
			ensCache: &ensGatewayCache{},
		}

		tdb.qRes.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
			*dest = models.SoulAgentENSResolution{
				ENSName:             "agent.lessersoul.eth",
				AgentID:             agentID,
				SoulRegistrationURI: "s3://bucket/reg.json",
				MCPEndpoint:         "https://example.com/mcp",
				ActivityPubURI:      "https://example.com/ap",
				Email:               "agent@example.com",
				Phone:               "+14155550123",
				Description:         "description",
				Status:              models.SoulAgentStatusActive,
			}
		}).Once()
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:          agentID,
				Wallet:           common.HexToAddress("0x1234").Hex(),
				LifecycleStatus:  models.SoulAgentStatusActive,
				SuccessorAgentID: successorID,
			}
		}).Once()
		tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
			*dest = models.SoulAgentChannel{AgentID: successorID, ChannelType: models.SoulChannelTypeENS, Identifier: ensGatewaySuccessorName}
		}).Once()

		got, ok, err := s.loadENSGatewayMaterial(context.Background(), "Agent.Lessersoul.ETH")
		if err != nil || !ok {
			t.Fatalf("loadENSGatewayMaterial err=%v ok=%v", err, ok)
		}
		if got.ENSName != "agent.lessersoul.eth" || got.SuccessorENSName != ensGatewaySuccessorName || !got.SuccessorENSOK {
			t.Fatalf("unexpected material: %#v", got)
		}

		cached, ok, err := s.loadENSGatewayMaterial(context.Background(), "agent.lessersoul.eth")
		if err != nil || !ok || cached.AgentID != agentID {
			t.Fatalf("expected cache hit, got material=%#v ok=%v err=%v", cached, ok, err)
		}
	})

	t.Run("not_found_and_blank_successor", func(t *testing.T) {
		t.Parallel()

		tdb := newENSGatewayTestDB()
		s := &Server{store: store.New(tdb.db), ensCache: &ensGatewayCache{}}
		tdb.qRes.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()

		if got, ok, err := s.loadENSGatewayMaterial(context.Background(), "missing.eth"); err != nil || ok || got != (ensGatewayMaterial{}) {
			t.Fatalf("unexpected missing result: material=%#v ok=%v err=%v", got, ok, err)
		}
		if name, ok, err := s.bestEffortSuccessorENSName(context.Background(), " "); err != nil || ok || name != "" {
			t.Fatalf("unexpected blank successor result: name=%q ok=%v err=%v", name, ok, err)
		}
	})
}

func newENSGatewayAnswerTestServer(addr common.Address) (*Server, common.Hash, [32]byte) {
	s := &Server{
		store:    store.New(newTestDBWithModelQueries()),
		ensCache: &ensGatewayCache{},
	}
	s.cacheENSGatewayMaterial("agent.lessersoul.eth", ensGatewayMaterial{
		ENSName:          "agent.lessersoul.eth",
		AgentID:          "0xabc",
		Wallet:           addr.Hex(),
		Status:           models.SoulAgentStatusActive,
		Email:            "agent@example.com",
		Phone:            "+14155550123",
		Description:      strings.Repeat("y", 300),
		SuccessorAgentID: "0xdef",
		SuccessorENSName: ensGatewaySuccessorName,
		SuccessorENSOK:   true,
	}, true, time.Now().UTC())

	node := ensNameHash("agent.lessersoul.eth")
	var nodeBytes [32]byte
	copy(nodeBytes[:], node[:])
	return s, node, nodeBytes
}

func TestAnswerENSQuery_AddrAndCoin(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0x0000000000000000000000000000000000001234")
	s, node, nodeBytes := newENSGatewayAnswerTestServer(addr)

	addrArgs, _ := ensAddrInputs.Pack(nodeBytes)
	addrOut, err := s.answerENSQuery(context.Background(), "agent.lessersoul.eth", node, append(append([]byte(nil), ensAddrSelector...), addrArgs...))
	if err != nil {
		t.Fatalf("addr query: %v", err)
	}
	decodedAddr, err := ensAddrOutputs.Unpack(addrOut)
	if err != nil {
		t.Fatalf("unpack addr response: %v", err)
	}
	addrValue, ok := decodedAddr[0].(common.Address)
	if !ok || addrValue != addr {
		t.Fatalf("unexpected addr response: decoded=%#v ok=%v", decodedAddr, ok)
	}

	coinArgs, _ := ensAddrCoinInputs.Pack(nodeBytes, big.NewInt(60))
	coinOut, err := s.answerENSQuery(context.Background(), "agent.lessersoul.eth", node, append(append([]byte(nil), ensAddrCoinSelector...), coinArgs...))
	if err != nil {
		t.Fatalf("coin query: %v", err)
	}
	decodedCoin, err := ensBytesOutputs.Unpack(coinOut)
	if err != nil {
		t.Fatalf("unpack coin response: %v", err)
	}
	coinValue, ok := decodedCoin[0].([]byte)
	if !ok || string(coinValue) != string(addr.Bytes()) {
		t.Fatalf("unexpected coin response: decoded=%#v ok=%v", decodedCoin, ok)
	}

	coinArgsUnsupported, _ := ensAddrCoinInputs.Pack(nodeBytes, big.NewInt(0))
	coinOutUnsupported, err := s.answerENSQuery(context.Background(), "agent.lessersoul.eth", node, append(append([]byte(nil), ensAddrCoinSelector...), coinArgsUnsupported...))
	if err != nil {
		t.Fatalf("unsupported coin query: %v", err)
	}
	decodedUnsupported, err := ensBytesOutputs.Unpack(coinOutUnsupported)
	if err != nil {
		t.Fatalf("unpack unsupported coin response: %v", err)
	}
	unsupportedValue, ok := decodedUnsupported[0].([]byte)
	if !ok || len(unsupportedValue) != 0 {
		t.Fatalf("unexpected unsupported coin response: decoded=%#v ok=%v", decodedUnsupported, ok)
	}
}

func TestAnswerENSQuery_TextAndContenthash(t *testing.T) {
	t.Parallel()

	s, node, nodeBytes := newENSGatewayAnswerTestServer(common.HexToAddress("0x0000000000000000000000000000000000001234"))

	textArgs, _ := ensTextInputs.Pack(nodeBytes, "soul.successor")
	textOut, err := s.answerENSQuery(context.Background(), "agent.lessersoul.eth", node, append(append([]byte(nil), ensTextSelector...), textArgs...))
	if err != nil {
		t.Fatalf("text query: %v", err)
	}
	decodedText, err := ensStringOutputs.Unpack(textOut)
	if err != nil {
		t.Fatalf("unpack text response: %v", err)
	}
	textValue, ok := decodedText[0].(string)
	if !ok || textValue != ensGatewaySuccessorName {
		t.Fatalf("unexpected text response: decoded=%#v ok=%v", decodedText, ok)
	}

	contentArgs, _ := ensContenthashInputs.Pack(nodeBytes)
	contentOut, err := s.answerENSQuery(context.Background(), "agent.lessersoul.eth", node, append(append([]byte(nil), ensContenthashSelector...), contentArgs...))
	if err != nil {
		t.Fatalf("contenthash query: %v", err)
	}
	decodedContent, err := ensBytesOutputs.Unpack(contentOut)
	if err != nil {
		t.Fatalf("unpack contenthash response: %v", err)
	}
	contentValue, ok := decodedContent[0].([]byte)
	if !ok || len(contentValue) != 0 {
		t.Fatalf("unexpected contenthash response: decoded=%#v ok=%v", decodedContent, ok)
	}
}

func TestAnswerENSQuery_Validation(t *testing.T) {
	t.Parallel()

	s, node, nodeBytes := newENSGatewayAnswerTestServer(common.HexToAddress("0x0000000000000000000000000000000000001234"))
	addrArgs, _ := ensAddrInputs.Pack(nodeBytes)
	if _, err := s.answerENSQuery(context.Background(), "agent.lessersoul.eth", node, []byte{0x01, 0x02, 0x03}); err == nil {
		t.Fatalf("expected invalid inner resolver data error")
	}

	badNodeArgs, _ := ensAddrInputs.Pack([32]byte{})
	if _, err := s.answerENSQuery(context.Background(), "agent.lessersoul.eth", node, append(append([]byte(nil), ensAddrSelector...), badNodeArgs...)); err == nil {
		t.Fatalf("expected node mismatch error")
	}
	if _, err := s.answerENSQuery(context.Background(), "agent.lessersoul.eth", node, append([]byte{0xff, 0xee, 0xdd, 0xcc}, addrArgs...)); err == nil {
		t.Fatalf("expected unsupported query error")
	}
}

func TestParseENSGatewayRequestAndResolveHandler(t *testing.T) {
	t.Parallel()

	node := ensNameHash("agent.lessersoul.eth")
	var nodeBytes [32]byte
	copy(nodeBytes[:], node[:])

	innerArgs, _ := ensAddrInputs.Pack(nodeBytes)
	innerData := append(append([]byte(nil), ensAddrSelector...), innerArgs...)

	callData, encodedName, parsedInner, err := parseENSGatewayRequest("agent.lessersoul.eth", hexutil.Encode(innerData))
	if err != nil {
		t.Fatalf("parse compatibility request: %v", err)
	}
	if len(callData) == 0 || len(encodedName) == 0 || len(parsedInner) == 0 {
		t.Fatalf("unexpected parsed compatibility request")
	}

	resolvePacked, _ := ensResolveInputs.Pack(encodedName, innerData)
	resolveCallData := append(append([]byte(nil), ensResolveSelector...), resolvePacked...)
	if _, _, _, parseErr := parseENSGatewayRequest("", hexutil.Encode(resolveCallData)); parseErr != nil {
		t.Fatalf("parse calldata request: %v", parseErr)
	}
	if _, _, _, parseErr := parseENSGatewayRequest("", "0x1234"); parseErr == nil {
		t.Fatalf("expected name-required error")
	}
	if _, _, _, parseErr := parseENSGatewayRequest("bad..name", hexutil.Encode(innerData)); parseErr == nil {
		t.Fatalf("expected invalid name error")
	}

	key := mustTrustTestKey(t)
	privateKeyHex := common.Bytes2Hex(crypto.FromECDSA(key))
	resolverAddr := "0x000000000000000000000000000000000000beef"

	s := &Server{
		cfg: config.Config{
			SoulEnabled:                   true,
			ENSGatewayResolverAddress:     resolverAddr,
			ENSGatewaySigningPrivateKey:   privateKeyHex,
			ENSGatewaySignatureTTLSeconds: 60,
		},
		store:    store.New(newTestDBWithModelQueries()),
		ensCache: &ensGatewayCache{},
	}
	s.cacheENSGatewayMaterial("agent.lessersoul.eth", ensGatewayMaterial{
		ENSName: "agent.lessersoul.eth",
		Wallet:  common.HexToAddress("0x0000000000000000000000000000000000001234").Hex(),
	}, true, time.Now().UTC())

	resp, err := s.handleENSGatewayResolve(&apptheory.Context{
		Request: apptheory.Request{Query: map[string][]string{
			"sender": {resolverAddr},
			"name":   {"agent.lessersoul.eth"},
			"data":   {hexutil.Encode(innerData)},
		}},
	})
	if err != nil {
		t.Fatalf("handleENSGatewayResolve: %v", err)
	}
	if resp.Status != 200 || !strings.HasPrefix(resp.Headers["cache-control"][0], "public, max-age=") {
		t.Fatalf("unexpected resolve response: %#v", resp)
	}

	var out ensGatewayResolveJSON
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(out.Data, "0x") {
		t.Fatalf("expected hex-encoded data, got %q", out.Data)
	}

	if _, err := s.handleENSGatewayResolve(&apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"sender": {"not-hex"}, "data": {hexutil.Encode(innerData)}}}}); err == nil {
		t.Fatalf("expected invalid sender error")
	}
	if _, err := s.handleENSGatewayResolve(&apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"sender": {"0x000000000000000000000000000000000000cafe"}, "data": {hexutil.Encode(innerData)}}}}); err == nil {
		t.Fatalf("expected unsupported sender error")
	}
}
