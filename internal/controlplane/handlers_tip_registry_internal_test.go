package controlplane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type tipRegistryTestDB struct {
	db     *ttmocks.MockExtendedDB
	qReg   *ttmocks.MockQuery
	qOp    *ttmocks.MockQuery
	qAudit *ttmocks.MockQuery
}

func newTipRegistryTestDB() tipRegistryTestDB {
	db := ttmocks.NewMockExtendedDB()
	qReg := new(ttmocks.MockQuery)
	qOp := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.TipHostRegistration")).Return(qReg).Maybe()
	db.On("Model", mock.AnythingOfType("*models.TipRegistryOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qReg, qOp, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
	}

	return tipRegistryTestDB{db: db, qReg: qReg, qOp: qOp, qAudit: qAudit}
}

func TestHandleTipHostRegistrationBegin_Success(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:          true,
			TipChainID:          1,
			TipContractAddress:  "0x0000000000000000000000000000000000000001",
			TipTxMode:           tipTxModeSafe,
			TipAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}

	body, _ := json.Marshal(tipHostRegistrationBeginRequest{
		Domain:     "example.com",
		WalletAddr: "0x000000000000000000000000000000000000dEaD",
		HostFeeBps: 5,
	})
	resp, err := s.handleTipHostRegistrationBegin(&apptheory.Context{RequestID: "r1", Request: apptheory.Request{Body: body}})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out tipHostRegistrationBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Registration.ID == "" || out.Wallet.Message == "" || out.Wallet.Nonce == "" {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Registration.DomainNormalized != "example.com" {
		t.Fatalf("expected normalized domain, got %#v", out.Registration.DomainNormalized)
	}
	if out.Wallet.Address != strings.ToLower("0x000000000000000000000000000000000000dEaD") {
		t.Fatalf("expected wallet lowercased, got %#v", out.Wallet.Address)
	}
	if len(out.Proofs) != 2 {
		t.Fatalf("expected 2 proof instructions, got %#v", out.Proofs)
	}
	if out.Proofs[0].DNSValue == "" || out.Proofs[0].DNSValue != out.Proofs[1].HTTPSBody {
		t.Fatalf("expected shared proof value, got %#v", out.Proofs)
	}
	if !strings.HasPrefix(out.Proofs[0].DNSName, tipRegistryProofPrefix) {
		t.Fatalf("unexpected dns name: %#v", out.Proofs[0].DNSName)
	}
}

func TestValidateOutboundHost_AndIsDeniedIP(t *testing.T) {
	t.Parallel()

	if err := validateOutboundHost(nil, " "); err == nil {
		t.Fatalf("expected error")
	}
	if err := validateOutboundHost(nil, "localhost"); err == nil {
		t.Fatalf("expected localhost blocked")
	}
	if err := validateOutboundHost(nil, "127.0.0.1"); err == nil {
		t.Fatalf("expected loopback blocked")
	}
	if err := validateOutboundHost(nil, "8.8.8.8"); err != nil {
		t.Fatalf("expected public ip allowed, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateOutboundHost(ctx, "example.com"); err == nil {
		t.Fatalf("expected canceled ctx to fail resolve")
	}
}

func TestCreateTipRegistryOperationForRegistration_ValidatesAndStores(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:          true,
			TipChainID:          1,
			TipContractAddress:  "0x0000000000000000000000000000000000000001",
			TipTxMode:           tipTxModeSafe,
			TipAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}

	if _, _, err := s.createTipRegistryOperationForRegistration(context.Background(), nil); err == nil {
		t.Fatalf("expected error")
	}

	// Safe mode requires safe configured.
	s2 := &Server{store: store.New(tdb.db), cfg: config.Config{TipContractAddress: "0x0000000000000000000000000000000000000001", TipTxMode: tipTxModeSafe}}
	if _, _, err := s2.createTipRegistryOperationForRegistration(context.Background(), &models.TipHostRegistration{Kind: models.TipRegistryOperationKindRegisterHost}); err == nil {
		t.Fatalf("expected conflict for missing safe address")
	}

	// Success path.
	reg := &models.TipHostRegistration{
		ID:               "r1",
		Kind:             models.TipRegistryOperationKindRegisterHost,
		DomainRaw:        "example.com",
		DomainNormalized: "example.com",
		HostIDHex:        "0x" + strings.Repeat("11", 32),
		WalletAddr:       "0x0000000000000000000000000000000000000003",
		HostFeeBps:       5,
	}
	op, safeTx, err := s.createTipRegistryOperationForRegistration(context.Background(), reg)
	if err != nil || op == nil || safeTx == nil {
		t.Fatalf("unexpected: op=%#v safeTx=%#v err=%v", op, safeTx, err)
	}
	if safeTx.To == "" || !strings.HasPrefix(safeTx.Data, "0x") {
		t.Fatalf("unexpected safe tx: %#v", safeTx)
	}
}

func TestTipRegistryAdminEndpoints_ListAndGet(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	tdb.qOp.On("All", mock.AnythingOfType("*[]*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.TipRegistryOperation)
		*dest = []*models.TipRegistryOperation{
			nil,
			{ID: "op1", Status: models.TipRegistryOperationStatusPending},
		}
	}).Once()
	if resp, err := s.handleListTipRegistryOperations(ctx); err != nil || resp.Status != 200 {
		t.Fatalf("list ops: resp=%#v err=%v", resp, err)
	}

	tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(theoryErrors.ErrItemNotFound).Once()
	getCtx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"id": "missing"},
	}
	getCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	if _, err := s.handleGetTipRegistryOperation(getCtx); err == nil {
		t.Fatalf("expected not_found")
	}
}
