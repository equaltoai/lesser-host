package controlplane

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type authTestDB struct {
	db           *ttmocks.MockExtendedDB
	qWallet      *ttmocks.MockQuery
	qWalletIndex *ttmocks.MockQuery
	qUser        *ttmocks.MockQuery
	qSession     *ttmocks.MockQuery
	qAudit       *ttmocks.MockQuery
}

func newAuthTestDB() authTestDB {
	db := ttmocks.NewMockExtendedDB()
	qWallet := new(ttmocks.MockQuery)
	qWalletIndex := new(ttmocks.MockQuery)
	qUser := new(ttmocks.MockQuery)
	qSession := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletChallenge")).Return(qWallet).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(qWalletIndex).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()
	db.On("Model", mock.AnythingOfType("*models.OperatorSession")).Return(qSession).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qWallet, qWalletIndex, qUser, qSession, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return authTestDB{
		db:           db,
		qWallet:      qWallet,
		qWalletIndex: qWalletIndex,
		qUser:        qUser,
		qSession:     qSession,
		qAudit:       qAudit,
	}
}

func generateWalletKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	return key, addr
}

func signWalletMessage(t *testing.T, key *ecdsa.PrivateKey, message string) string {
	t.Helper()

	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return "0x" + hex.EncodeToString(sig)
}

func TestHandleWalletChallenge_Success(t *testing.T) {
	t.Parallel()

	tdb := newAuthTestDB()
	s := &Server{store: store.New(tdb.db)}

	body, _ := json.Marshal(walletChallengeRequest{Address: "0xabc", Username: testUsernameAlice})
	resp, err := s.handleWalletChallenge(&apptheory.Context{
		Request: apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out walletChallengeResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID == "" || out.Nonce == "" || out.Message == "" {
		t.Fatalf("expected non-empty challenge, got %#v", out)
	}
	if out.ChainID != 1 {
		t.Fatalf("expected default chainId 1, got %#v", out)
	}
}

func TestHandleWalletLogin_Success(t *testing.T) {
	t.Parallel()

	tdb := newAuthTestDB()
	s := &Server{store: store.New(tdb.db)}

	message := "hello"
	key, addr := generateWalletKey(t)
	sig := signWalletMessage(t, key, message)

	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
		*dest = models.WalletChallenge{
			ID:        "c1",
			Username:  testUsernameAlice,
			Address:   strings.ToLower(addr),
			ChainID:   1,
			Nonce:     "n",
			Message:   message,
			IssuedAt:  time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qWallet.On("Delete").Return(nil).Once()

	tdb.qWalletIndex.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletIndex](t, args, 0)
		*dest = models.WalletIndex{Username: testUsernameAlice}
	}).Once()

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: testUsernameAlice, Role: models.RoleAdmin}
	}).Once()

	tdb.qSession.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	req := walletVerifyRequest{
		ChallengeID: "c1",
		Address:     addr,
		Signature:   sig,
		Message:     message,
	}
	body, _ := json.Marshal(req)
	resp, err := s.handleWalletLogin(&apptheory.Context{
		RequestID: "r1",
		Request:   apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out operatorLoginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Token == "" || out.Username != testUsernameAlice || out.Role != models.RoleAdmin || out.Method != "wallet" {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandlePortalWalletChallengeAndLogin_CreatesUserWhenMissing(t *testing.T) {
	t.Parallel()

	tdb := newAuthTestDB()
	s := &Server{store: store.New(tdb.db)}

	key, addr := generateWalletKey(t)
	loginUsername := portalUsernameForWalletAddress(addr)

	tdb.qWallet.On("Create").Return(nil).Once()
	challengeBody, _ := json.Marshal(portalWalletChallengeRequest{Address: addr})
	chalResp, err := s.handlePortalWalletChallenge(&apptheory.Context{Request: apptheory.Request{Body: challengeBody}})
	if err != nil {
		t.Fatalf("challenge err: %v", err)
	}
	if chalResp.Status != 200 {
		t.Fatalf("expected 200, got %d", chalResp.Status)
	}
	var chal walletChallengeResponse
	_ = json.Unmarshal(chalResp.Body, &chal)
	if chal.Username != loginUsername {
		t.Fatalf("expected derived username, got %#v", chal)
	}

	// Fetch challenge for login.
	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
		*dest = models.WalletChallenge{
			ID:        chal.ID,
			Username:  chal.Username,
			Address:   strings.ToLower(addr),
			ChainID:   1,
			Nonce:     chal.Nonce,
			Message:   chal.Message,
			IssuedAt:  chal.IssuedAt,
			ExpiresAt: chal.ExpiresAt,
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qWallet.On("Delete").Return(nil).Once()

	// Not linked yet.
	tdb.qWalletIndex.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	// User does not exist yet.
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(theoryErrors.ErrItemNotFound).Once()

	tdb.qSession.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	sig := signWalletMessage(t, key, chal.Message)
	loginBody, _ := json.Marshal(portalWalletLoginRequest{
		ChallengeID: chal.ID,
		Address:     addr,
		Signature:   sig,
		Message:     chal.Message,
	})
	resp, err := s.handlePortalWalletLogin(&apptheory.Context{
		RequestID: "r2",
		Request:   apptheory.Request{Body: loginBody},
	})
	if err != nil {
		t.Fatalf("login err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestHandlePortalMe_OperatorMe_AndLogout(t *testing.T) {
	t.Parallel()

	tdb := newAuthTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: testUsernameAlice, Role: models.RoleCustomer, DisplayName: "Alice", Email: "a@example.com"}
	}).Twice()

	if resp, err := s.handlePortalMe(&apptheory.Context{AuthIdentity: testUsernameAlice}); err != nil || resp.Status != 200 {
		t.Fatalf("portal me: resp=%#v err=%v", resp, err)
	}
	if resp, err := s.handleOperatorMe(&apptheory.Context{AuthIdentity: testUsernameAlice}); err != nil || resp.Status != 200 {
		t.Fatalf("operator me: resp=%#v err=%v", resp, err)
	}

	tdb.qSession.On("Delete").Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qAudit.On("Create").Return(nil).Once()
	ctx := &apptheory.Context{AuthIdentity: testUsernameAlice}
	ctx.Set(ctxKeyOperatorSessionID, "session1")
	if resp, err := s.handleAuthLogout(ctx); err != nil || resp.Status != 200 {
		t.Fatalf("logout: resp=%#v err=%v", resp, err)
	}
}
