package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
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

type soulRegistryTestDB struct {
	db           *ttmocks.MockExtendedDB
	qReg         *ttmocks.MockQuery
	qOp          *ttmocks.MockQuery
	qAudit       *ttmocks.MockQuery
	qWalletIdx   *ttmocks.MockQuery
	qUser        *ttmocks.MockQuery
	qDomain      *ttmocks.MockQuery
	qInstance    *ttmocks.MockQuery
	qIdentity    *ttmocks.MockQuery
	qWalletAgent *ttmocks.MockQuery
	qDomainAgent *ttmocks.MockQuery
	qCapAgent    *ttmocks.MockQuery
}

func newSoulRegistryTestDB() soulRegistryTestDB {
	db := ttmocks.NewMockExtendedDB()
	qReg := new(ttmocks.MockQuery)
	qOp := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qWalletIdx := new(ttmocks.MockQuery)
	qUser := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qWalletAgent := new(ttmocks.MockQuery)
	qDomainAgent := new(ttmocks.MockQuery)
	qCapAgent := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(qReg).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(qWalletIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulWalletAgentIndex")).Return(qWalletAgent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulDomainAgentIndex")).Return(qDomainAgent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(qCapAgent).Maybe()

	for _, q := range []*ttmocks.MockQuery{
		qReg,
		qOp,
		qAudit,
		qWalletIdx,
		qUser,
		qDomain,
		qInstance,
		qIdentity,
		qWalletAgent,
		qDomainAgent,
		qCapAgent,
	} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("All", mock.Anything).Return(nil).Maybe()
	}

	return soulRegistryTestDB{
		db:           db,
		qReg:         qReg,
		qOp:          qOp,
		qAudit:       qAudit,
		qWalletIdx:   qWalletIdx,
		qUser:        qUser,
		qDomain:      qDomain,
		qInstance:    qInstance,
		qIdentity:    qIdentity,
		qWalletAgent: qWalletAgent,
		qDomainAgent: qDomainAgent,
		qCapAgent:    qCapAgent,
	}
}

func TestHandleSoulAgentRegistrationBegin_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulTxMode:                  "safe",
			SoulAdminSafeAddress:        "0x0000000000000000000000000000000000000002",
			SoulSupportedCapabilities:   []string{"social", "commerce"},
		},
	}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: models.RoleCustomer, ApprovalStatus: models.UserApprovalStatusApproved}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(soulAgentRegistrationBeginRequest{
		Domain:       testDomainExampleCom,
		LocalID:      "agent-alice",
		Wallet:       "0x000000000000000000000000000000000000dEaD",
		Capabilities: []any{"social"},
	})

	ctx := &apptheory.Context{RequestID: "r1", AuthIdentity: "alice", Request: apptheory.Request{Body: body}}
	resp, err := s.handleSoulAgentRegistrationBegin(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulAgentRegistrationBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Registration.ID == "" || out.Wallet.Message == "" || out.Wallet.Nonce == "" {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Registration.DomainNormalized != testDomainExampleCom {
		t.Fatalf("expected normalized domain, got %#v", out.Registration.DomainNormalized)
	}
	if out.Registration.LocalID != "agent-alice" {
		t.Fatalf("expected normalized local id, got %#v", out.Registration.LocalID)
	}
	if out.Wallet.Address != strings.ToLower("0x000000000000000000000000000000000000dEaD") {
		t.Fatalf("expected wallet lowercased, got %#v", out.Wallet.Address)
	}
	if len(out.Proofs) != 2 {
		t.Fatalf("expected 2 proof instructions, got %#v", out.Proofs)
	}

	dnsProof := out.Proofs[0]
	httpsProof := out.Proofs[1]
	if strings.TrimSpace(dnsProof.DNSValue) == "" {
		t.Fatalf("expected DNS proof value, got %#v", out.Proofs)
	}
	if !strings.HasPrefix(dnsProof.DNSValue, soulRegistryProofValue) {
		t.Fatalf("expected DNS proof to start with %q, got %#v", soulRegistryProofValue, dnsProof.DNSValue)
	}
	token := strings.TrimPrefix(dnsProof.DNSValue, soulRegistryProofValue)
	if strings.TrimSpace(token) == "" {
		t.Fatalf("expected non-empty proof token, got %#v", dnsProof.DNSValue)
	}

	var httpsBody map[string]any
	if err := json.Unmarshal([]byte(httpsProof.HTTPSBody), &httpsBody); err != nil {
		t.Fatalf("expected HTTPS proof body to be JSON, got %q (err=%v)", httpsProof.HTTPSBody, err)
	}
	if got, _ := httpsBody["lesser-soul-agent"].(string); got != token {
		t.Fatalf("expected HTTPS proof token %q, got %#v", token, httpsBody)
	}
	if !strings.HasPrefix(out.Proofs[0].DNSName, soulRegistryProofPrefix) {
		t.Fatalf("unexpected dns name: %#v", out.Proofs[0].DNSName)
	}
}

func TestHandleSoulAgentRegistrationBegin_StructuredCapabilities_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulTxMode:                  "safe",
			SoulAdminSafeAddress:        "0x0000000000000000000000000000000000000002",
			SoulSupportedCapabilities:   []string{"social", "commerce"},
		},
	}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: models.RoleCustomer, ApprovalStatus: models.UserApprovalStatusApproved}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(soulAgentRegistrationBeginRequest{
		Domain:  testDomainExampleCom,
		LocalID: "agent-alice",
		Wallet:  "0x000000000000000000000000000000000000dEaD",
		Capabilities: []any{
			map[string]any{
				"capability": "social",
				"scope":      "general",
				"claimLevel": "self-declared",
			},
		},
	})

	ctx := &apptheory.Context{RequestID: "r1", AuthIdentity: "alice", Request: apptheory.Request{Body: body}}
	resp, err := s.handleSoulAgentRegistrationBegin(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}
}

func TestHandleSoulAgentRegistrationBegin_RejectsNonSelfDeclaredClaimLevels(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulTxMode:                  "safe",
			SoulAdminSafeAddress:        "0x0000000000000000000000000000000000000002",
			SoulSupportedCapabilities:   []string{"social", "commerce"},
		},
	}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: models.RoleCustomer, ApprovalStatus: models.UserApprovalStatusApproved}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(soulAgentRegistrationBeginRequest{
		Domain:  testDomainExampleCom,
		LocalID: "agent-alice",
		Wallet:  "0x000000000000000000000000000000000000dEaD",
		Capabilities: []any{
			map[string]any{
				"capability":    "social",
				"claimLevel":    "challenge-passed",
				"validationRef": "VALIDATION#2026-03-01T00:00:00Z#c1",
			},
		},
	})

	ctx := &apptheory.Context{RequestID: "r1", AuthIdentity: "alice", Request: apptheory.Request{Body: body}}
	_, err := s.handleSoulAgentRegistrationBegin(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestHandleSoulAgentRegistrationVerify_UsesExistingProofFlagsAndCreatesOperation(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulTxMode:                  "safe",
			SoulAdminSafeAddress:        "0x0000000000000000000000000000000000000002",
			SoulMintSignerKey:           strings.Repeat("ab", 32),
			WebAuthnRPID:                "lesser.host",
		},
	}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: models.RoleCustomer, ApprovalStatus: models.UserApprovalStatusApproved}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	// Registration loaded by id.
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	addr := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	message := "soul test message"
	sig, err := crypto.Sign(accounts.TextHash([]byte(message)), key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	sigHex := "0x" + hex.EncodeToString(sig)

	tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentRegistration](t, args, 0)
		*dest = models.SoulAgentRegistration{
			ID:               "reg1",
			Username:         "alice",
			DomainNormalized: "example.com",
			LocalID:          "agent-alice",
			AgentID:          "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab",
			Wallet:           addr,
			WalletMessage:    message,
			ProofToken:       "t1",
			DNSVerified:      true,
			HTTPSVerified:    true,
			Status:           models.SoulAgentRegistrationStatusPending,
			CreatedAt:        time.Now().Add(-time.Minute).UTC(),
			UpdatedAt:        time.Now().Add(-time.Minute).UTC(),
			ExpiresAt:        time.Now().Add(time.Minute).UTC(),
		}
	}).Once()

	// No existing identity yet.
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Twice()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	principalDeclaration := "I accept responsibility for this agent's behavior."
	principalDigest := crypto.Keccak256([]byte(principalDeclaration))
	principalSig, err := crypto.Sign(accounts.TextHash(principalDigest), key)
	if err != nil {
		t.Fatalf("principal Sign: %v", err)
	}
	principalSigHex := "0x" + hex.EncodeToString(principalSig)

	body, _ := json.Marshal(soulAgentRegistrationVerifyRequest{
		Signature:            sigHex,
		PrincipalAddress:     addr,
		PrincipalDeclaration: principalDeclaration,
		PrincipalSignature:   principalSigHex,
		DeclaredAt:           time.Now().UTC().Format(time.RFC3339),
	})
	ctx := &apptheory.Context{
		RequestID:    "r2",
		AuthIdentity: "alice",
		Params:       map[string]string{"id": "reg1"},
		Request:      apptheory.Request{Body: body},
	}
	resp, err := s.handleSoulAgentRegistrationVerify(ctx)
	if err != nil {
		t.Fatalf("verify err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulAgentRegistrationVerifyResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Operation.OperationID == "" {
		t.Fatalf("expected operation id")
	}
	if out.SafeTx == nil || out.SafeTx.To == "" || !strings.HasPrefix(out.SafeTx.Data, "0x") {
		t.Fatalf("expected safe tx payload, got %#v", out.SafeTx)
	}
}

func TestNormalizeSoulCapabilitiesStrict_EnforcesSupportedList(t *testing.T) {
	t.Parallel()

	got, err := normalizeSoulCapabilitiesStrict([]string{"social"}, []string{" social ", "SOCIAL", "commerce"})
	if err == nil {
		t.Fatalf("expected error for unsupported capability, got=%v", got)
	}

	got, err = normalizeSoulCapabilitiesStrict([]string{"social"}, []string{" social ", "SOCIAL"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0] != "social" {
		t.Fatalf("unexpected caps: %#v", got)
	}
}

func TestVerifySoulRegistryDNS_EmptyInput(t *testing.T) {
	t.Parallel()

	if verifySoulRegistryDNS(context.Background(), "", "x") {
		t.Fatalf("expected false")
	}
	if verifySoulRegistryDNS(context.Background(), "example.com", "") {
		t.Fatalf("expected false")
	}
}
