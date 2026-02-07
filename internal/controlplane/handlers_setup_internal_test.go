package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestParseSetupBootstrapVerifyInput(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, err := parseSetupBootstrapVerifyInput(ctx); err == nil {
		t.Fatalf("expected error")
	}

	// Accept legacy snake_case challenge id and "challenge" field for message.
	ctx.Request.Body = []byte(`{"challenge_id":"c","address":"a","signature":"s","challenge":"m"}`)
	got, err := parseSetupBootstrapVerifyInput(ctx)
	if err != nil {
		t.Fatalf("parseSetupBootstrapVerifyInput: %v", err)
	}
	if got.ChallengeID != "c" || got.Message != "m" {
		t.Fatalf("unexpected parsed input: %#v", got)
	}
}

func TestParseSetupCreateAdminRequestInput(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, appErr := parseSetupCreateAdminRequestInput(ctx); appErr == nil {
		t.Fatalf("expected error")
	}

	ctx.Request.Body = []byte(`{"username":"bootstrap","wallet":{"challengeId":"c","address":"a","signature":"s","message":"m"}}`)
	if _, appErr := parseSetupCreateAdminRequestInput(ctx); appErr == nil {
		t.Fatalf("expected reserved username error")
	}

	ctx.Request.Body = []byte(`{"username":" alice ","displayName":" Alice ","wallet":{"challengeId":" c ","address":" a ","signature":" s ","message":" m "}}`)
	req, appErr := parseSetupCreateAdminRequestInput(ctx)
	if appErr != nil {
		t.Fatalf("parseSetupCreateAdminRequestInput: %v", appErr)
	}
	if req.Username != "alice" || req.DisplayName != "Alice" || req.Wallet.ChallengeID != "c" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

type setupTestDB struct {
	db           *ttmocks.MockExtendedDB
	qCP          *ttmocks.MockQuery
	qSetup       *ttmocks.MockQuery
	qWallet      *ttmocks.MockQuery
	qWalletIndex *ttmocks.MockQuery
	qUser        *ttmocks.MockQuery
	qCred        *ttmocks.MockQuery
	qAudit       *ttmocks.MockQuery
}

func newSetupTestDB() setupTestDB {
	db := ttmocks.NewMockExtendedDB()
	qCP := new(ttmocks.MockQuery)
	qSetup := new(ttmocks.MockQuery)
	qWallet := new(ttmocks.MockQuery)
	qWalletIndex := new(ttmocks.MockQuery)
	qUser := new(ttmocks.MockQuery)
	qCred := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(qCP).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SetupSession")).Return(qSetup).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletChallenge")).Return(qWallet).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(qWalletIndex).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletCredential")).Return(qCred).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qCP, qSetup, qWallet, qWalletIndex, qUser, qCred, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return setupTestDB{
		db:           db,
		qCP:          qCP,
		qSetup:       qSetup,
		qWallet:      qWallet,
		qWalletIndex: qWalletIndex,
		qUser:        qUser,
		qCred:        qCred,
		qAudit:       qAudit,
	}
}

func TestHandleSetupStatus_LockedAndActive(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{cfg: config.Config{Stage: "lab"}, store: store.New(tdb.db)}

	// Locked when config missing.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	resp, err := s.handleSetupStatus(&apptheory.Context{})
	if err != nil {
		t.Fatalf("handleSetupStatus err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	var out setupStatusResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ControlPlaneState != "locked" || !out.Locked {
		t.Fatalf("unexpected locked status: %#v", out)
	}

	// Active when bootstrapped.
	now := time.Now().UTC()
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ControlPlaneConfig)
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "admin", BootstrappedAt: now}
		_ = dest.UpdateKeys()
	}).Once()
	resp, err = s.handleSetupStatus(&apptheory.Context{})
	if err != nil || resp.Status != 200 {
		t.Fatalf("handleSetupStatus active: resp=%#v err=%v", resp, err)
	}
}

func TestHandleSetupBootstrapChallenge_ErrorsAndSuccess(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	key, addr := generateWalletKey(t)
	_ = key

	s := &Server{cfg: config.Config{BootstrapWalletAddress: addr}, store: store.New(tdb.db)}

	// Locked (config missing).
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Maybe()

	// Address mismatch.
	body, _ := json.Marshal(setupBootstrapChallengeRequest{Address: "0xdead", ChainID: 1})
	resp, err := s.handleSetupBootstrapChallenge(&apptheory.Context{Request: apptheory.Request{Body: body}})
	if err == nil || resp != nil {
		t.Fatalf("expected error for mismatched address")
	}

	// Success (Create challenge).
	tdb.qWallet.On("Create").Return(nil).Once()
	body, _ = json.Marshal(setupBootstrapChallengeRequest{Address: addr, ChainID: 0})
	resp, err = s.handleSetupBootstrapChallenge(&apptheory.Context{Request: apptheory.Request{Body: body}})
	if err != nil {
		t.Fatalf("handleSetupBootstrapChallenge err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	var out walletChallengeResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID == "" || out.Username != setupBootstrapUser {
		t.Fatalf("unexpected challenge: %#v", out)
	}
	if out.ChainID != 1 {
		t.Fatalf("expected default chainId 1, got %#v", out)
	}
}

func TestHandleSetupBootstrapVerify_Success(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	key, addr := generateWalletKey(t)

	s := &Server{cfg: config.Config{BootstrapWalletAddress: addr}, store: store.New(tdb.db)}

	// Locked (config missing).
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Maybe()

	msg := "bootstrap verify"
	sig := signWalletMessage(t, key, msg)

	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.WalletChallenge)
		*dest = models.WalletChallenge{
			ID:        "c1",
			Username:  setupBootstrapUser,
			Address:   strings.ToLower(addr),
			ChainID:   1,
			Nonce:     "n",
			Message:   msg,
			IssuedAt:  time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qWallet.On("Delete").Return(nil).Once()
	tdb.qSetup.On("Create").Return(nil).Once()

	body, _ := json.Marshal(setupBootstrapVerifyRequest{
		ChallengeID: "c1",
		Address:     addr,
		Signature:   sig,
		Message:     msg,
	})
	resp, err := s.handleSetupBootstrapVerify(&apptheory.Context{Request: apptheory.Request{Body: body}})
	if err != nil {
		t.Fatalf("handleSetupBootstrapVerify err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	var out setupBootstrapVerifyResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.TokenType != "Bearer" || out.Token == "" || out.ExpiresAt.IsZero() {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestRequireSetupSession_ExpiredDeletesAndUnauthorized(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{cfg: config.Config{BootstrapWalletAddress: "0xabc"}, store: store.New(tdb.db)}

	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.SetupSession)
		*dest = models.SetupSession{
			ID:         "tok",
			Purpose:    setupPurposeBootstrap,
			WalletAddr: strings.ToLower("0xabc"),
			IssuedAt:   time.Now().UTC().Add(-2 * time.Hour),
			ExpiresAt:  time.Now().UTC().Add(-1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qSetup.On("Delete").Return(nil).Once()

	_, err := s.requireSetupSession(&apptheory.Context{
		Request: apptheory.Request{
			Headers: map[string][]string{"authorization": {"Bearer tok"}},
		},
	})
	if err == nil {
		t.Fatalf("expected unauthorized")
	}
}

func TestHandleSetupCreateAdmin_AndFinalize_Success(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{cfg: config.Config{BootstrapWalletAddress: "0xboot"}, store: store.New(tdb.db)}

	// Locked + no primary admin yet.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()

	// Setup session present.
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.SetupSession)
		*dest = models.SetupSession{
			ID:         "setup-token",
			Purpose:    setupPurposeBootstrap,
			WalletAddr: strings.ToLower("0xboot"),
			IssuedAt:   time.Now().UTC(),
			ExpiresAt:  time.Now().UTC().Add(1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()

	// Admin wallet challenge.
	key, addr := generateWalletKey(t)
	msg := "admin challenge"
	sig := signWalletMessage(t, key, msg)
	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.WalletChallenge)
		*dest = models.WalletChallenge{
			ID:        "wc1",
			Username:  "alice",
			Address:   strings.ToLower(addr),
			ChainID:   1,
			Nonce:     "n",
			Message:   msg,
			IssuedAt:  time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qWallet.On("Delete").Return(nil).Once()

	// Wallet not linked yet.
	tdb.qWalletIndex.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	tdb.qUser.On("Create").Return(nil).Once()
	tdb.qCred.On("Create").Return(nil).Once()
	tdb.qWalletIndex.On("Create").Return(nil).Once()
	tdb.qCP.On("CreateOrUpdate").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	body, _ := json.Marshal(setupCreateAdminRequest{
		Username:    "alice",
		DisplayName: "Alice",
		Wallet: walletVerifyRequest{
			ChallengeID: "wc1",
			Address:     addr,
			Signature:   sig,
			Message:     msg,
		},
	})
	ctx := &apptheory.Context{
		RequestID:    "rid",
		Request:      apptheory.Request{Body: body, Headers: map[string][]string{"authorization": {"Bearer setup-token"}}},
		AuthIdentity: "alice", // unused by create_admin but present
	}
	resp, err := s.handleSetupCreateAdmin(ctx)
	if err != nil {
		t.Fatalf("handleSetupCreateAdmin err: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("expected 201, got %d", resp.Status)
	}

	// Finalize requires admin auth and primary admin configured.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ControlPlaneConfig)
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "alice"}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qCP.On("CreateOrUpdate").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx2 := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid2"}
	ctx2.Set(ctxKeyOperatorRole, models.RoleAdmin)
	resp, err = s.handleSetupFinalize(ctx2)
	if err != nil {
		t.Fatalf("handleSetupFinalize err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}
