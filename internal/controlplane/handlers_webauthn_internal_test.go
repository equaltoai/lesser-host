package controlplane

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type webAuthnTestDB struct {
	db         *ttmocks.MockExtendedDB
	qCred      *ttmocks.MockQuery
	qChallenge *ttmocks.MockQuery
	qAudit     *ttmocks.MockQuery
}

func newWebAuthnTestDB() webAuthnTestDB {
	db := ttmocks.NewMockExtendedDB()
	qCred := new(ttmocks.MockQuery)
	qChallenge := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnCredential")).Return(qCred).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(qChallenge).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qCred, qChallenge, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return webAuthnTestDB{
		db:         db,
		qCred:      qCred,
		qChallenge: qChallenge,
		qAudit:     qAudit,
	}
}

type stubWebAuthnEngine struct {
	beginRegistration func(user webauthn.User, opts ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error)
	beginLogin        func(user webauthn.User, opts ...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error)
}

func (s stubWebAuthnEngine) BeginRegistration(user webauthn.User, opts ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	return s.beginRegistration(user, opts...)
}

func (s stubWebAuthnEngine) CreateCredential(_ webauthn.User, _ webauthn.SessionData, _ *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
	return nil, nil
}

func (s stubWebAuthnEngine) BeginLogin(user webauthn.User, opts ...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return s.beginLogin(user, opts...)
}

func (s stubWebAuthnEngine) ValidateLogin(_ webauthn.User, _ webauthn.SessionData, _ *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error) {
	return nil, nil
}

func TestEnsureWebAuthnConfigured(t *testing.T) {
	t.Parallel()

	if err := (*Server)(nil).ensureWebAuthnConfigured(); err == nil {
		t.Fatalf("expected error")
	}
	if err := (&Server{}).ensureWebAuthnConfigured(); err == nil {
		t.Fatalf("expected error")
	}

	if err := (&Server{webAuthn: stubWebAuthnEngine{}}).ensureWebAuthnConfigured(); err != nil {
		t.Fatalf("expected configured, got %v", err)
	}
}

func TestWebAuthnUser_Methods(t *testing.T) {
	t.Parallel()

	u := &webAuthnUser{
		id:          testUsernameAlice,
		name:        testUsernameAlice,
		displayName: "Alice",
		credentials: []webauthn.Credential{{ID: []byte("id")}},
	}

	if string(u.WebAuthnID()) != testUsernameAlice {
		t.Fatalf("unexpected id: %q", string(u.WebAuthnID()))
	}
	if u.WebAuthnName() != testUsernameAlice {
		t.Fatalf("unexpected name: %q", u.WebAuthnName())
	}
	if u.WebAuthnDisplayName() != "Alice" {
		t.Fatalf("unexpected display name: %q", u.WebAuthnDisplayName())
	}
	if u.WebAuthnIcon() != "" {
		t.Fatalf("unexpected icon: %q", u.WebAuthnIcon())
	}
	if creds := u.WebAuthnCredentials(); len(creds) != 1 || string(creds[0].ID) != "id" {
		t.Fatalf("unexpected creds: %#v", creds)
	}
}

func TestLoadWebAuthnSession_UnauthorizedWhenNotFound(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{}
	if _, err := s.loadWebAuthnSession(ctx, "challenge", testUsernameAlice, "login"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestLoadWebAuthnSession_ValidatesUserAndTypeAndSessionJSON(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge:   "c1",
			UserID:      "bob",
			Type:        "login",
			SessionData: []byte(`{}`),
		}
	}).Once()
	ctx := &apptheory.Context{}
	if _, err := s.loadWebAuthnSession(ctx, "c1", testUsernameAlice, "login"); err == nil {
		t.Fatalf("expected unauthorized for user mismatch")
	}

	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge:   "c2",
			UserID:      testUsernameAlice,
			Type:        "registration",
			SessionData: []byte(`{}`),
		}
	}).Once()
	if _, err := s.loadWebAuthnSession(ctx, "c2", testUsernameAlice, "login"); err == nil {
		t.Fatalf("expected unauthorized for type mismatch")
	}

	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge:   "c3",
			UserID:      testUsernameAlice,
			Type:        "login",
			SessionData: []byte(`not-json`),
		}
	}).Once()
	if _, err := s.loadWebAuthnSession(ctx, "c3", testUsernameAlice, "login"); err == nil {
		t.Fatalf("expected invalid session error")
	}
}

func TestGetWebAuthnChallenge_ExpiredDeletesAndNotFound(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge: "expired",
			UserID:    testUsernameAlice,
			Type:      "login",
			ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
		}
	}).Once()
	tdb.qChallenge.On("Delete").Return(nil).Once()

	ctx := &apptheory.Context{}
	_, err := s.getWebAuthnChallenge(ctx, "expired")
	if !theoryErrors.IsNotFound(err) {
		t.Fatalf("expected not_found, got %v", err)
	}
}

func TestHandleWebAuthnCredentials_UnauthorizedAndSuccess(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db), webAuthn: stubWebAuthnEngine{}}

	if _, err := s.handleWebAuthnCredentials(&apptheory.Context{}); err == nil {
		t.Fatalf("expected unauthorized")
	}

	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = []*models.WebAuthnCredential{
			{ID: "cred1", Name: "My Passkey", CreatedAt: time.Unix(10, 0).UTC(), LastUsedAt: time.Unix(11, 0).UTC()},
		}
	}).Once()

	resp, err := s.handleWebAuthnCredentials(&apptheory.Context{AuthIdentity: testUsernameAlice})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestHandleWebAuthnUpdateCredential_ValidatesInputAndUpdates(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db), webAuthn: stubWebAuthnEngine{}}

	// Missing path param.
	if _, err := s.handleWebAuthnUpdateCredential(&apptheory.Context{AuthIdentity: testUsernameAlice, Request: apptheory.Request{Body: []byte(`{}`)}}); err == nil {
		t.Fatalf("expected bad_request")
	}

	// Missing name.
	if _, err := s.handleWebAuthnUpdateCredential(&apptheory.Context{
		AuthIdentity: testUsernameAlice,
		Params:       map[string]string{"credentialId": "c1"},
		Request:      apptheory.Request{Body: []byte(`{}`)},
	}); err == nil {
		t.Fatalf("expected bad_request")
	}

	tdb.qCred.On("Update", mock.Anything).Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()
	resp, err := s.handleWebAuthnUpdateCredential(&apptheory.Context{
		AuthIdentity: testUsernameAlice,
		RequestID:    "r1",
		Params:       map[string]string{"credentialId": "c1"},
		Request:      apptheory.Request{Body: []byte(`{"name":" New "}`)},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestHandleWebAuthnDeleteCredential_Idempotent(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db), webAuthn: stubWebAuthnEngine{}}

	// Missing credentialId.
	if _, err := s.handleWebAuthnDeleteCredential(&apptheory.Context{AuthIdentity: testUsernameAlice}); err == nil {
		t.Fatalf("expected bad_request")
	}

	tdb.qCred.On("Delete").Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qAudit.On("Create").Return(nil).Once()
	resp, err := s.handleWebAuthnDeleteCredential(&apptheory.Context{
		AuthIdentity: testUsernameAlice,
		Params:       map[string]string{"credentialId": "c1"},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestHandleWebAuthnRegisterBegin_StoresChallenge(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	engine := stubWebAuthnEngine{
		beginRegistration: func(_ webauthn.User, _ ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
			sd := &webauthn.SessionData{Challenge: "c1"}
			return &protocol.CredentialCreation{}, sd, nil
		},
	}
	s := &Server{store: store.New(tdb.db), webAuthn: engine}

	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		// Include a credential to exercise base64 decoding path.
		*dest = []*models.WebAuthnCredential{{ID: base64.StdEncoding.EncodeToString([]byte("id"))}}
	}).Once()
	tdb.qChallenge.On("Create").Return(nil).Once()

	resp, err := s.handleWebAuthnRegisterBegin(&apptheory.Context{AuthIdentity: testUsernameAlice})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out webAuthnBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Challenge != "c1" {
		t.Fatalf("expected challenge c1, got %#v", out)
	}
}

func TestHandleWebAuthnLoginBegin_RequiresCredentialsAndStoresChallenge(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	engine := stubWebAuthnEngine{
		beginLogin: func(_ webauthn.User, _ ...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
			sd := &webauthn.SessionData{Challenge: "c2"}
			return &protocol.CredentialAssertion{}, sd, nil
		},
	}
	s := &Server{store: store.New(tdb.db), webAuthn: engine}

	// No credentials => not_found.
	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = nil
	}).Once()
	if _, err := s.handleWebAuthnLoginBegin(&apptheory.Context{Request: apptheory.Request{Body: []byte(`{"username":"alice"}`)}}); err == nil {
		t.Fatalf("expected not_found")
	}

	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = []*models.WebAuthnCredential{{ID: base64.StdEncoding.EncodeToString([]byte("id"))}}
	}).Once()
	tdb.qChallenge.On("Create").Return(nil).Once()

	resp, err := s.handleWebAuthnLoginBegin(&apptheory.Context{Request: apptheory.Request{Body: []byte(`{"username":"alice"}`)}})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}
