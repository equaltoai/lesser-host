package controlplane

import (
	"errors"
	"fmt"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// newWebAuthnCredScanMock builds a mock DB whose WebAuthnCredential model
// routes to one MockQuery pre-wired for the keyed partition read
// (PK=USER#<username>, SK BEGINS_WITH WEBAUTHN_CRED#) under test
// (issue #1061 part D).
func newWebAuthnCredScanMock() (*ttmocks.MockExtendedDB, *ttmocks.MockQuery) {
	db := ttmocks.NewMockExtendedDB()
	qCred := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WebAuthnCredential")).Return(qCred).Maybe()
	qCred.On("Where", "PK", "=", "USER#alice").Return(qCred).Once()
	qCred.On("Where", "SK", "BEGINS_WITH", "WEBAUTHN_CRED#").Return(qCred).Once()
	return db, qCred
}

func webauthnCred(id string) *models.WebAuthnCredential {
	return &models.WebAuthnCredential{ID: id, UserID: "alice", Name: "key " + id}
}

func TestListUserWebAuthnCredentials_BoundedSinglePage(t *testing.T) {
	t.Parallel()

	db, qCred := newWebAuthnCredScanMock()
	// Literal pin: the read is clamped to a single page of Limit(11)
	// (maxWebAuthnCredentials + 1); the constant under test is never
	// referenced.
	qCred.On("Limit", 11).Return(qCred).Once()
	qCred.On("AllPaginated", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = []*models.WebAuthnCredential{webauthnCred("cred1"), webauthnCred("cred2")}
	}).Once()

	s := &Server{store: store.New(db)}
	creds, err := s.listUserWebAuthnCredentials(new(apptheory.Context), "alice")
	require.NoError(t, err)
	require.Len(t, creds, 2)
	require.Equal(t, "cred1", creds[0].ID)
	require.Equal(t, "cred2", creds[1].ID)
	qCred.AssertExpectations(t)
	qCred.AssertNotCalled(t, "Scan", mock.Anything)
	qCred.AssertNotCalled(t, "Cursor", mock.Anything)
}

func TestListUserWebAuthnCredentials_EmptyPartition(t *testing.T) {
	t.Parallel()

	db, qCred := newWebAuthnCredScanMock()
	qCred.On("Limit", 11).Return(qCred).Once()
	qCred.On("AllPaginated", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = nil
	}).Once()

	s := &Server{store: store.New(db)}
	creds, err := s.listUserWebAuthnCredentials(new(apptheory.Context), "alice")
	require.NoError(t, err)
	require.Empty(t, creds)
	qCred.AssertExpectations(t)
}

func TestListUserWebAuthnCredentials_AtMaxCredentials(t *testing.T) {
	t.Parallel()

	// A legitimate user sits exactly at the per-user maximum (10 credentials):
	// the single page of Limit(11) still terminates with HasMore=false, so the
	// read returns all ten without error.
	db, qCred := newWebAuthnCredScanMock()
	qCred.On("Limit", 11).Return(qCred).Once()
	qCred.On("AllPaginated", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		page := make([]*models.WebAuthnCredential, 0, 10)
		for i := 0; i < 10; i++ {
			page = append(page, webauthnCred(fmt.Sprintf("cred-%02d", i)))
		}
		*dest = page
	}).Once()

	s := &Server{store: store.New(db)}
	creds, err := s.listUserWebAuthnCredentials(new(apptheory.Context), "alice")
	require.NoError(t, err)
	require.Len(t, creds, 10)
	qCred.AssertExpectations(t)
}

func TestListUserWebAuthnCredentials_StructuralBoundViolationFailsClosed(t *testing.T) {
	t.Parallel()

	// A HasMore page at the clamped limit means the partition exceeds the
	// structural per-user maximum; the walk (maxPages=1) fails closed instead
	// of silently truncating the credential set.
	db, qCred := newWebAuthnCredScanMock()
	qCred.On("Limit", 11).Return(qCred).Once()
	qCred.On("AllPaginated", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "unexpected-more"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WebAuthnCredential](t, args, 0)
		*dest = []*models.WebAuthnCredential{webauthnCred("cred-01")}
	}).Once()

	s := &Server{store: store.New(db)}
	creds, err := s.listUserWebAuthnCredentials(new(apptheory.Context), "alice")
	require.Nil(t, creds)
	require.Error(t, err)
	qCred.AssertExpectations(t)
}

func TestListUserWebAuthnCredentials_PropagatesDBError(t *testing.T) {
	t.Parallel()

	db, qCred := newWebAuthnCredScanMock()
	qCred.On("Limit", 11).Return(qCred).Once()
	qCred.On("AllPaginated", mock.AnythingOfType("*[]*models.WebAuthnCredential")).Return((*core.PaginatedResult)(nil), errors.New("db down")).Once()

	s := &Server{store: store.New(db)}
	creds, err := s.listUserWebAuthnCredentials(new(apptheory.Context), "alice")
	require.Nil(t, creds)
	require.ErrorContains(t, err, "db down")
	qCred.AssertExpectations(t)
}
