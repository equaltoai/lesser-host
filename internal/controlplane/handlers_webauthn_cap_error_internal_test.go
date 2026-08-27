package controlplane

import (
	"encoding/json"
	"fmt"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// expectWebAuthnCredWalkExhaustion drives listUserWebAuthnCredentials to the
// structural-bound refusal: a single page of Limit(11) that still reports
// HasMore means the partition exceeds the per-user maximum, so the walk fails
// closed (issue #1061 part D). Each caller must map that listing error to
// app.internal and return it — never an empty/partial success body.
func expectWebAuthnCredWalkExhaustion(t *testing.T, q *ttmocks.MockQuery, cursor string) {
	t.Helper()
	filterMockQueryCalls(q, "Limit")
	q.On("Limit", 11).Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(&core.PaginatedResult{HasMore: true, NextCursor: cursor}, nil).Once()
}

func requireWebAuthnListingError(t *testing.T, resp *apptheory.Response, err error) {
	t.Helper()
	require.Nil(t, resp, "cap exhaustion must not produce a success body")
	require.Error(t, err, "cap exhaustion must surface as an error, never a swallowed listing")
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok, "expected *apptheory.AppTheoryError, got %T: %v", err, err)
	require.Equal(t, "app.internal", appErr.Code)
}

// TestHandleWebAuthnRegisterBegin_CapExhaustionPropagates pins the
// register-begin leg (handlers_webauthn.go:205-208). A swallowed listing
// error would begin a registration against an empty credential set and
// answer 200 — this test dies.
func TestHandleWebAuthnRegisterBegin_CapExhaustionPropagates(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	// Provide the engine so a swallow mutation would complete registration
	// (200) instead of panicking on a nil stub — the test then dies on the
	// error assertion, not on a mock artifact.
	engine := stubWebAuthnEngine{
		beginRegistration: func(_ webauthn.User, _ ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
			return &protocol.CredentialCreation{}, &webauthn.SessionData{Challenge: "c1"}, nil
		},
	}
	s := &Server{store: store.New(tdb.db), webAuthn: engine}

	expectWebAuthnCredWalkExhaustion(t, tdb.qCred, "wb-reg-begin")

	resp, err := s.handleWebAuthnRegisterBegin(&apptheory.Context{AuthIdentity: testUsernameAlice})
	requireWebAuthnListingError(t, resp, err)
	tdb.qCred.AssertExpectations(t)
}

// TestHandleWebAuthnRegisterFinish_CapExhaustionPropagates pins the
// register-finish leg (handlers_webauthn.go:307-310). The challenge lookup is
// stubbed only so a swallow mutation deterministically continues into
// completeWebAuthnRegistration and returns app.bad_request (invalid response)
// instead of app.internal — this test dies on the code assertion.
func TestHandleWebAuthnRegisterFinish_CapExhaustionPropagates(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db), webAuthn: stubWebAuthnEngine{}}

	expectWebAuthnCredWalkExhaustion(t, tdb.qCred, "wb-reg-finish")
	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge:   "c1",
			UserID:      testUsernameAlice,
			Type:        "registration",
			SessionData: []byte(`{}`),
		}
	}).Once()

	req := webAuthnFinishRegistrationRequest{
		Challenge:      "c1",
		Response:       map[string]any{},
		CredentialName: "key",
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	resp, err := s.handleWebAuthnRegisterFinish(&apptheory.Context{
		AuthIdentity: testUsernameAlice,
		Request:      apptheory.Request{Body: body},
	})
	requireWebAuthnListingError(t, resp, err)
	tdb.qCred.AssertExpectations(t)
}

// TestHandleWebAuthnLoginBegin_CapExhaustionPropagates pins the login-begin
// leg (handlers_webauthn.go:354-357). A swallowed listing error leaves an
// empty credential set, so the handler answers app.unauthorized (anti-user-
// enumeration) instead of the listing error — this test dies on the code
// assertion.
func TestHandleWebAuthnLoginBegin_CapExhaustionPropagates(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db), webAuthn: stubWebAuthnEngine{}}

	expectWebAuthnCredWalkExhaustion(t, tdb.qCred, "wb-login-begin")

	resp, err := s.handleWebAuthnLoginBegin(&apptheory.Context{
		Request: apptheory.Request{Body: []byte(`{"username":"alice"}`)},
	})
	requireWebAuthnListingError(t, resp, err)
	tdb.qCred.AssertExpectations(t)
}

// TestHandleWebAuthnCredentials_CapExhaustionPropagates pins the
// credentials-list leg (handlers_webauthn.go:590-593). A swallowed listing
// error would answer 200 with an empty credentials array — this test dies.
func TestHandleWebAuthnCredentials_CapExhaustionPropagates(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{store: store.New(tdb.db), webAuthn: stubWebAuthnEngine{}}

	expectWebAuthnCredWalkExhaustion(t, tdb.qCred, "wb-creds-list")

	resp, err := s.handleWebAuthnCredentials(&apptheory.Context{AuthIdentity: testUsernameAlice})
	requireWebAuthnListingError(t, resp, err)
	tdb.qCred.AssertExpectations(t)
}

// TestCompleteWebAuthnRegistration_RefusesAtMaxCredentials pins the written
// structural bound (handlers_webauthn.go:265): with the per-user maximum of
// 10 credentials already stored, an 11th registration attempt must be refused
// (app.conflict "max credentials reached") before any credential is created.
// The CreateCredential canary dies if the guard is weakened to `>` — the
// 10-credential partition then passes the guard and reaches the engine.
func TestCompleteWebAuthnRegistration_RefusesAtMaxCredentials(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnTestDB()
	s := &Server{
		store: store.New(tdb.db),
		webAuthn: stubWebAuthnEngine{
			createCredential: func(_ webauthn.User, _ webauthn.SessionData, _ *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
				t.Fatal("CreateCredential must not run when the per-user maximum (10) is reached")
				return nil, nil
			},
		},
	}

	// The session loads before the structural guard; stub a valid registration
	// challenge so the guard is the only barrier between the request and a
	// successful credential creation.
	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WebAuthnChallenge](t, args, 0)
		*dest = models.WebAuthnChallenge{
			Challenge:   "c1",
			UserID:      testUsernameAlice,
			Type:        "registration",
			SessionData: []byte(`{}`),
		}
	}).Once()

	creds := make([]*models.WebAuthnCredential, 0, 10)
	for i := 0; i < 10; i++ {
		creds = append(creds, webauthnCred(fmt.Sprintf("max-%02d", i)))
	}

	req := webAuthnFinishRegistrationRequest{
		Challenge:      "c1",
		Response:       makeSetupPasskeyCreationResponse(t, "c1"),
		CredentialName: "key-11",
	}

	stored, err := s.completeWebAuthnRegistration(
		new(apptheory.Context), testUsernameAlice, testUsernameAlice, req, creds,
	)
	require.Nil(t, stored)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok, "expected *apptheory.AppTheoryError, got %T: %v", err, err)
	require.Equal(t, appErrCodeConflict, appErr.Code)
	require.Contains(t, appErr.Message, "max credentials reached")
}
