package controlplane

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type webAuthnFinishTestDB struct {
	db         *ttmocks.MockExtendedDB
	qCred      *ttmocks.MockQuery
	qChallenge *ttmocks.MockQuery
	qAudit     *ttmocks.MockQuery
	qUser      *ttmocks.MockQuery
	qSession   *ttmocks.MockQuery
}

func newWebAuthnFinishTestDB() webAuthnFinishTestDB {
	db := ttmocks.NewMockExtendedDB()
	qCred := new(ttmocks.MockQuery)
	qChallenge := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qUser := new(ttmocks.MockQuery)
	qSession := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnCredential")).Return(qCred).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(qChallenge).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()
	db.On("Model", mock.AnythingOfType("*models.OperatorSession")).Return(qSession).Maybe()

	for _, q := range []*ttmocks.MockQuery{qCred, qChallenge, qAudit, qUser, qSession} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return webAuthnFinishTestDB{
		db:         db,
		qCred:      qCred,
		qChallenge: qChallenge,
		qAudit:     qAudit,
		qUser:      qUser,
		qSession:   qSession,
	}
}

type stubWebAuthnFinishEngine struct {
	validateLogin func(user webauthn.User, session webauthn.SessionData, parsedResponse *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error)
}

func (s stubWebAuthnFinishEngine) BeginRegistration(_ webauthn.User, _ ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	return &protocol.CredentialCreation{}, &webauthn.SessionData{Challenge: "c"}, nil
}

func (s stubWebAuthnFinishEngine) CreateCredential(_ webauthn.User, _ webauthn.SessionData, _ *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
	return nil, errors.New("CreateCredential not implemented")
}

func (s stubWebAuthnFinishEngine) BeginLogin(_ webauthn.User, _ ...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return &protocol.CredentialAssertion{}, &webauthn.SessionData{Challenge: "c"}, nil
}

func (s stubWebAuthnFinishEngine) ValidateLogin(user webauthn.User, session webauthn.SessionData, parsedResponse *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error) {
	if s.validateLogin == nil {
		return nil, errors.New("validateLogin not set")
	}
	return s.validateLogin(user, session, parsedResponse)
}

func TestHandleWebAuthnLoginFinish_Success(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnFinishTestDB()
	s := &Server{
		store: store.New(tdb.db),
		webAuthn: stubWebAuthnFinishEngine{
			validateLogin: func(_ webauthn.User, _ webauthn.SessionData, _ *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error) {
				return &webauthn.Credential{
					ID: []byte("id"),
					Authenticator: webauthn.Authenticator{
						SignCount: 2,
					},
					Flags: webauthn.CredentialFlags{
						BackupEligible: true,
						BackupState:    true,
					},
				}, nil
			},
		},
	}

	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge:   "c1",
			UserID:      "alice",
			Type:        "login",
			SessionData: []byte(`{}`),
			ExpiresAt:   time.Now().UTC().Add(1 * time.Minute),
		}
	}).Once()

	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = []*models.WebAuthnCredential{
			{ID: base64.StdEncoding.EncodeToString([]byte("id")), UserID: "alice"},
		}
	}).Once()

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: "admin"}
		_ = dest.UpdateKeys()
	}).Once()

	idRaw := base64.RawURLEncoding.EncodeToString([]byte("id"))
	clientData := base64.RawURLEncoding.EncodeToString([]byte(`{"type":"webauthn.get","challenge":"c","origin":"https://example.com"}`))
	authenticatorData := base64.RawURLEncoding.EncodeToString(make([]byte, 37))
	signature := base64.RawURLEncoding.EncodeToString([]byte("sig"))

	req := webAuthnFinishLoginRequest{
		Username:  "alice",
		Challenge: "c1",
		Response: map[string]any{
			"id":    idRaw,
			"rawId": idRaw,
			"type":  "public-key",
			"response": map[string]any{
				"clientDataJSON":    clientData,
				"authenticatorData": authenticatorData,
				"signature":         signature,
			},
		},
		DeviceName: "laptop",
	}
	body, _ := json.Marshal(req)

	resp, err := s.handleWebAuthnLoginFinish(&apptheory.Context{
		RequestID: "rid",
		Request:   apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("handleWebAuthnLoginFinish: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200, got %#v", resp)
	}

	var out operatorLoginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Username != "alice" || out.Method != "webauthn" || out.Token == "" {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandleWebAuthnRegisterFinish_InvalidResponse(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnFinishTestDB()
	s := &Server{
		store:    store.New(tdb.db),
		webAuthn: stubWebAuthnFinishEngine{},
	}

	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge:   "c1",
			UserID:      "alice",
			Type:        "registration",
			SessionData: []byte(`{}`),
			ExpiresAt:   time.Now().UTC().Add(1 * time.Minute),
		}
	}).Once()

	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = nil
	}).Once()

	req := webAuthnFinishRegistrationRequest{
		Challenge:      "c1",
		Response:       map[string]any{},
		CredentialName: "key",
	}
	body, _ := json.Marshal(req)

	_, err := s.handleWebAuthnRegisterFinish(&apptheory.Context{
		AuthIdentity: "alice",
		Request:      apptheory.Request{Body: body},
	})
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request, got %T: %v", err, err)
	}
}
