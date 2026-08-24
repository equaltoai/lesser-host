package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
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

	ctx.Request.Body = []byte(`{"username":"alice","displayName":" Alice ","wallet":{"challengeId":" c ","address":" a ","signature":" s ","message":" m "}}`)
	req, appErr := parseSetupCreateAdminRequestInput(ctx)
	if appErr != nil {
		t.Fatalf("parseSetupCreateAdminRequestInput: %v", appErr)
	}
	if req.Username != testUsernameAlice || req.DisplayName != "Alice" || req.Wallet == nil || req.Wallet.ChallengeID != "c" {
		t.Fatalf("unexpected request: %#v", req)
	}

	ctx.Request.Body = []byte(`{"username":"alice","passkey":{"challenge":"pc","response":{"id":"cred"}}}`)
	req, appErr = parseSetupCreateAdminRequestInput(ctx)
	if appErr != nil {
		t.Fatalf("parseSetupCreateAdminRequestInput passkey: %v", appErr)
	}
	if req.Username != testUsernameAlice || req.Passkey == nil || req.Passkey.Challenge != "pc" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestParseSetupPasskeyRegisterBeginRequestInput(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{"username":"alice","displayName":" Root Admin "}`)}}
	req, appErr := parseSetupPasskeyRegisterBeginRequestInput(ctx)
	if appErr != nil {
		t.Fatalf("parseSetupPasskeyRegisterBeginRequestInput: %v", appErr)
	}
	if req.Username != testUsernameAlice || req.DisplayName != "Root Admin" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestParseSetupCreateAdminRequestInput_RejectsUnsafeManagedUsernames(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		" Alice ",
		"Agent-Alice",
		"agent_alice",
		"agent.alice",
		"agent@alice",
		"agent%2falice",
		"agent/alice",
		"-agent",
		"agent-",
	} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			body := fmt.Sprintf(`{"username":%q,"wallet":{"challengeId":"c","address":"a","signature":"s","message":"m"}}`, raw)
			if _, appErr := parseSetupCreateAdminRequestInput(&apptheory.Context{Request: apptheory.Request{Body: []byte(body)}}); appErr == nil || appErr.Code != appErrCodeBadRequest {
				t.Fatalf("parseSetupCreateAdminRequestInput(%q) expected bad_request, got %v", raw, appErr)
			}
		})
	}
}

type setupTestDB struct {
	db            *ttmocks.MockExtendedDB
	qCP           *ttmocks.MockQuery
	qSetup        *ttmocks.MockQuery
	qWallet       *ttmocks.MockQuery
	qWalletIndex  *ttmocks.MockQuery
	qUser         *ttmocks.MockQuery
	qCred         *ttmocks.MockQuery
	qWebAuthnChal *ttmocks.MockQuery
	qWebAuthnCred *ttmocks.MockQuery
	qAudit        *ttmocks.MockQuery
}

func newSetupTestDB() setupTestDB {
	db := ttmocks.NewMockExtendedDB()
	qCP := new(ttmocks.MockQuery)
	qSetup := new(ttmocks.MockQuery)
	qWallet := new(ttmocks.MockQuery)
	qWalletIndex := new(ttmocks.MockQuery)
	qUser := new(ttmocks.MockQuery)
	qCred := new(ttmocks.MockQuery)
	qWebAuthnChal := new(ttmocks.MockQuery)
	qWebAuthnCred := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(qCP).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SetupSession")).Return(qSetup).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletChallenge")).Return(qWallet).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(qWalletIndex).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletCredential")).Return(qCred).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(qWebAuthnChal).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnCredential")).Return(qWebAuthnCred).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qCP, qSetup, qWallet, qWalletIndex, qUser, qCred, qWebAuthnChal, qWebAuthnCred, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("WithCondition", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return setupTestDB{
		db:            db,
		qCP:           qCP,
		qSetup:        qSetup,
		qWallet:       qWallet,
		qWalletIndex:  qWalletIndex,
		qUser:         qUser,
		qCred:         qCred,
		qWebAuthnChal: qWebAuthnChal,
		qWebAuthnCred: qWebAuthnCred,
		qAudit:        qAudit,
	}
}

func makeSetupPasskeyCreationResponse(t *testing.T, challenge string) map[string]any {
	t.Helper()

	_ = challenge // registration parsing in these tests needs a valid WebAuthn shape, not challenge verification.

	const raw = `{
		"id":"6xrtBhJQW6QU4tOaB4rrHaS2Ks0yDDL_q8jDC16DEjZ-VLVf4kCRkvl2xp2D71sTPYns-exsHQHTy3G-zJRK8g",
		"rawId":"6xrtBhJQW6QU4tOaB4rrHaS2Ks0yDDL_q8jDC16DEjZ-VLVf4kCRkvl2xp2D71sTPYns-exsHQHTy3G-zJRK8g",
		"type":"public-key",
		"authenticatorAttachment":"platform",
		"clientExtensionResults":{"appid":true},
		"response":{
			"attestationObject":"o2NmbXRkbm9uZWdhdHRTdG10oGhhdXRoRGF0YVjEdKbqkhPJnC90siSSsyDPQCYqlMGpUKA5fyklC2CEHvBBAAAAAAAAAAAAAAAAAAAAAAAAAAAAQOsa7QYSUFukFOLTmgeK6x2ktirNMgwy_6vIwwtegxI2flS1X-JAkZL5dsadg-9bEz2J7PnsbB0B08txvsyUSvKlAQIDJiABIVggLKF5xS0_BntttUIrm2Z2tgZ4uQDwllbdIfrrBMABCNciWCDHwin8Zdkr56iSIh0MrB5qZiEzYLQpEOREhMUkY6q4Vw",
			"clientDataJSON":"eyJjaGFsbGVuZ2UiOiJXOEd6RlU4cEdqaG9SYldyTERsYW1BZnFfeTRTMUNaRzFWdW9lUkxBUnJFIiwib3JpZ2luIjoiaHR0cHM6Ly93ZWJhdXRobi5pbyIsInR5cGUiOiJ3ZWJhdXRobi5jcmVhdGUifQ",
			"transports":["usb","nfc","fake"]
		}
	}`

	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal registration fixture: %v", err)
	}
	return out
}

func TestHandleSetupStatus_LockedAndActive(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{cfg: config.Config{Stage: "lab", BootstrapWalletAddress: "0xBootWallet"}, store: store.New(tdb.db)}

	// Locked when config missing — bootstrap wallet address should be exposed.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	resp, err := s.handleSetupStatus(&apptheory.Context{})
	if err != nil {
		t.Fatalf("handleSetupStatus err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	var out setupStatusResponse
	if unmarshalErr := json.Unmarshal(resp.Body, &out); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if out.ControlPlaneState != "locked" || !out.Locked {
		t.Fatalf("unexpected locked status: %#v", out)
	}
	if !out.BootstrapWalletAddressSet || out.BootstrapWalletAddress != "0xBootWallet" {
		t.Fatalf("expected bootstrap wallet address to be exposed when locked, got %#v", out)
	}

	// Active when bootstrapped — bootstrap wallet address should be hidden.
	now := time.Now().UTC()
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ControlPlaneConfig)
		if !ok {
			t.Fatalf("expected *models.ControlPlaneConfig, got %T", destAny)
		}
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "admin", BootstrappedAt: now}
		_ = dest.UpdateKeys()
	}).Once()
	resp, err = s.handleSetupStatus(&apptheory.Context{})
	if err != nil || resp.Status != 200 {
		t.Fatalf("handleSetupStatus active: resp=%#v err=%v", resp, err)
	}
	var active setupStatusResponse
	if unmarshalErr := json.Unmarshal(resp.Body, &active); unmarshalErr != nil {
		t.Fatalf("unmarshal active: %v", unmarshalErr)
	}
	if !active.BootstrapWalletAddressSet {
		t.Fatalf("expected BootstrapWalletAddressSet to remain true")
	}
	if active.BootstrapWalletAddress != "" {
		t.Fatalf("expected bootstrap wallet address to be hidden after bootstrap, got %q", active.BootstrapWalletAddress)
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.WalletChallenge)
		if !ok {
			t.Fatalf("expected *models.WalletChallenge, got %T", destAny)
		}
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

func TestHandleSetupWebAuthnRegisterBegin_Success(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{
		cfg:   config.Config{BootstrapWalletAddress: "0xboot"},
		store: store.New(tdb.db),
		webAuthn: stubWebAuthnEngine{
			beginRegistration: func(user webauthn.User, _ ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
				if user.WebAuthnName() != testUsernameAlice || user.WebAuthnDisplayName() != "Alice Root" {
					t.Fatalf("unexpected WebAuthn user: name=%q display=%q", user.WebAuthnName(), user.WebAuthnDisplayName())
				}
				return &protocol.CredentialCreation{}, &webauthn.SessionData{Challenge: "setup-passkey-begin"}, nil
			},
		},
	}

	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SetupSession](t, args, 0)
		*dest = models.SetupSession{
			ID:         "setup-token",
			Purpose:    setupPurposeBootstrap,
			WalletAddr: "0xboot",
			IssuedAt:   time.Now().UTC(),
			ExpiresAt:  time.Now().UTC().Add(1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qWebAuthnChal.On("Create").Return(nil).Once()

	body := []byte(`{"username":"alice","displayName":"Alice Root"}`)
	resp, err := s.handleSetupWebAuthnRegisterBegin(&apptheory.Context{
		Request: apptheory.Request{
			Body:    body,
			Headers: map[string][]string{"authorization": {"Bearer setup-token"}},
		},
	})
	if err != nil {
		t.Fatalf("handleSetupWebAuthnRegisterBegin err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out webAuthnBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Challenge != "setup-passkey-begin" {
		t.Fatalf("unexpected challenge: %#v", out)
	}
}

func TestP0_SetupWebAuthnRegisterBegin_RequiresSetupSession(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{
		cfg:   config.Config{BootstrapWalletAddress: "0xboot"},
		store: store.New(tdb.db),
		webAuthn: stubWebAuthnEngine{
			beginRegistration: func(_ webauthn.User, _ ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
				t.Fatal("BeginRegistration must not run without a setup session")
				return nil, nil, nil
			},
		},
	}

	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()

	body := []byte(`{"username":"alice","displayName":"Alice Root"}`)
	resp, err := s.handleSetupWebAuthnRegisterBegin(&apptheory.Context{
		Request: apptheory.Request{Body: body},
	})
	if resp != nil {
		t.Fatalf("expected no response, got %#v", resp)
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected *apptheory.AppTheoryError, got %T: %v", err, err)
	}
	if appErr.Code != testProvisionConsentCodeUnauthorized {
		t.Fatalf("expected unauthorized, got %#v", appErr)
	}
	tdb.qSetup.AssertNotCalled(t, "First", mock.Anything)
}

func TestRequireSetupSession_ExpiredDeletesAndUnauthorized(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{cfg: config.Config{BootstrapWalletAddress: "0xabc"}, store: store.New(tdb.db)}

	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.SetupSession)
		if !ok {
			t.Fatalf("expected *models.SetupSession, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.SetupSession)
		if !ok {
			t.Fatalf("expected *models.SetupSession, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.WalletChallenge)
		if !ok {
			t.Fatalf("expected *models.WalletChallenge, got %T", destAny)
		}
		*dest = models.WalletChallenge{
			ID:        "wc1",
			Username:  testUsernameAlice,
			Address:   strings.ToLower(addr),
			ChainID:   1,
			Nonce:     "n",
			Message:   msg,
			IssuedAt:  time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		}
		_ = dest.UpdateKeys()
	}).Once()
	// Wallet not linked yet.
	tdb.qWalletIndex.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	tdb.qUser.On("Create").Return(nil).Once()
	tdb.qCred.On("Create").Return(nil).Once()
	tdb.qWalletIndex.On("Create").Return(nil).Once()
	tdb.qCP.On("CreateOrUpdate").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	body, _ := json.Marshal(setupCreateAdminRequest{
		Username:    testUsernameAlice,
		DisplayName: "Alice",
		Wallet: &walletVerifyRequest{
			ChallengeID: "wc1",
			Address:     addr,
			Signature:   sig,
			Message:     msg,
		},
	})
	ctx := &apptheory.Context{
		RequestID:    "rid",
		Request:      apptheory.Request{Body: body, Headers: map[string][]string{"authorization": {"Bearer setup-token"}}},
		AuthIdentity: testUsernameAlice, // unused by create_admin but present
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ControlPlaneConfig)
		if !ok {
			t.Fatalf("expected *models.ControlPlaneConfig, got %T", destAny)
		}
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: testUsernameAlice}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qCP.On("CreateOrUpdate").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()
	tdb.qWebAuthnCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = []*models.WebAuthnCredential{{ID: "cred1", UserID: testUsernameAlice}}
	}).Once()

	ctx2 := &apptheory.Context{AuthIdentity: testUsernameAlice, RequestID: "rid2"}
	ctx2.Set(ctxKeyOperatorRole, models.RoleAdmin)
	resp, err = s.handleSetupFinalize(ctx2)
	if err != nil {
		t.Fatalf("handleSetupFinalize err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestP0_SetupCreateAdminPasskey_RequiresSetupSession(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{
		cfg:   config.Config{BootstrapWalletAddress: "0xboot"},
		store: store.New(tdb.db),
		webAuthn: stubWebAuthnEngine{
			createCredential: func(_ webauthn.User, _ webauthn.SessionData, _ *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
				t.Fatal("CreateCredential must not run without a setup session")
				return nil, nil
			},
		},
	}
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(setupCreateAdminRequest{
		Username: testUsernameAlice,
		Passkey: &webAuthnFinishRegistrationRequest{
			Challenge: "pc1",
			Response:  makeSetupPasskeyCreationResponse(t, "pc1"),
		},
	})
	resp, err := s.handleSetupCreateAdmin(&apptheory.Context{Request: apptheory.Request{Body: body}})
	if resp != nil {
		t.Fatalf("expected no response, got %#v", resp)
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected *apptheory.AppTheoryError, got %T: %v", err, err)
	}
	if appErr.Code != testProvisionConsentCodeUnauthorized {
		t.Fatalf("expected unauthorized, got %#v", appErr)
	}
}

func TestP0_SetupCreateAdminPasskey_RejectsPreexistingAdminBeforeChallengeConsumption(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{
		cfg:      config.Config{BootstrapWalletAddress: "0xboot"},
		store:    store.New(tdb.db),
		webAuthn: stubWebAuthnEngine{},
	}
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SetupSession](t, args, 0)
		*dest = models.SetupSession{
			ID:         "setup-token",
			Purpose:    setupPurposeBootstrap,
			WalletAddr: "0xboot",
			IssuedAt:   time.Now().UTC(),
			ExpiresAt:  time.Now().UTC().Add(1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{
			Username:       testUsernameAlice,
			Role:           models.RoleAdmin,
			Approved:       true,
			ApprovalStatus: models.UserApprovalStatusApproved,
			CreatedAt:      time.Now().UTC().Add(-1 * time.Minute),
		}
		_ = dest.UpdateKeys()
	}).Once()

	body, _ := json.Marshal(setupCreateAdminRequest{
		Username: testUsernameAlice,
		Passkey: &webAuthnFinishRegistrationRequest{
			Challenge: "pc1",
			Response:  makeSetupPasskeyCreationResponse(t, "pc1"),
		},
	})
	resp, err := s.handleSetupCreateAdmin(&apptheory.Context{
		Request: apptheory.Request{
			Body:    body,
			Headers: map[string][]string{"authorization": {"Bearer setup-token"}},
		},
	})
	if resp != nil {
		t.Fatalf("expected no response, got %#v", resp)
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected *apptheory.AppTheoryError, got %T: %v", err, err)
	}
	if appErr.Code != appErrCodeConflict || !strings.Contains(appErr.Message, "resolve partial setup state") {
		t.Fatalf("expected named conflict, got %#v", appErr)
	}
	tdb.qWebAuthnChal.AssertNotCalled(t, "First", mock.Anything)
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
}

func TestP0_SetupCreateAdminPasskey_DoesNotLinkBootstrapWalletCredential(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	tx := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tx
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	s := &Server{
		cfg:   config.Config{BootstrapWalletAddress: "0xboot"},
		store: store.New(tdb.db),
		webAuthn: stubWebAuthnEngine{
			createCredential: func(_ webauthn.User, _ webauthn.SessionData, _ *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
				return &webauthn.Credential{
					ID:              []byte("setup-passkey-cred"),
					PublicKey:       []byte("public-key"),
					AttestationType: "none",
					Authenticator: webauthn.Authenticator{
						AAGUID:    make([]byte, 16),
						SignCount: 1,
					},
					Flags: webauthn.CredentialFlags{
						UserPresent:  true,
						UserVerified: true,
					},
				}, nil
			},
		},
	}

	stubSetupPasskeyAdminCreationPrereqs(t, tdb)

	createdKinds := map[string]bool{}
	tx.On("Create", mock.Anything, mock.Anything).Return(tx).Times(4).Run(func(args mock.Arguments) {
		assertSetupPasskeyCreateAdminItem(t, args.Get(0), createdKinds)
	})
	auditActions := map[string]bool{}
	tx.On("Put", mock.Anything, mock.Anything).Return(tx).Twice().Run(func(args mock.Arguments) {
		audit := testutil.RequireMockArg[*models.AuditLogEntry](t, args, 0)
		auditActions[audit.Action] = true
	})
	tx.On("Delete", mock.Anything, mock.Anything).Return(tx).Once().Run(func(args mock.Arguments) {
		assertSetupPasskeyChallengeDelete(t, args)
	})

	body, _ := json.Marshal(setupCreateAdminRequest{
		Username:    testUsernameAlice,
		DisplayName: "Alice",
		Passkey: &webAuthnFinishRegistrationRequest{
			Challenge:      "pc1",
			Response:       makeSetupPasskeyCreationResponse(t, "pc1"),
			CredentialName: "Setup Admin Passkey",
		},
	})
	resp, err := s.handleSetupCreateAdmin(&apptheory.Context{
		RequestID: "rid-passkey",
		Request: apptheory.Request{
			Body:    body,
			Headers: map[string][]string{"authorization": {"Bearer setup-token"}},
		},
	})
	if err != nil {
		t.Fatalf("handleSetupCreateAdmin passkey err: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("expected 201, got %d", resp.Status)
	}

	var out setupCreateAdminResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Username != testUsernameAlice || out.Token == "" || out.Method != "webauthn" || out.Role != models.RoleAdmin {
		t.Fatalf("unexpected response: %#v", out)
	}
	if !createdKinds["user"] || !createdKinds["webauthn_credential"] || !createdKinds["operator_session"] || !createdKinds["control_plane"] {
		t.Fatalf("missing transaction writes: %#v", createdKinds)
	}
	if !auditActions["setup.create_admin"] || !auditActions["auth.webauthn.register"] {
		t.Fatalf("expected setup + passkey audit entries, got %#v", auditActions)
	}
	tdb.qCred.AssertNotCalled(t, "Create")
	tdb.qWalletIndex.AssertNotCalled(t, "Create")
}

func TestHandleSetupCreateAdminWithPasskey_FailsWhenPasskeyAuditKeysInvalid(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{
		cfg:   config.Config{BootstrapWalletAddress: "0xboot"},
		store: store.New(tdb.db),
		webAuthn: stubWebAuthnEngine{
			createCredential: func(_ webauthn.User, _ webauthn.SessionData, _ *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
				return &webauthn.Credential{
					ID:              bytes.Repeat([]byte("x"), 780),
					PublicKey:       []byte("public-key"),
					AttestationType: "none",
					Authenticator: webauthn.Authenticator{
						AAGUID:    make([]byte, 16),
						SignCount: 1,
					},
					Flags: webauthn.CredentialFlags{
						UserPresent:  true,
						UserVerified: true,
					},
				}, nil
			},
		},
	}

	stubSetupPasskeyAdminCreationPrereqs(t, tdb)

	body, _ := json.Marshal(setupCreateAdminRequest{
		Username:    testUsernameAlice,
		DisplayName: "Alice",
		Passkey: &webAuthnFinishRegistrationRequest{
			Challenge:      "pc1",
			Response:       makeSetupPasskeyCreationResponse(t, "pc1"),
			CredentialName: "Setup Admin Passkey",
		},
	})
	resp, err := s.handleSetupCreateAdmin(&apptheory.Context{
		RequestID: "rid-passkey-audit-too-long",
		Request: apptheory.Request{
			Body:    body,
			Headers: map[string][]string{"authorization": {"Bearer setup-token"}},
		},
	})
	if resp != nil {
		t.Fatalf("expected no response, got %#v", resp)
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected *apptheory.AppTheoryError, got %T: %v", err, err)
	}
	if appErr.Code != appErrCodeInternal || appErr.Message != "internal error" {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
}

func stubSetupPasskeyAdminCreationPrereqs(t *testing.T, tdb setupTestDB) {
	t.Helper()

	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SetupSession](t, args, 0)
		*dest = models.SetupSession{
			ID:         "setup-token",
			Purpose:    setupPurposeBootstrap,
			WalletAddr: "0xboot",
			IssuedAt:   time.Now().UTC(),
			ExpiresAt:  time.Now().UTC().Add(1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qWebAuthnChal.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge:   "pc1",
			UserID:      testUsernameAlice,
			Type:        "registration",
			SessionData: []byte(`{}`),
			ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
		}
		_ = dest.UpdateKeys()
	}).Once()
}

func assertSetupPasskeyCreateAdminItem(t *testing.T, raw any, createdKinds map[string]bool) {
	t.Helper()

	switch item := raw.(type) {
	case *models.User:
		createdKinds["user"] = true
		if item.Username != testUsernameAlice || item.Role != models.RoleAdmin {
			t.Fatalf("unexpected setup admin user: %#v", item)
		}
	case *models.WebAuthnCredential:
		createdKinds["webauthn_credential"] = true
		if item.UserID != testUsernameAlice || item.Name != "Setup Admin Passkey" {
			t.Fatalf("unexpected stored passkey: %#v", item)
		}
	case *models.OperatorSession:
		createdKinds["operator_session"] = true
		if item.Username != testUsernameAlice || item.Role != models.RoleAdmin || item.Method != testSessionMethodWebAuthn {
			t.Fatalf("unexpected operator session: %#v", item)
		}
	case *models.ControlPlaneConfig:
		createdKinds["control_plane"] = true
		if item.PrimaryAdminUsername != testUsernameAlice {
			t.Fatalf("unexpected control plane config: %#v", item)
		}
	default:
		t.Fatalf("unexpected transaction create item type %T", raw)
	}
}

func assertSetupPasskeyChallengeDelete(t *testing.T, args mock.Arguments) {
	t.Helper()

	challenge := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
	if challenge.Challenge != "pc1" {
		t.Fatalf("unexpected deleted challenge: %#v", challenge)
	}
}

func TestHandleSetupCreateAdmin_RejectsBootstrapWalletAsPrimaryAdmin(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	_, bootstrapAddr := generateWalletKey(t)
	s := &Server{cfg: config.Config{BootstrapWalletAddress: strings.ToUpper(bootstrapAddr)}, store: store.New(tdb.db)}

	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SetupSession](t, args, 0)
		*dest = models.SetupSession{
			ID:         "setup-token",
			Purpose:    setupPurposeBootstrap,
			WalletAddr: strings.ToLower(bootstrapAddr),
			IssuedAt:   time.Now().UTC(),
			ExpiresAt:  time.Now().UTC().Add(1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()

	body, _ := json.Marshal(setupCreateAdminRequest{
		Username:    testUsernameAlice,
		DisplayName: "Alice",
		Wallet: &walletVerifyRequest{
			ChallengeID: "wc1",
			Address:     strings.ToLower(bootstrapAddr),
			Signature:   "sig",
			Message:     "msg",
		},
	})

	resp, err := s.handleSetupCreateAdmin(&apptheory.Context{
		Request: apptheory.Request{Body: body, Headers: map[string][]string{"authorization": {"Bearer setup-token"}}},
	})
	if resp != nil {
		t.Fatalf("expected no response, got %#v", resp)
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected *apptheory.AppTheoryError, got %T: %v", err, err)
	}
	if appErr.Code != appErrCodeForbidden || !strings.Contains(appErr.Message, "one-time setup authority") {
		t.Fatalf("expected explicit forbidden bootstrap wallet error, got %#v", appErr)
	}
	tdb.qWallet.AssertNotCalled(t, "First", mock.Anything)
	tdb.qUser.AssertNotCalled(t, "Create")
}

func TestValidateSetupCreateAdminState_ConflictsAndUnauthorized(t *testing.T) {
	tdb := newSetupTestDB()
	s := &Server{cfg: config.Config{BootstrapWalletAddress: "0xboot"}, store: store.New(tdb.db)}

	// Already bootstrapped => conflict.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ControlPlaneConfig)
		if !ok {
			t.Fatalf("expected *models.ControlPlaneConfig, got %T", destAny)
		}
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "admin", BootstrappedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()
	if _, appErr := s.validateSetupCreateAdminState(&apptheory.Context{}); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for bootstrapped, got %#v", appErr)
	}

	// Primary admin already set => conflict.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ControlPlaneConfig)
		if !ok {
			t.Fatalf("expected *models.ControlPlaneConfig, got %T", destAny)
		}
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "admin"}
		_ = dest.UpdateKeys()
	}).Once()
	if _, appErr := s.validateSetupCreateAdminState(&apptheory.Context{}); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for primary admin set, got %#v", appErr)
	}

	// Missing setup session token => unauthorized.
	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, appErr := s.validateSetupCreateAdminState(&apptheory.Context{}); appErr == nil || appErr.Code != testProvisionConsentCodeUnauthorized {
		t.Fatalf("expected unauthorized for missing setup session, got %#v", appErr)
	}
}

func TestCreateSetupAdminUser_ConflictAndInternal(t *testing.T) {
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
	if appErr := s.createSetupAdminUser(ctx, testUsernameAlice, "", time.Now().UTC()); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for username exists, got %#v", appErr)
	}

	s = makeServer(errors.New("boom"))
	if appErr := s.createSetupAdminUser(ctx, "bob", "", time.Now().UTC()); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
}

func TestVerifySetupCreateAdminWallet_ConflictWhenAlreadyLinked(t *testing.T) {
	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.WalletChallenge)
		if !ok {
			t.Fatalf("expected *models.WalletChallenge, got %T", destAny)
		}
		*dest = models.WalletChallenge{
			ID:        "wc1",
			Username:  testUsernameAlice,
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.WalletIndex)
		if !ok {
			t.Fatalf("expected *models.WalletIndex, got %T", destAny)
		}
		*dest = models.WalletIndex{Username: "someone-else"}
		dest.UpdateKeys("ethereum", "0xabc", "someone-else")
	}).Once()

	_, _, appErr := s.verifySetupCreateAdminWallet(&apptheory.Context{}, testUsernameAlice, walletVerifyRequest{
		ChallengeID: "wc1",
		Address:     "0xAbc",
		Signature:   "sig",
		Message:     "m",
	})
	if appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for already linked wallet, got %#v", appErr)
	}
}

func TestHandleSetupFinalize_ForbiddenForNonAdminAndNonPrimary(t *testing.T) {
	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ControlPlaneConfig)
		if !ok {
			t.Fatalf("expected *models.ControlPlaneConfig, got %T", destAny)
		}
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: testUsernameAlice}
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
}

func TestHandleSetupFinalize_RequiresPrimaryAdminPasskey(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ControlPlaneConfig](t, args, 0)
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: testUsernameAlice}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qWebAuthnCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = nil
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: testUsernameAlice, RequestID: "rid"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	resp, err := s.handleSetupFinalize(ctx)
	if resp != nil {
		t.Fatalf("expected no response, got %#v", resp)
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected *apptheory.AppTheoryError, got %T: %v", err, err)
	}
	if appErr.Code != appErrCodeConflict || !strings.Contains(appErr.Message, "passkey is required") {
		t.Fatalf("expected passkey conflict, got %#v", appErr)
	}
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
			assertSetupCreateAdminParseError(t, tc.body, tc.wantMsg)
		})
	}
}

func TestParseSetupCreateAdminRequestInput_ValidatesPasskeyFieldsAndModeSelection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{name: "missing_mode", body: `{"username":"alice"}`, wantMsg: "exactly one admin credential path is required"},
		{name: "both_modes", body: `{"username":"alice","wallet":{"challengeId":"c","address":"a","signature":"s","message":"m"},"passkey":{"challenge":"pc","response":{"id":"cred"}}}`, wantMsg: "exactly one admin credential path is required"},
		{name: "missing_passkey_challenge", body: `{"username":"alice","passkey":{"response":{"id":"cred"}}}`, wantMsg: "passkey.challenge is required"},
		{name: "missing_passkey_response", body: `{"username":"alice","passkey":{"challenge":"pc"}}`, wantMsg: "passkey.response is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertSetupCreateAdminParseError(t, tc.body, tc.wantMsg)
		})
	}
}

func assertSetupCreateAdminParseError(t *testing.T, body string, wantMsg string) {
	t.Helper()

	_, appErr := parseSetupCreateAdminRequestInput(&apptheory.Context{
		Request: apptheory.Request{Body: []byte(body)},
	})
	if appErr == nil || appErr.Code != appErrCodeBadRequest || !strings.Contains(appErr.Message, wantMsg) {
		t.Fatalf("expected bad_request %q, got %#v", wantMsg, appErr)
	}
}

func TestVerifySetupBootstrapChallenge_ReturnsInternalErrorForNilServerOrCtx(t *testing.T) {
	t.Parallel()

	var s *Server
	if err := s.verifySetupBootstrapChallenge(&apptheory.Context{}, "boot", setupBootstrapVerifyInput{}); err == nil {
		t.Fatalf("expected internal error for nil server")
	}

	s2 := &Server{}
	if err := s2.verifySetupBootstrapChallenge(nil, "boot", setupBootstrapVerifyInput{}); err == nil {
		t.Fatalf("expected internal error for nil ctx")
	}
}

func TestVerifySetupBootstrapChallenge_ForbiddenWhenWalletMismatch(t *testing.T) {
	t.Parallel()

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

func TestVerifySetupBootstrapChallenge_ChallengeNotFound(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

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
}

func TestVerifySetupBootstrapChallenge_ChallengeDBError(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

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
}

func TestVerifySetupBootstrapChallenge_ChallengeUsernameMismatch(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
		*dest = models.WalletChallenge{ID: "c1", Username: testUsernameAlice, Address: "boot", Message: "m"}
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
}

func TestVerifySetupBootstrapChallenge_ChallengeAddressMismatch(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
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
}

func TestVerifySetupBootstrapChallenge_ChallengeMessageMismatch(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
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
}

func TestVerifySetupBootstrapChallenge_InvalidSignature(t *testing.T) {
	t.Parallel()

	tdb := newSetupTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

	key, addr := generateWalletKey(t)
	_ = key
	bootstrapWallet := strings.ToLower(addr)

	tdb.qWallet.On("First", mock.AnythingOfType("*models.WalletChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.SetupSession)
		if !ok {
			t.Fatalf("expected *models.SetupSession, got %T", destAny)
		}
		*dest = models.SetupSession{ID: "tok", Purpose: "other", WalletAddr: "0xboot", ExpiresAt: time.Now().UTC().Add(1 * time.Hour)}
		_ = dest.UpdateKeys()
	}).Once()
	if _, err := s.requireSetupSession(&apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"authorization": {"Bearer tok"}}}}); err == nil {
		t.Fatalf("expected unauthorized for purpose mismatch")
	}

	// Bootstrap wallet mismatch.
	tdb.qSetup.On("First", mock.AnythingOfType("*models.SetupSession")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.SetupSession)
		if !ok {
			t.Fatalf("expected *models.SetupSession, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ControlPlaneConfig)
		if !ok {
			t.Fatalf("expected *models.ControlPlaneConfig, got %T", destAny)
		}
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
