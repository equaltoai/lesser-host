package controlplane

import (
	"errors"
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type webAuthnMoreTestDB struct {
	db        *ttmocks.MockExtendedDB
	qCred     *ttmocks.MockQuery
	qChallenge *ttmocks.MockQuery
}

func newWebAuthnMoreTestDB() webAuthnMoreTestDB {
	db := ttmocks.NewMockExtendedDB()
	qCred := new(ttmocks.MockQuery)
	qChallenge := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnCredential")).Return(qCred).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(qChallenge).Maybe()

	for _, q := range []*ttmocks.MockQuery{qCred, qChallenge} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return webAuthnMoreTestDB{db: db, qCred: qCred, qChallenge: qChallenge}
}

func TestParseWebAuthnLoginFinishRequest_ValidatesRequiredFields(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, err := parseWebAuthnLoginFinishRequest(ctx); err == nil {
		t.Fatalf("expected username required")
	}

	body, _ := json.Marshal(webAuthnFinishLoginRequest{Username: "alice"})
	ctx = &apptheory.Context{Request: apptheory.Request{Body: body}}
	if _, err := parseWebAuthnLoginFinishRequest(ctx); err == nil {
		t.Fatalf("expected challenge required")
	}

	body, _ = json.Marshal(webAuthnFinishLoginRequest{Username: "alice", Challenge: "c"})
	ctx = &apptheory.Context{Request: apptheory.Request{Body: body}}
	if _, err := parseWebAuthnLoginFinishRequest(ctx); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestBuildWebAuthnUser_LoadsCredentials(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnMoreTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.WebAuthnCredential)
		*dest = []*models.WebAuthnCredential{
			nil,
			{ID: "YQ==", UserID: "alice", Name: "key1"},
		}
	}).Once()

	ctx := &apptheory.Context{}
	user, creds, err := s.buildWebAuthnUser(ctx, "alice")
	if err != nil || user == nil || len(user.WebAuthnCredentials()) != 1 || len(creds) != 2 {
		t.Fatalf("unexpected: user=%#v creds=%#v err=%v", user, creds, err)
	}
}

func TestWebAuthnFinishHandlers_RequireConfig(t *testing.T) {
	t.Parallel()

	tdb := newWebAuthnMoreTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, err := s.handleWebAuthnRegisterFinish(&apptheory.Context{}); err == nil {
		t.Fatalf("expected conflict")
	}
	if _, err := s.handleWebAuthnLoginFinish(&apptheory.Context{}); err == nil {
		t.Fatalf("expected conflict")
	}

	// loadWebAuthnSession: unauthorized on missing challenge.
	tdb.qChallenge.On("First", mock.AnythingOfType("*models.WebAuthnChallenge")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.loadWebAuthnSession(&apptheory.Context{}, "c", "alice", "login"); err == nil {
		t.Fatalf("expected unauthorized")
	}

	// buildWebAuthnUser: internal error when DB query fails.
	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(errors.New("boom")).Once()
	_, _, err := s.buildWebAuthnUser(&apptheory.Context{}, "alice")
	if err == nil {
		t.Fatalf("expected error")
	}
}
