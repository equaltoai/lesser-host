package controlplane

import (
	"encoding/json"
	"errors"
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

func TestSetupAdminAndFinalize_ErrorBranches(t *testing.T) {
	t.Run("validateSetupCreateAdminState_conflicts_and_unauthorized", func(t *testing.T) {
		tdb := newSetupTestDB()
		s := &Server{cfg: config.Config{BootstrapWalletAddress: "0xboot"}, store: store.New(tdb.db)}

		// Already bootstrapped => conflict.
		tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.ControlPlaneConfig)
			*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "admin", BootstrappedAt: time.Now().UTC()}
			_ = dest.UpdateKeys()
		}).Once()
		if _, appErr := s.validateSetupCreateAdminState(&apptheory.Context{}); appErr == nil || appErr.Code != "app.conflict" {
			t.Fatalf("expected conflict for bootstrapped, got %#v", appErr)
		}

		// Primary admin already set => conflict.
		tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.ControlPlaneConfig)
			*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "admin"}
			_ = dest.UpdateKeys()
		}).Once()
		if _, appErr := s.validateSetupCreateAdminState(&apptheory.Context{}); appErr == nil || appErr.Code != "app.conflict" {
			t.Fatalf("expected conflict for primary admin set, got %#v", appErr)
		}

		// Missing setup session token => unauthorized.
		tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
		if _, appErr := s.validateSetupCreateAdminState(&apptheory.Context{}); appErr == nil || appErr.Code != "app.unauthorized" {
			t.Fatalf("expected unauthorized for missing setup session, got %#v", appErr)
		}
	})

	t.Run("createSetupAdminUser_conflict_and_internal", func(t *testing.T) {
		makeServer := func(createErr error) *Server {
			db := ttmocks.NewMockExtendedDBStrict()
			qUser := new(ttmocks.MockQuery)
			db.On("WithContext", mock.Anything).Return(db).Maybe()
			db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()
			qUser.On("IfNotExists").Return(qUser).Maybe()
			qUser.On("Create").Return(createErr).Once()
			return &Server{store: store.New(db)}
		}

		ctx := &apptheory.Context{}

		s := makeServer(theoryErrors.ErrConditionFailed)
		if appErr := s.createSetupAdminUser(ctx, "alice", "", time.Now().UTC()); appErr == nil || appErr.Code != "app.conflict" {
			t.Fatalf("expected conflict for username exists, got %#v", appErr)
		}

		s = makeServer(errors.New("boom"))
		if appErr := s.createSetupAdminUser(ctx, "bob", "", time.Now().UTC()); appErr == nil || appErr.Code != "app.internal" {
			t.Fatalf("expected internal error, got %#v", appErr)
		}
	})

	t.Run("verifySetupCreateAdminWallet_conflict_when_already_linked", func(t *testing.T) {
		tdb := newSetupTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.WalletChallenge)
			*dest = models.WalletChallenge{
				ID:        "wc1",
				Username:  "alice",
				Address:   strings.ToLower("0xabc"),
				ChainID:   1,
				Nonce:     "n",
				Message:   "m",
				IssuedAt:  time.Now().UTC(),
				ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
			}
			_ = dest.UpdateKeys()
		}).Once()

		tdb.qWalletIndex.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.WalletIndex)
			*dest = models.WalletIndex{Username: "someone-else"}
			dest.UpdateKeys("ethereum", "0xabc", "someone-else")
		}).Once()

		_, _, appErr := s.verifySetupCreateAdminWallet(&apptheory.Context{}, "alice", walletVerifyRequest{
			ChallengeID: "wc1",
			Address:     "0xAbc",
			Signature:   "sig",
			Message:     "m",
		})
		if appErr == nil || appErr.Code != "app.conflict" {
			t.Fatalf("expected conflict for already linked wallet, got %#v", appErr)
		}
	})

	t.Run("handleSetupFinalize_forbidden_for_non_admin_and_non_primary", func(t *testing.T) {
		tdb := newSetupTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.ControlPlaneConfig)
			*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "alice"}
			_ = dest.UpdateKeys()
		}).Maybe()

		ctx := &apptheory.Context{AuthIdentity: "bob"}
		ctx.Set(ctxKeyOperatorRole, models.RoleOperator)
		if _, err := s.handleSetupFinalize(ctx); err == nil {
			t.Fatalf("expected forbidden for non-admin")
		}

		ctx2 := &apptheory.Context{AuthIdentity: "bob"}
		ctx2.Set(ctxKeyOperatorRole, models.RoleAdmin)
		if _, err := s.handleSetupFinalize(ctx2); err == nil {
			t.Fatalf("expected forbidden for non-primary admin")
		}
	})
}

func TestParseSetupCreateAdminRequestInput_ValidatesWalletFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{name: "missing_challenge", body: `{"username":"alice","wallet":{"address":"a","signature":"s","message":"m"}}`, wantMsg: "wallet.challengeId is required"},
		{name: "missing_address", body: `{"username":"alice","wallet":{"challengeId":"c","signature":"s","message":"m"}}`, wantMsg: "wallet.address is required"},
		{name: "missing_signature", body: `{"username":"alice","wallet":{"challengeId":"c","address":"a","message":"m"}}`, wantMsg: "wallet.signature is required"},
		{name: "missing_message", body: `{"username":"alice","wallet":{"challengeId":"c","address":"a","signature":"s"}}`, wantMsg: "wallet.message is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, appErr := parseSetupCreateAdminRequestInput(&apptheory.Context{
				Request: apptheory.Request{Body: []byte(tc.body)},
			})
			if appErr == nil || appErr.Code != "app.bad_request" || !strings.Contains(appErr.Message, tc.wantMsg) {
				t.Fatalf("expected bad_request %q, got %#v", tc.wantMsg, appErr)
			}
		})
	}
}

func TestVerifySetupBootstrapChallenge_ErrorBranches(t *testing.T) {
	t.Parallel()

	// Nil server / nil ctx.
	{
		var s *Server
		if err := s.verifySetupBootstrapChallenge(&apptheory.Context{}, "boot", setupBootstrapVerifyInput{}); err == nil {
			t.Fatalf("expected internal error for nil server")
		}
		s2 := &Server{}
		if err := s2.verifySetupBootstrapChallenge(nil, "boot", setupBootstrapVerifyInput{}); err == nil {
			t.Fatalf("expected internal error for nil ctx")
		}
	}

	// Wallet mismatch.
	{
		s := &Server{}
		err := s.verifySetupBootstrapChallenge(&apptheory.Context{}, "boot", setupBootstrapVerifyInput{
			ChallengeID: "c",
			Address:     "other",
			Signature:   "s",
			Message:     "m",
		})
		if err == nil {
			t.Fatalf("expected forbidden wallet mismatch")
		}
	}

	// DB / challenge mismatches.
	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

	t.Run("challenge_not_found", func(t *testing.T) {
		tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(theoryErrors.ErrItemNotFound).Once()
		err := s.verifySetupBootstrapChallenge(ctx, "boot", setupBootstrapVerifyInput{
			ChallengeID: "c1",
			Address:     "boot",
			Signature:   "s",
			Message:     "m",
		})
		if err == nil {
			t.Fatalf("expected unauthorized")
		}
	})

	t.Run("challenge_db_error", func(t *testing.T) {
		tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(errors.New("boom")).Once()
		err := s.verifySetupBootstrapChallenge(ctx, "boot", setupBootstrapVerifyInput{
			ChallengeID: "c1",
			Address:     "boot",
			Signature:   "s",
			Message:     "m",
		})
		if err == nil {
			t.Fatalf("expected internal error")
		}
	})

	t.Run("challenge_username_mismatch", func(t *testing.T) {
		tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.WalletChallenge)
			*dest = models.WalletChallenge{ID: "c1", Username: "alice", Address: "boot", Message: "m"}
			_ = dest.UpdateKeys()
		}).Once()

		err := s.verifySetupBootstrapChallenge(ctx, "boot", setupBootstrapVerifyInput{
			ChallengeID: "c1",
			Address:     "boot",
			Signature:   "s",
			Message:     "m",
		})
		if err == nil {
			t.Fatalf("expected forbidden")
		}
	})

	t.Run("challenge_address_mismatch", func(t *testing.T) {
		tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.WalletChallenge)
			*dest = models.WalletChallenge{ID: "c1", Username: setupBootstrapUser, Address: "other", Message: "m"}
			_ = dest.UpdateKeys()
		}).Once()

		err := s.verifySetupBootstrapChallenge(ctx, "boot", setupBootstrapVerifyInput{
			ChallengeID: "c1",
			Address:     "boot",
			Signature:   "s",
			Message:     "m",
		})
		if err == nil {
			t.Fatalf("expected forbidden")
		}
	})

	t.Run("challenge_message_mismatch", func(t *testing.T) {
		tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.WalletChallenge)
			*dest = models.WalletChallenge{ID: "c1", Username: setupBootstrapUser, Address: "boot", Message: "other"}
			_ = dest.UpdateKeys()
		}).Once()

		err := s.verifySetupBootstrapChallenge(ctx, "boot", setupBootstrapVerifyInput{
			ChallengeID: "c1",
			Address:     "boot",
			Signature:   "s",
			Message:     "m",
		})
		if err == nil {
			t.Fatalf("expected forbidden")
		}
	})

	t.Run("invalid_signature", func(t *testing.T) {
		key, addr := generateWalletKey(t)
		_ = key
		bootstrapWallet := strings.ToLower(addr)

		tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.WalletChallenge)
			*dest = models.WalletChallenge{ID: "c1", Username: setupBootstrapUser, Address: bootstrapWallet, Message: "m"}
			_ = dest.UpdateKeys()
		}).Once()

		err := s.verifySetupBootstrapChallenge(ctx, bootstrapWallet, setupBootstrapVerifyInput{
			ChallengeID: "c1",
			Address:     bootstrapWallet,
			Signature:   "not-a-signature",
			Message:     "m",
		})
		if err == nil {
			t.Fatalf("expected unauthorized invalid signature")
		}
	})
}

func TestRequireSetupSession_RejectsMissingTokenAndMismatchedFields(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{cfg: config.Config{BootstrapWalletAddress: "0xboot"}, store: store.New(tdb.db)}

	// Missing auth header.
	if _, err := s.requireSetupSession(&apptheory.Context{}); err == nil {
		t.Fatalf("expected unauthorized")
	}

	// Purpose mismatch.
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.SetupSession)
		*dest = models.SetupSession{ID: "tok", Purpose: "other", WalletAddr: "0xboot", ExpiresAt: time.Now().UTC().Add(1 * time.Hour)}
		_ = dest.UpdateKeys()
	}).Once()
	if _, err := s.requireSetupSession(&apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"authorization": {"Bearer tok"}}}}); err == nil {
		t.Fatalf("expected unauthorized for purpose mismatch")
	}

	// Bootstrap wallet mismatch.
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.SetupSession)
		*dest = models.SetupSession{ID: "tok", Purpose: setupPurposeBootstrap, WalletAddr: "0xother", ExpiresAt: time.Now().UTC().Add(1 * time.Hour)}
		_ = dest.UpdateKeys()
	}).Once()
	if _, err := s.requireSetupSession(&apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"authorization": {"Bearer tok"}}}}); err == nil {
		t.Fatalf("expected unauthorized for wallet mismatch")
	}

	// Internal DB error.
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(errors.New("boom")).Once()
	if _, err := s.requireSetupSession(&apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"authorization": {"Bearer tok"}}}}); err == nil {
		t.Fatalf("expected internal error")
	}
}

func TestHandleSetupBootstrapChallenge_ConflictAndValidationBranches(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	// Already bootstrapped => conflict.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ControlPlaneConfig)
		*dest = models.ControlPlaneConfig{BootstrappedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()
	if _, err := s.handleSetupBootstrapChallenge(&apptheory.Context{Request: apptheory.Request{Body: []byte(`{"address":"0xabc"}`)}}); err == nil {
		t.Fatalf("expected conflict when already bootstrapped")
	}

	// Missing bootstrap wallet config.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.handleSetupBootstrapChallenge(&apptheory.Context{Request: apptheory.Request{Body: []byte(`{"address":"0xabc"}`)}}); err == nil {
		t.Fatalf("expected conflict when bootstrap wallet not configured")
	}

	// Missing address.
	key, addr := generateWalletKey(t)
	_ = key
	s.cfg.BootstrapWalletAddress = addr
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.handleSetupBootstrapChallenge(&apptheory.Context{Request: apptheory.Request{Body: []byte(`{"address":" "}`)}}); err == nil {
		t.Fatalf("expected bad_request when address missing")
	}
}
