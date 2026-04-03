package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type fakeSoulPublicPacks struct {
	body        []byte
	contentType string
	etag        string
	err         error
}

func (f *fakeSoulPublicPacks) PutObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error {
	return nil
}

func (f *fakeSoulPublicPacks) GetObject(ctx context.Context, key string, maxBytes int64) ([]byte, string, string, error) {
	if f.err != nil {
		return nil, "", "", f.err
	}
	return f.body, f.contentType, f.etag, nil
}

type fakeSoulPublicEthClient struct {
	callContract func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error)
	filterLogs   func(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
}

func (f *fakeSoulPublicEthClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if f != nil && f.callContract != nil {
		return f.callContract(ctx, msg)
	}
	return nil, errors.New("unexpected CallContract")
}

func (f *fakeSoulPublicEthClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	if f != nil && f.filterLogs != nil {
		return f.filterLogs(ctx, q)
	}
	return nil, errors.New("unexpected FilterLogs")
}

func (f *fakeSoulPublicEthClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return nil, ethereum.NotFound
}

func (f *fakeSoulPublicEthClient) Close() {}

type soulPublicTestDB struct {
	db        *ttmocks.MockExtendedDB
	qID       *ttmocks.MockQuery
	qRep      *ttmocks.MockQuery
	qVal      *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qInstance *ttmocks.MockQuery
	qDomIdx   *ttmocks.MockQuery
	qCapIdx   *ttmocks.MockQuery
	qBoundIdx *ttmocks.MockQuery
	qChannel  *ttmocks.MockQuery
	qPrefs    *ttmocks.MockQuery
	qEmailIdx *ttmocks.MockQuery
	qPhoneIdx *ttmocks.MockQuery
	qENS      *ttmocks.MockQuery
	qChanIdx  *ttmocks.MockQuery
}

func newSoulPublicTestDB() soulPublicTestDB {
	db := ttmocks.NewMockExtendedDB()
	qID := new(ttmocks.MockQuery)
	qRep := new(ttmocks.MockQuery)
	qVal := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)
	qDomIdx := new(ttmocks.MockQuery)
	qCapIdx := new(ttmocks.MockQuery)
	qBoundIdx := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qPrefs := new(ttmocks.MockQuery)
	qEmailIdx := new(ttmocks.MockQuery)
	qPhoneIdx := new(ttmocks.MockQuery)
	qENS := new(ttmocks.MockQuery)
	qChanIdx := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qRep).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentValidationRecord")).Return(qVal).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulDomainAgentIndex")).Return(qDomIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(qCapIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(qBoundIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(qPrefs).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(qEmailIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(qPhoneIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(qENS).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(qChanIdx).Maybe()

	for _, q := range []*ttmocks.MockQuery{qID, qRep, qVal, qDomain, qInstance, qDomIdx, qCapIdx, qBoundIdx, qChannel, qPrefs, qEmailIdx, qPhoneIdx, qENS, qChanIdx} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("Cursor", mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
	}
	return soulPublicTestDB{
		db:        db,
		qID:       qID,
		qRep:      qRep,
		qVal:      qVal,
		qDomain:   qDomain,
		qInstance: qInstance,
		qDomIdx:   qDomIdx,
		qCapIdx:   qCapIdx,
		qBoundIdx: qBoundIdx,
		qChannel:  qChannel,
		qPrefs:    qPrefs,
		qEmailIdx: qEmailIdx,
		qPhoneIdx: qPhoneIdx,
		qENS:      qENS,
		qChanIdx:  qChanIdx,
	}
}

func TestEnvIntPositiveFromString(t *testing.T) {
	t.Parallel()

	if got := envIntPositiveFromString("", 50); got != 50 {
		t.Fatalf("unexpected default: %d", got)
	}
	if got := envIntPositiveFromString("nope", 50); got != 50 {
		t.Fatalf("unexpected invalid: %d", got)
	}
	if got := envIntPositiveFromString("-1", 50); got != 50 {
		t.Fatalf("unexpected negative: %d", got)
	}
	if got := envIntPositiveFromString("0", 50); got != 50 {
		t.Fatalf("unexpected zero: %d", got)
	}
	if got := envIntPositiveFromString(" 15 ", 50); got != 15 {
		t.Fatalf("unexpected positive: %d", got)
	}
	if got := envIntPositiveFromString("999999999999999999999999", 50); got != 50 {
		t.Fatalf("unexpected overflow fallback: %d", got)
	}
}

func TestEnvIntPositiveClampedFromString(t *testing.T) {
	t.Parallel()

	if got := envIntPositiveClampedFromString("500", 50, 200); got != 200 {
		t.Fatalf("unexpected clamped value: %d", got)
	}
	if got := envIntPositiveClampedFromString("25", 50, 200); got != 25 {
		t.Fatalf("unexpected unclamped value: %d", got)
	}
}

func TestParseSoulSearchQuery(t *testing.T) {
	t.Parallel()

	if d, local, err := parseSoulSearchQuery(""); err != nil || d != "" || local != "" {
		t.Fatalf("unexpected empty: d=%q local=%q err=%v", d, local, err)
	}
	if d, local, err := parseSoulSearchQuery(testDomainExampleCom); err != nil || d != testDomainExampleCom || local != "" {
		t.Fatalf("unexpected domain: d=%q local=%q err=%v", d, local, err)
	}
	if d, local, err := parseSoulSearchQuery(testDomainExampleCom + "/agent-alice"); err != nil || d != testDomainExampleCom || local == "" {
		t.Fatalf("unexpected domain/local: d=%q local=%q err=%v", d, local, err)
	}
	if _, _, err := parseSoulSearchQuery("agent-only"); err == nil {
		t.Fatalf("expected local-only query to fail closed")
	}
}

func TestHandleSoulPublicSearch_CurrentInstanceBareLocalQuery(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("ab", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true, Stage: "lab"}}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "simulacrum.greater.website",
			InstanceSlug:       "simulacrum",
			Status:             models.DomainStatusVerified,
			Type:               models.DomainTypePrimary,
			VerificationMethod: "managed",
		}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "simulacrum", HostedBaseDomain: "simulacrum.greater.website"}
	}).Once()
	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: agentID, Domain: "simulacrum.greater.website", LocalID: "medic"}}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{
		Headers: map[string][]string{"host": {"dev.simulacrum.greater.website"}},
		Query:   map[string][]string{"q": {"medic"}},
	}}
	assertSoulPublicSearchResponse(t, s, ctx, agentID)
}

func TestHandleSoulPublicSearch_DomainParamManagedAliasCanonicalizes(t *testing.T) {
	t.Parallel()

	agentID, s := newManagedAliasSoulSearchTestServer(t, "ac")
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"domain": {"dev.simulacrum.greater.website"},
		"q":      {"medic"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentID)
}

func TestHandleSoulPublicSearch_QDomainAndDomainParamAliasCanonicalizeTogether(t *testing.T) {
	t.Parallel()

	agentID, s := newManagedAliasSoulSearchTestServer(t, "ad")
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"domain": {"dev.simulacrum.greater.website"},
		"q":      {"simulacrum.greater.website/medic"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentID)
}

func TestHandleSoulPublicSearch_QualifiedQuerySkipsCurrentHostResolution(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("ae", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true, Stage: "lab"}}

	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: agentID, Domain: "example.com", LocalID: "medic"}}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{
		Headers: map[string][]string{"host": {"dev.simulacrum.greater.website"}},
		Query:   map[string][]string{"q": {"example.com/medic"}},
	}}
	assertSoulPublicSearchResponse(t, s, ctx, agentID)
}

func newManagedAliasSoulSearchTestServer(t *testing.T, agentHexByte string) (string, *Server) {
	t.Helper()

	agentID := "0x" + strings.Repeat(agentHexByte, 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true, Stage: "lab"}}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "simulacrum.greater.website",
			InstanceSlug:       "simulacrum",
			Status:             models.DomainStatusVerified,
			Type:               models.DomainTypePrimary,
			VerificationMethod: "managed",
		}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "simulacrum", HostedBaseDomain: "simulacrum.greater.website"}
	}).Once()
	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: agentID, Domain: "simulacrum.greater.website", LocalID: "medic"}}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	return agentID, s
}

func TestSetSoulPublicHeaders(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{SoulEnabled: true}}
	s.setSoulPublicHeaders(nil, nil, "")
	resp := &apptheory.Response{}
	s.setSoulPublicHeaders(nil, resp, "")
	if resp.Headers == nil || len(resp.Headers["cache-control"]) != 1 || resp.Headers["cache-control"][0] != "no-store" {
		t.Fatalf("unexpected cache header: %#v", resp.Headers)
	}
	if len(resp.Headers["access-control-allow-origin"]) != 1 || resp.Headers["access-control-allow-origin"][0] != "*" {
		t.Fatalf("unexpected allow-origin header: %#v", resp.Headers)
	}
}

func TestHandleSoulPublicGetAgent_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{SoulEnabled: true},
	}

	agentID := "0x" + strings.Repeat("11", 32)

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Composite: 0.1, UpdatedAt: time.Now().UTC()}
	}).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgent(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	if resp.Headers == nil || len(resp.Headers["cache-control"]) != 1 || resp.Headers["cache-control"][0] != boundaryTestCacheControl {
		t.Fatalf("unexpected headers: %#v", resp.Headers)
	}

	var out soulPublicAgentResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Version != "1" || out.Agent.AgentID != agentID || out.Reputation == nil || out.Reputation.AgentID != agentID {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandleSoulPublicGetAgent_IncludesENSAndAvatarStyles(t *testing.T) {
	t.Parallel()

	s, expected := newSoulPublicAvatarTestServer(t)

	ctx := &apptheory.Context{Params: map[string]string{"agentId": expected.agentID}}
	resp, err := s.handleSoulPublicGetAgent(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var out soulPublicAgentResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertSoulPublicAvatarResponse(t, out, expected)
}

type soulPublicAvatarExpectations struct {
	agentID          string
	ensName          string
	registryAddr     common.Address
	renderers        [3]common.Address
	images           [3]string
	currentStyleID   int
	currentStyleName string
}

type soulPublicAvatarRPCFixture struct {
	tokenURI         string
	tokenURICall     []byte
	styleNameCall    []byte
	renderAvatarCall []byte
	styleNames       [3]string
	styleSVGs        [3]string
}

func newSoulPublicAvatarTestServer(t *testing.T) (*Server, soulPublicAvatarExpectations) {
	t.Helper()

	tdb := newSoulPublicTestDB()
	expected := soulPublicAvatarExpectations{
		agentID:          "0x" + strings.Repeat("12", 32),
		ensName:          "agent-bot.lessersoul.eth",
		registryAddr:     common.HexToAddress("0x0000000000000000000000000000000000000abc"),
		renderers:        [3]common.Address{common.HexToAddress("0x0000000000000000000000000000000000000100"), common.HexToAddress("0x0000000000000000000000000000000000000101"), common.HexToAddress("0x0000000000000000000000000000000000000102")},
		images:           [3]string{encodeSoulPublicAvatarSVG("<svg>blob</svg>"), encodeSoulPublicAvatarSVG("<svg>geometry</svg>"), encodeSoulPublicAvatarSVG("<svg>sigil</svg>")},
		currentStyleID:   1,
		currentStyleName: "Sacred Geometry",
	}

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulRPCURL:                  "http://rpc",
			SoulRegistryContractAddress: expected.registryAddr.Hex(),
		},
	}

	expectSoulPublicAvatarIdentity(t, tdb, expected.agentID)
	expectSoulPublicAvatarENSChannel(t, tdb, expected.agentID, expected.ensName)
	configureSoulPublicAvatarRPC(t, s, expected)

	return s, expected
}

func expectSoulPublicAvatarIdentity(t *testing.T, tdb soulPublicTestDB, agentID string) {
	t.Helper()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:                agentID,
			Domain:                 "example.com",
			LocalID:                "agent-bot",
			Wallet:                 "0x00000000000000000000000000000000000000aa",
			TokenID:                agentID,
			MetaURI:                "https://example.com/metadata.json",
			PrincipalAddress:       "0x00000000000000000000000000000000000000bb",
			PrincipalSignature:     "0xdeadbeef",
			PrincipalDeclaration:   "I accept responsibility for this agent's behavior.",
			PrincipalDeclaredAt:    "2026-04-01T12:00:00Z",
			SelfDescriptionVersion: 3,
			Status:                 models.SoulAgentStatusActive,
			LifecycleStatus:        models.SoulAgentStatusActive,
			MintTxHash:             "0x" + strings.Repeat("ab", 32),
			MintedAt:               time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
			UpdatedAt:              time.Date(2026, 4, 2, 8, 30, 0, 0, time.UTC),
		}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()
}

func expectSoulPublicAvatarENSChannel(t *testing.T, tdb soulPublicTestDB, agentID string, ensName string) {
	t.Helper()

	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     agentID,
			ChannelType: models.SoulChannelTypeENS,
			Identifier:  ensName,
			Status:      models.SoulChannelStatusActive,
		}
	}).Once()
}

func configureSoulPublicAvatarRPC(t *testing.T, s *Server, expected soulPublicAvatarExpectations) {
	t.Helper()

	fixture := newSoulPublicAvatarRPCFixture(t, expected)

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeSoulPublicEthClient{
			callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
				if response, ok := soulPublicAvatarCallResult(t, msg, expected, fixture); ok {
					return response, nil
				}
				t.Fatalf("unexpected CallContract to=%v data=%x", msg.To, msg.Data)
				return nil, errors.New("unexpected call")
			},
			filterLogs: func(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
				return []types.Log{
					rendererUpdatedLog(0, expected.renderers[0]),
					rendererUpdatedLog(1, expected.renderers[1]),
					rendererUpdatedLog(2, expected.renderers[2]),
				}, nil
			},
		}, nil
	}
}

func newSoulPublicAvatarRPCFixture(t *testing.T, expected soulPublicAvatarExpectations) soulPublicAvatarRPCFixture {
	t.Helper()

	tokenID, ok := new(big.Int).SetString(strings.TrimPrefix(expected.agentID, "0x"), 16)
	if !ok {
		t.Fatalf("failed to parse token id")
	}

	styleNameCall, err := soul.EncodeRendererStyleNameCall()
	if err != nil {
		t.Fatalf("encode styleName: %v", err)
	}
	renderAvatarCall, err := soul.EncodeRendererRenderAvatarCall(tokenID)
	if err != nil {
		t.Fatalf("encode renderAvatar: %v", err)
	}
	tokenURICall, err := soul.EncodeTokenURICall(tokenID)
	if err != nil {
		t.Fatalf("encode tokenURI: %v", err)
	}

	tokenMetadataJSON, err := json.Marshal(soulAvatarTokenMetadata{
		Image: expected.images[expected.currentStyleID],
		Attributes: []soulAvatarTokenMetadataAttribute{
			{TraitType: "Style", Value: expected.currentStyleName},
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	return soulPublicAvatarRPCFixture{
		tokenURI:         "data:application/json;base64," + base64.StdEncoding.EncodeToString(tokenMetadataJSON),
		tokenURICall:     tokenURICall,
		styleNameCall:    styleNameCall,
		renderAvatarCall: renderAvatarCall,
		styleNames:       [3]string{"Ethereal Blob", "Sacred Geometry", "Sigil"},
		styleSVGs:        [3]string{"<svg>blob</svg>", "<svg>geometry</svg>", "<svg>sigil</svg>"},
	}
}

func soulPublicAvatarCallResult(t *testing.T, msg ethereum.CallMsg, expected soulPublicAvatarExpectations, fixture soulPublicAvatarRPCFixture) ([]byte, bool) {
	t.Helper()

	if msg.To != nil && *msg.To == expected.registryAddr && bytes.Equal(msg.Data, fixture.tokenURICall) {
		return packSingleStringResult(t, fixture.tokenURI), true
	}
	return soulPublicAvatarRendererCallResult(t, msg, expected, fixture)
}

func soulPublicAvatarRendererCallResult(t *testing.T, msg ethereum.CallMsg, expected soulPublicAvatarExpectations, fixture soulPublicAvatarRPCFixture) ([]byte, bool) {
	t.Helper()

	for idx, renderer := range expected.renderers {
		if msg.To == nil || *msg.To != renderer {
			continue
		}
		if bytes.Equal(msg.Data, fixture.styleNameCall) {
			return packSingleStringResult(t, fixture.styleNames[idx]), true
		}
		if bytes.Equal(msg.Data, fixture.renderAvatarCall) {
			return packSingleStringResult(t, fixture.styleSVGs[idx]), true
		}
	}
	return nil, false
}

func assertSoulPublicAvatarResponse(t *testing.T, out soulPublicAgentResponse, expected soulPublicAvatarExpectations) {
	t.Helper()

	if out.Agent.ENSName != expected.ensName {
		t.Fatalf("expected ens_name, got %#v", out.Agent)
	}
	if out.Agent.Avatar == nil {
		t.Fatalf("expected avatar payload, got %#v", out.Agent)
	}
	assertSoulPublicAvatarSelection(t, out.Agent.Avatar, expected)
	assertSoulPublicAvatarStyles(t, out.Agent.Avatar, expected)
	assertSoulPublicAgentIdentityDetails(t, out)
}

func assertSoulPublicAvatarSelection(t *testing.T, avatar *soulPublicAvatarView, expected soulPublicAvatarExpectations) {
	t.Helper()

	if avatar.CurrentStyleID == nil || *avatar.CurrentStyleID != expected.currentStyleID {
		t.Fatalf("expected current style id %d, got %#v", expected.currentStyleID, avatar)
	}
	if avatar.CurrentStyleName != expected.currentStyleName {
		t.Fatalf("expected current style name, got %#v", avatar)
	}
	if avatar.CurrentRendererAddress != strings.ToLower(expected.renderers[expected.currentStyleID].Hex()) {
		t.Fatalf("expected current renderer %s, got %#v", expected.renderers[expected.currentStyleID].Hex(), avatar)
	}
	if avatar.Image != expected.images[expected.currentStyleID] {
		t.Fatalf("expected current image %q, got %#v", expected.images[expected.currentStyleID], avatar)
	}
	if !avatar.Styles[expected.currentStyleID].Selected {
		t.Fatalf("expected selected style, got %#v", avatar.Styles)
	}
}

func assertSoulPublicAvatarStyles(t *testing.T, avatar *soulPublicAvatarView, expected soulPublicAvatarExpectations) {
	t.Helper()

	if len(avatar.Styles) != len(expected.images) {
		t.Fatalf("expected three styles, got %#v", avatar)
	}
	for idx, image := range expected.images {
		if avatar.Styles[idx].Image != image {
			t.Fatalf("unexpected style images: %#v", avatar.Styles)
		}
	}
}

func assertSoulPublicAgentIdentityDetails(t *testing.T, out soulPublicAgentResponse) {
	t.Helper()

	if out.Agent.PrincipalSignature == "" || out.Agent.PrincipalDeclaration == "" || out.Agent.PrincipalDeclaredAt == "" || out.Agent.MetaURI == "" {
		t.Fatalf("expected richer identity details, got %#v", out.Agent)
	}
}

func encodeSoulPublicAvatarSVG(svg string) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

func TestHandleSoulPublicGetAgent_AvatarLookupFailureIsNonFatal(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	agentID := "0x" + strings.Repeat("34", 32)
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulRPCURL:                  "http://rpc",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000abc",
		},
	}

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-bot",
			Wallet:          "0x00000000000000000000000000000000000000aa",
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
		}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeSoulPublicEthClient{
			filterLogs: func(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
				return nil, errors.New("rpc boom")
			},
		}, nil
	}

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgent(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var out soulPublicAgentResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Agent.Avatar != nil {
		t.Fatalf("expected avatar enrichment to fail closed, got %#v", out.Agent.Avatar)
	}
	if out.Agent.AgentID != agentID {
		t.Fatalf("expected base identity to survive, got %#v", out.Agent)
	}
}

func packSingleStringResult(t *testing.T, value string) []byte {
	t.Helper()

	stringType, err := abi.NewType("string", "", nil)
	if err != nil {
		t.Fatalf("new string type: %v", err)
	}
	args := abi.Arguments{{Type: stringType}}
	out, err := args.Pack(value)
	if err != nil {
		t.Fatalf("pack string result: %v", err)
	}
	return out
}

func rendererUpdatedLog(styleID uint8, renderer common.Address) types.Log {
	return types.Log{
		Topics: []common.Hash{
			soulRendererUpdatedTopic,
			common.BigToHash(big.NewInt(int64(styleID))),
		},
		Data: common.LeftPadBytes(renderer.Bytes(), 32),
	}
}

func TestHandleSoulPublicGetReputation_NotFoundAndSuccess(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("22", 32)

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
		if _, err := s.handleSoulPublicGetReputation(ctx); err == nil {
			t.Fatalf("expected not_found error")
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
			*dest = models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Composite: 0.2, UpdatedAt: time.Now().UTC()}
		}).Once()

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
		resp, err := s.handleSoulPublicGetReputation(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}
	})
}

func TestHandleSoulPublicGetRegistration_SuccessAndNoSuchKey(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("33", 32)

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		packs := &fakeSoulPublicPacks{body: []byte(`{"ok":true}`), contentType: "", etag: "  etag "}
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
		resp, err := s.handleSoulPublicGetRegistration(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}
		if got := strings.TrimSpace(resp.Headers["content-type"][0]); got != "application/json" {
			t.Fatalf("expected default content-type, got %q", got)
		}
		if got := strings.TrimSpace(resp.Headers["etag"][0]); got != "etag" {
			t.Fatalf("unexpected etag: %q", got)
		}
		if got := strings.TrimSpace(resp.Headers["cache-control"][0]); got != "public, max-age=300" {
			t.Fatalf("unexpected cache-control: %q", got)
		}
	})

	t.Run("no_such_key", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		packs := &fakeSoulPublicPacks{err: &s3types.NoSuchKey{}}
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
		if _, err := s.handleSoulPublicGetRegistration(ctx); err == nil {
			t.Fatalf("expected not_found")
		}
	})
}

func TestHandleSoulPublicGetValidations_PaginatesAndFiltersNilItems(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	agentID := "0x" + strings.Repeat("44", 32)

	tdb.qVal.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{NextCursor: " c2 ", HasMore: true}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentValidationRecord](t, args, 0)
		*dest = []*models.SoulAgentValidationRecord{
			nil,
			{AgentID: agentID, ChallengeID: "c1", ChallengeType: "identity_verify", Result: "pass", EvaluatedAt: time.Now().UTC()},
		}
	}).Once()

	ctx := &apptheory.Context{
		Params:  map[string]string{"agentId": agentID},
		Request: apptheory.Request{Query: map[string][]string{"cursor": {"c1"}, "limit": {"500"}}},
	}
	resp, err := s.handleSoulPublicGetValidations(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulPublicValidationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Validations) != 1 || !out.HasMore || out.NextCursor != "c2" {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandleSoulPublicSearch_CapabilityBranch(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qCapIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulCapabilityAgentIndex](t, args, 0)
		*dest = []*models.SoulCapabilityAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "a"},
			{AgentID: agentB, Domain: "example.com", LocalID: "b"},
		}
	}).Once()
	mockSoulPublicIdentityStatuses(t, &tdb, agentA, agentB)

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"capability": {"social"},
		"q":          {"example.com"},
		"cursor":     {"c1"},
		"limit":      {"10"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentA)
}

func TestHandleSoulPublicSearch_DomainBranch(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: agentA, Domain: "example.com", LocalID: "a"}}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentA, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"q": {"example.com/agent-a"}}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
}

func TestHandleSoulPublicSearch_DomainParamAndLocalQuery(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: agentA, Domain: "example.com", LocalID: "agent-alice"}}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentA, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"domain": {"example.com"},
		"q":      {"agent-a"},
	}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
}

func TestHandleSoulPublicSearch_MissingQuery(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{}}}
	if _, err := s.handleSoulPublicSearch(ctx); err == nil {
		t.Fatalf("expected bad_request")
	}
}

func TestHandleSoulPublicSearch_InvalidCapability(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"capability": {"x x"}}}}
	if _, err := s.handleSoulPublicSearch(ctx); err == nil {
		t.Fatalf("expected bad_request")
	}
}

func TestHandleSoulPublicSearch_InvalidPrincipal(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":         {"example.com"},
		"principal": {"not-a-wallet"},
	}}}
	if _, err := s.handleSoulPublicSearch(ctx); err == nil {
		t.Fatalf("expected bad_request")
	}
}

func TestHandleSoulPublicGetRegistration_RegistrationErrorPassthrough(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	packs := &fakeSoulPublicPacks{err: errors.New("boom")}
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}
	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentA}}
	if _, err := s.handleSoulPublicGetRegistration(ctx); err == nil {
		t.Fatalf("expected internal error")
	}
}

func TestHandleSoulPublicSearch_ClaimLevelRequiresCapability(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":          {"example.com"},
		"claimLevel": {"challenge-passed"},
	}}}
	if _, err := s.handleSoulPublicSearch(ctx); err == nil {
		t.Fatalf("expected bad_request")
	}
}

func TestHandleSoulPublicSearch_ClaimLevelFiltersCapabilityResults(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qCapIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulCapabilityAgentIndex](t, args, 0)
		*dest = []*models.SoulCapabilityAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "a", ClaimLevel: "self-declared"},
			{AgentID: agentB, Domain: "example.com", LocalID: "b", ClaimLevel: "challenge-passed"},
		}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{Status: models.SoulAgentStatusActive}
	}).Maybe()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"capability": {"social"},
		"q":          {"example.com"},
		"claimLevel": {"challenge-passed"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentB)
}

func TestHandleSoulPublicSearch_PrincipalFiltersDomainResults(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	principal := "0x00000000000000000000000000000000000000aa"
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "agent-a"},
			{AgentID: agentB, Domain: "example.com", LocalID: "agent-b"},
		}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:          agentA,
			Domain:           "example.com",
			LocalID:          "agent-a",
			PrincipalAddress: principal,
			Status:           models.SoulAgentStatusActive,
		}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:          agentB,
			Domain:           "example.com",
			LocalID:          "agent-b",
			PrincipalAddress: "0x00000000000000000000000000000000000000bb",
			Status:           models.SoulAgentStatusActive,
		}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":         {"example.com"},
		"principal": {principal},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentA)
}

func TestHandleSoulPublicSearch_BoundaryFiltersDomainResults(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qBoundIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulBoundaryKeywordAgentIndex](t, args, 0)
		*dest = []*models.SoulBoundaryKeywordAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "a", Keyword: "finance"},
			{AgentID: agentB, Domain: "example.com", LocalID: "b", Keyword: "finance"},
		}
	}).Once()
	mockSoulPublicIdentityStatuses(t, &tdb, agentA, agentB)

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":        {"example.com"},
		"boundary": {"finance"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentA)
}

func TestHandleSoulPublicSearch_ClaimLevelBoundaryStatusCombination(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qCapIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulCapabilityAgentIndex](t, args, 0)
		*dest = []*models.SoulCapabilityAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "a", ClaimLevel: "challenge-passed"},
			{AgentID: agentB, Domain: "example.com", LocalID: "b", ClaimLevel: "challenge-passed"},
		}
	}).Once()
	mockSoulPublicIdentityStatuses(t, &tdb, agentA, agentB)
	tdb.qBoundIdx.On("First", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulBoundaryKeywordAgentIndex](t, args, 0)
		*dest = models.SoulBoundaryKeywordAgentIndex{AgentID: agentA, Domain: "example.com", LocalID: "a", Keyword: "finance"}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"capability": {"social"},
		"q":          {"example.com"},
		"claimLevel": {"challenge-passed"},
		"boundary":   {"finance"},
		"status":     {"active"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentA)
}

func TestHandleSoulPublicGetAgentChannels_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("11", 32)
	identityUpdated := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	prefsUpdated := identityUpdated.Add(2 * time.Hour)
	emailUpdated := identityUpdated.Add(3 * time.Hour)
	verifiedAt := identityUpdated.Add(30 * time.Minute)

	mockSoulPublicGetAgentChannelsSuccess(t, &tdb, agentID, identityUpdated, prefsUpdated, emailUpdated, verifiedAt)

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgentChannels(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSoulPublicAgentChannelsResponse(t, resp, agentID, emailUpdated)
}

func mockSoulPublicIdentityStatuses(t *testing.T, tdb *soulPublicTestDB, activeAgentID string, suspendedAgentID string) {
	t.Helper()

	firstCalls := 0
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		status := models.SoulAgentStatusActive
		agentID := activeAgentID
		if firstCalls > 0 {
			status = models.SoulAgentStatusSuspended
			agentID = suspendedAgentID
		}
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: status}
		firstCalls++
	}).Times(2)
}

func assertSoulPublicSearchResponse(t *testing.T, s *Server, ctx *apptheory.Context, expectedAgentID string) {
	t.Helper()

	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
	var out soulSearchResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != expectedAgentID {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func mockSoulPublicGetAgentChannelsSuccess(
	t *testing.T,
	tdb *soulPublicTestDB,
	agentID string,
	identityUpdated time.Time,
	prefsUpdated time.Time,
	emailUpdated time.Time,
	verifiedAt time.Time,
) {
	t.Helper()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentID,
			Domain:    "example.com",
			LocalID:   "agent-bob",
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: identityUpdated,
		}
	}).Once()
	tdb.qPrefs.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentContactPreferences](t, args, 0)
		*dest = models.SoulAgentContactPreferences{
			AgentID:              agentID,
			Preferred:            "email",
			AvailabilitySchedule: "always",
			ResponseTarget:       "PT4H",
			ResponseGuarantee:    "best-effort",
			Languages:            []string{"en"},
			UpdatedAt:            prefsUpdated,
		}
	}).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:      agentID,
			ChannelType:  models.SoulChannelTypeEmail,
			Identifier:   "agent-bob@lessersoul.ai",
			Capabilities: []string{"receive", "send"},
			Protocols:    []string{"smtp"},
			Verified:     true,
			VerifiedAt:   verifiedAt,
			Status:       models.SoulChannelStatusActive,
			UpdatedAt:    emailUpdated,
		}
	}).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
}

func assertSoulPublicAgentChannelsResponse(t *testing.T, resp *apptheory.Response, agentID string, emailUpdated time.Time) {
	t.Helper()
	assertSoulPublicAgentChannelsStatusAndHeaders(t, resp)
	out := decodeSoulPublicAgentChannelsResponse(t, resp)
	assertSoulPublicAgentChannelsBody(t, out, agentID)
	assertSoulPublicAgentChannelsUpdatedAt(t, out.UpdatedAt, emailUpdated)
}

func assertSoulPublicAgentChannelsStatusAndHeaders(t *testing.T, resp *apptheory.Response) {
	t.Helper()
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if resp.Headers == nil || len(resp.Headers["cache-control"]) != 1 || resp.Headers["cache-control"][0] != boundaryTestCacheControl {
		t.Fatalf("unexpected headers: %#v", resp.Headers)
	}
}

func decodeSoulPublicAgentChannelsResponse(t *testing.T, resp *apptheory.Response) soulPublicAgentChannelsResponse {
	t.Helper()
	var out soulPublicAgentChannelsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func assertSoulPublicAgentChannelsBody(t *testing.T, out soulPublicAgentChannelsResponse, agentID string) {
	t.Helper()
	if out.AgentID != agentID {
		t.Fatalf("expected agentId %q, got %q", agentID, out.AgentID)
	}
	if out.Channels.ENS != nil || out.Channels.Phone != nil || out.Channels.Email == nil {
		t.Fatalf("unexpected channels: %#v", out.Channels)
	}
	if out.Channels.Email.Address != "agent-bob@lessersoul.ai" || !out.Channels.Email.Verified || out.Channels.Email.VerifiedAt == "" {
		t.Fatalf("unexpected email channel: %#v", out.Channels.Email)
	}
	if out.ContactPreferences == nil || out.ContactPreferences.Preferred != commChannelEmail || len(out.ContactPreferences.Languages) != 1 {
		t.Fatalf("unexpected prefs: %#v", out.ContactPreferences)
	}
}

func assertSoulPublicAgentChannelsUpdatedAt(t *testing.T, got string, emailUpdated time.Time) {
	t.Helper()
	if got != emailUpdated.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("expected updatedAt %q, got %q", emailUpdated.UTC().Format(time.RFC3339Nano), got)
	}
}

func TestHandleSoulPublicResolveEmail_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("22", 32)

	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
		*dest = models.SoulEmailAgentIndex{Email: "agent-bob@lessersoul.ai", AgentID: agentID}
	}).Once()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Params: map[string]string{"emailAddress": "agent-bob@lessersoul.ai"}}
	resp, err := s.handleSoulPublicResolveEmail(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulPublicAgentResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Agent.AgentID != agentID {
		t.Fatalf("expected agent_id %q, got %#v", agentID, out)
	}
}

func TestHandleSoulPublicSearch_ChannelFilter(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("aa", 32)

	tdb.qChanIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulChannelAgentIndex](t, args, 0)
		*dest = []*models.SoulChannelAgentIndex{
			{AgentID: agentID, Domain: "example.com", LocalID: "agent-a", ChannelType: "email"},
		}
	}).Once()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"channel": {"email"}}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulSearchResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != agentID {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandleSoulPublicSearch_ENS(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("bb", 32)

	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent-bob.lessersoul.eth", AgentID: agentID}
	}).Once()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-bob", Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"ens": {"agent-bob.lessersoul.eth"}}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulSearchResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != agentID {
		t.Fatalf("unexpected response: %#v", out)
	}
}
