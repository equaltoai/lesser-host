package controlplane

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const soulLifecycleTestAgentIDHex = "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab"

type fakeEVMClient struct {
	callContract func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error)
}

func (f *fakeEVMClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if f == nil || f.callContract == nil {
		return nil, nil
	}
	return f.callContract(ctx, msg)
}

func (f *fakeEVMClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return nil, ethereum.NotFound
}

func (f *fakeEVMClient) Close() {}

type fakeSoulPackStore struct {
	key          string
	body         []byte
	contentType  string
	cacheControl string
	puts         []fakePut
	objects      map[string]fakePut
}

type fakePut struct {
	key  string
	body []byte
}

func (f *fakeSoulPackStore) PutObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error {
	f.key = key
	f.body = append([]byte(nil), body...)
	f.contentType = contentType
	f.cacheControl = cacheControl
	f.puts = append(f.puts, fakePut{key: key, body: append([]byte(nil), body...)})
	if f.objects == nil {
		f.objects = map[string]fakePut{}
	}
	f.objects[key] = fakePut{key: key, body: append([]byte(nil), body...)}
	return nil
}

func (f *fakeSoulPackStore) GetObject(ctx context.Context, key string, maxBytes int64) ([]byte, string, string, error) {
	if f == nil || f.objects == nil {
		return nil, "", "", &s3types.NoSuchKey{}
	}
	obj, ok := f.objects[key]
	if !ok {
		return nil, "", "", &s3types.NoSuchKey{}
	}
	if maxBytes > 0 && int64(len(obj.body)) > maxBytes {
		return nil, "", "", errors.New("object too large")
	}
	return append([]byte(nil), obj.body...), f.contentType, "", nil
}

type soulLifecycleTestDB struct {
	db          *ttmocks.MockExtendedDB
	qDomain     *ttmocks.MockQuery
	qInstance   *ttmocks.MockQuery
	qIdentity   *ttmocks.MockQuery
	qRotation   *ttmocks.MockQuery
	qOp         *ttmocks.MockQuery
	qAudit      *ttmocks.MockQuery
	qWalletIdx  *ttmocks.MockQuery
	qCapIdx     *ttmocks.MockQuery
	qVersion    *ttmocks.MockQuery
	qBoundary   *ttmocks.MockQuery
	qBoundIdx   *ttmocks.MockQuery
	qChannel    *ttmocks.MockQuery
	qPrefs      *ttmocks.MockQuery
	qEmailIdx   *ttmocks.MockQuery
	qPhoneIdx   *ttmocks.MockQuery
	qChannelIdx *ttmocks.MockQuery
	qENS        *ttmocks.MockQuery
	qContinuity *ttmocks.MockQuery
	qDispute    *ttmocks.MockQuery
}

func newSoulLifecycleTestDB() soulLifecycleTestDB {
	db := ttmocks.NewMockExtendedDB()
	qDomain := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qRotation := new(ttmocks.MockQuery)
	qOp := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qWalletIdx := new(ttmocks.MockQuery)
	qCapIdx := new(ttmocks.MockQuery)
	qVersion := new(ttmocks.MockQuery)
	qBoundary := new(ttmocks.MockQuery)
	qBoundIdx := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qContactPrefs := new(ttmocks.MockQuery)
	qEmailIdx := new(ttmocks.MockQuery)
	qPhoneIdx := new(ttmocks.MockQuery)
	qChannelTypeIdx := new(ttmocks.MockQuery)
	qENSResolution := new(ttmocks.MockQuery)
	qContinuity := new(ttmocks.MockQuery)
	qDispute := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(qRotation).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(qWalletIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(qCapIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentVersion")).Return(qVersion).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(qBoundary).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(qBoundIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(qContactPrefs).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(qEmailIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(qPhoneIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(qChannelTypeIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(qENSResolution).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentContinuity")).Return(qContinuity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentDispute")).Return(qDispute).Maybe()

	for _, q := range []*ttmocks.MockQuery{
		qDomain,
		qInstance,
		qIdentity,
		qRotation,
		qOp,
		qAudit,
		qWalletIdx,
		qCapIdx,
		qVersion,
		qBoundary,
		qBoundIdx,
		qChannel,
		qContactPrefs,
		qEmailIdx,
		qPhoneIdx,
		qChannelTypeIdx,
		qENSResolution,
		qContinuity,
		qDispute,
	} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Update", mock.Anything, mock.Anything).Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
	}

	return soulLifecycleTestDB{
		db:          db,
		qDomain:     qDomain,
		qInstance:   qInstance,
		qIdentity:   qIdentity,
		qRotation:   qRotation,
		qOp:         qOp,
		qAudit:      qAudit,
		qWalletIdx:  qWalletIdx,
		qCapIdx:     qCapIdx,
		qVersion:    qVersion,
		qBoundary:   qBoundary,
		qBoundIdx:   qBoundIdx,
		qChannel:    qChannel,
		qPrefs:      qContactPrefs,
		qEmailIdx:   qEmailIdx,
		qPhoneIdx:   qPhoneIdx,
		qChannelIdx: qChannelTypeIdx,
		qENS:        qENSResolution,
		qContinuity: qContinuity,
		qDispute:    qDispute,
	}
}

func TestHandleSoulAgentRotateWalletBegin_CreatesRotationRequest(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRPCURL:                  "http://rpc.local",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulTxMode:                  "safe",
			SoulAdminSafeAddress:        "0x0000000000000000000000000000000000000002",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	currentWallet := "0x000000000000000000000000000000000000beef"

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Wallet:  strings.ToLower(currentWallet),
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()

	tdb.qRotation.On("First", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(theoryErrors.ErrItemNotFound).Once()

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}
	walletRet, _ := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.HexToAddress(currentWallet))
	nonceRet, _ := parsedABI.Methods["agentNonces"].Outputs.Pack(big.NewInt(7))

	client := &fakeEVMClient{callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["getAgentWallet"].ID) {
			return walletRet, nil
		}
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["agentNonces"].ID) {
			return nonceRet, nil
		}
		return nil, ethereum.NotFound
	}}
	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) { return client, nil }

	body, _ := json.Marshal(soulRotateWalletBeginRequest{NewWalletAddress: "0x000000000000000000000000000000000000dEaD"})
	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: body},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulAgentRotateWalletBegin(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulRotateWalletBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Rotation.AgentID != agentIDHex {
		t.Fatalf("expected agent_id %q, got %q", agentIDHex, out.Rotation.AgentID)
	}
	if out.Rotation.CurrentWallet != strings.ToLower(currentWallet) {
		t.Fatalf("expected current_wallet %q, got %q", strings.ToLower(currentWallet), out.Rotation.CurrentWallet)
	}
	if out.Rotation.DigestHex == "" || out.Rotation.DigestHex != out.Typed.DigestHex {
		t.Fatalf("expected digest in response")
	}
	if out.Typed.Domain.ChainID != 1 {
		t.Fatalf("expected chainId 1, got %d", out.Typed.Domain.ChainID)
	}
	if out.Typed.Message.Nonce != "7" {
		t.Fatalf("expected nonce 7, got %q", out.Typed.Message.Nonce)
	}
}

func TestHandleSoulAgentRotateWalletConfirm_CreatesOperation(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRPCURL:                  "http://rpc.local",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulTxMode:                  "safe",
			SoulAdminSafeAddress:        "0x0000000000000000000000000000000000000002",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	_, agentInt, _ := parseSoulAgentIDHex(agentIDHex)
	nonce := big.NewInt(7)
	deadline := time.Now().UTC().Add(30 * time.Minute).Unix()

	currentKey, _ := crypto.GenerateKey()
	newKey, _ := crypto.GenerateKey()
	currentWallet := strings.ToLower(crypto.PubkeyToAddress(currentKey.PublicKey).Hex())
	newWallet := strings.ToLower(crypto.PubkeyToAddress(newKey.PublicKey).Hex())

	typed, digest, appErr := soulRotationTypedData(1, "0x0000000000000000000000000000000000000001", agentInt, currentWallet, newWallet, nonce, deadline)
	if appErr != nil {
		t.Fatalf("typed data: %v", appErr)
	}

	sigCurrent, _ := crypto.Sign(digest, currentKey)
	sigNew, _ := crypto.Sign(digest, newKey)

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Wallet:  currentWallet,
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()
	tdb.qRotation.On("First", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulWalletRotationRequest](t, args, 0)
		*dest = models.SoulWalletRotationRequest{
			AgentID:       agentIDHex,
			Username:      "admin",
			CurrentWallet: currentWallet,
			NewWallet:     newWallet,
			Nonce:         nonce.String(),
			Deadline:      deadline,
			DigestHex:     typed.DigestHex,
			Spent:         false,
			ExpiresAt:     time.Now().UTC().Add(30 * time.Minute),
		}
	}).Once()

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}
	walletRet, _ := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.HexToAddress(currentWallet))
	nonceRet, _ := parsedABI.Methods["agentNonces"].Outputs.Pack(nonce)
	client := &fakeEVMClient{callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["getAgentWallet"].ID) {
			return walletRet, nil
		}
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["agentNonces"].ID) {
			return nonceRet, nil
		}
		return nil, ethereum.NotFound
	}}
	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) { return client, nil }

	body, _ := json.Marshal(soulRotateWalletConfirmRequest{
		CurrentSignature: "0x" + hex.EncodeToString(sigCurrent),
		NewSignature:     "0x" + hex.EncodeToString(sigNew),
	})
	ctx := &apptheory.Context{
		RequestID:    "r2",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: body},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulAgentRotateWalletConfirm(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulRotateWalletConfirmResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Operation.Kind != models.SoulOperationKindRotateWallet {
		t.Fatalf("expected kind rotate_wallet, got %q", out.Operation.Kind)
	}
	if out.SafeTx == nil || out.SafeTx.To == "" || !strings.HasPrefix(out.SafeTx.Data, "0x") {
		t.Fatalf("expected safe tx payload, got %#v", out.SafeTx)
	}
}

func TestHandleSoulAgentUpdateRegistration_PublishesToS3(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	packs := &fakeSoulPackStore{}
	s := &Server{
		store:     store.New(tdb.db),
		soulPacks: packs,
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRPCURL:                  "http://rpc.local",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulTxMode:                  "safe",
			SoulAdminSafeAddress:        "0x0000000000000000000000000000000000000002",
			SoulSupportedCapabilities:   []string{"social", "commerce"},
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	key, _ := crypto.GenerateKey()
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:      agentIDHex,
			Domain:       "example.com",
			LocalID:      "agent-alice",
			Wallet:       wallet,
			Status:       models.SoulAgentStatusActive,
			Capabilities: []string{"social"},
			UpdatedAt:    time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()
	tdb.qVersion.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
		*dest = nil
	}).Once()
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Twice()

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}
	walletRet, _ := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.HexToAddress(wallet))
	client := &fakeEVMClient{callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["getAgentWallet"].ID) {
			return walletRet, nil
		}
		return nil, ethereum.NotFound
	}}
	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) { return client, nil }

	unsigned := map[string]any{
		"version":      "1",
		"agentId":      agentIDHex,
		"domain":       "example.com",
		"localId":      "agent-alice",
		"wallet":       wallet,
		"capabilities": []string{"social", "commerce"},
		"endpoints": map[string]any{
			"mcp": "https://example.com/soul/mcp",
		},
		"attestations": map[string]any{},
		"created":      "2026-02-21T00:00:00Z",
		"updated":      "2026-02-21T00:00:00Z",
	}
	unsignedBytes, _ := json.Marshal(unsigned)
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	digest := crypto.Keccak256(jcsBytes)
	sig, _ := crypto.Sign(accounts.TextHash(digest), key)
	sigHex := "0x" + hex.EncodeToString(sig)

	reg := map[string]any{}
	if unmarshalErr := json.Unmarshal(unsignedBytes, &reg); unmarshalErr != nil {
		t.Fatalf("unmarshal unsigned: %v", unmarshalErr)
	}
	regAttAny, ok := reg["attestations"]
	if !ok {
		t.Fatalf("expected attestations object")
	}
	regAtt, ok := regAttAny.(map[string]any)
	if !ok {
		t.Fatalf("expected attestations object, got %T", regAttAny)
	}
	regAtt["selfAttestation"] = sigHex
	regBytes, _ := json.Marshal(reg)

	ctx := &apptheory.Context{
		RequestID:    "r3",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: regBytes},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulAgentUpdateRegistration(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	out := assertSoulUpdateRegistrationResponse(t, resp, agentIDHex, wallet)
	assertSoulUpdateRegistrationPuts(t, packs, regBytes, out.S3Key, soulRegistrationVersionedS3Key(agentIDHex, out.Version))
}

func assertSoulUpdateRegistrationResponse(t *testing.T, resp *apptheory.Response, agentIDHex string, wallet string) soulUpdateRegistrationResponse {
	t.Helper()
	var out soulUpdateRegistrationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.S3Key != soulRegistrationS3Key(agentIDHex) {
		t.Fatalf("expected s3 key %q, got %q", soulRegistrationS3Key(agentIDHex), out.S3Key)
	}
	if out.Version < 1 {
		t.Fatalf("expected version >= 1, got %d", out.Version)
	}
	if out.Agent.Wallet != wallet {
		t.Fatalf("expected wallet %q, got %q", wallet, out.Agent.Wallet)
	}
	return out
}

func assertSoulUpdateRegistrationPuts(t *testing.T, packs *fakeSoulPackStore, regBytes []byte, currentKey string, versionedKey string) {
	t.Helper()
	if len(packs.puts) < 2 {
		t.Fatalf("expected at least 2 puts, got %d", len(packs.puts))
	}
	foundCurrent := false
	foundVersioned := false
	for _, put := range packs.puts {
		switch put.key {
		case currentKey:
			foundCurrent = true
			if !bytes.Equal(put.body, regBytes) {
				t.Fatalf("expected current put body to match request body")
			}
		case versionedKey:
			foundVersioned = true
			if !bytes.Equal(put.body, regBytes) {
				t.Fatalf("expected versioned put body to match request body")
			}
		}
	}
	if !foundCurrent {
		t.Fatalf("expected current registration put to %q", currentKey)
	}
	if !foundVersioned {
		t.Fatalf("expected versioned registration put to %q", versionedKey)
	}
}
