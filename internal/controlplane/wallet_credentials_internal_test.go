package controlplane

import (
	"errors"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func newWalletCredentialTestServer() (*Server, *ttmocks.MockQuery) {
	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.AnythingOfType("*models.WalletCredential")).Return(q)
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("Limit", mock.Anything).Return(q).Maybe()

	return &Server{store: store.New(db)}, q
}

func TestGetWalletCredential_ErrorsOnNilOrBadArgs(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{}

	var s *Server
	_, err := s.getWalletCredential(ctx, "alice", "0xabc")
	require.Error(t, err)

	s2 := &Server{store: store.New(ttmocks.NewMockExtendedDBStrict())}
	_, err = s2.getWalletCredential(ctx, " ", "0xabc")
	require.Error(t, err)
	_, err = s2.getWalletCredential(ctx, "alice", " ")
	require.Error(t, err)
}

func TestGetWalletCredential_ReturnsCredential(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.AnythingOfType("*models.WalletCredential")).Return(q)

	q.On("Where", "PK", "=", "USER#alice").Return(q).Once()
	q.On("Where", "SK", "=", "WALLET#0xabc").Return(q).Once()
	q.On("Limit", 1).Return(q).Once()
	q.On("First", mock.AnythingOfType("*models.WalletCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletCredential](t, args, 0)
		*dest = models.WalletCredential{Username: "alice", Address: "0xabc"}
	}).Once()

	s := &Server{store: store.New(db)}
	cred, err := s.getWalletCredential(&apptheory.Context{}, "alice", " 0xAbC ")
	require.NoError(t, err)
	require.NotNil(t, cred)
	require.Equal(t, "alice", cred.Username)
	require.Equal(t, "0xabc", cred.Address)
}

func TestCredentialForWalletUsername_Branches(t *testing.T) {
	t.Parallel()

	t.Run("non_wallet_username_returns_nil", func(t *testing.T) {
		t.Parallel()

		s := &Server{store: store.New(ttmocks.NewMockExtendedDBStrict())}
		cred, appErr := s.credentialForWalletUsername(&apptheory.Context{}, "alice")
		require.Nil(t, cred)
		require.Nil(t, appErr)
	})

	t.Run("not_found_returns_nil", func(t *testing.T) {
		t.Parallel()

		s, q := newWalletCredentialTestServer()
		q.On("First", mock.AnythingOfType("*models.WalletCredential")).Return(theoryErrors.ErrItemNotFound).Once()
		cred, appErr := s.credentialForWalletUsername(&apptheory.Context{}, "wallet-abc")
		require.Nil(t, cred)
		require.Nil(t, appErr)
	})

	t.Run("unexpected_error_returns_internal", func(t *testing.T) {
		t.Parallel()

		s, q := newWalletCredentialTestServer()
		q.On("First", mock.AnythingOfType("*models.WalletCredential")).Return(errors.New("boom")).Once()
		cred, appErr := s.credentialForWalletUsername(&apptheory.Context{}, "wallet-abc")
		require.Nil(t, cred)
		require.NotNil(t, appErr)
		require.Equal(t, "app.internal", appErr.Code)
	})
}

func TestMostRecentlyUsedWalletCredential_SkipsInvalidAndChoosesLatest(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	old := &models.WalletCredential{Address: "0xabc", LastUsed: now.Add(-time.Hour)}
	newest := &models.WalletCredential{Address: "0xdef", LastUsed: now}
	emptyAddr := &models.WalletCredential{Address: " ", LastUsed: now.Add(10 * time.Hour)}

	best := mostRecentlyUsedWalletCredential([]*models.WalletCredential{nil, emptyAddr, old, newest})
	require.Same(t, newest, best)
}

func TestRequireUserWalletCredential_Branches(t *testing.T) {
	t.Parallel()

	t.Run("empty_username_unauthorized", func(t *testing.T) {
		t.Parallel()

		s := &Server{store: store.New(ttmocks.NewMockExtendedDBStrict())}
		cred, appErr := s.requireUserWalletCredential(&apptheory.Context{}, " ")
		require.Nil(t, cred)
		require.NotNil(t, appErr)
		require.Equal(t, "app.unauthorized", appErr.Code)
	})

	t.Run("wallet_username_happy_path", func(t *testing.T) {
		t.Parallel()

		s, q := newWalletCredentialTestServer()
		q.On("First", mock.AnythingOfType("*models.WalletCredential")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.WalletCredential](t, args, 0)
			*dest = models.WalletCredential{Username: "wallet-abc", Address: "0xabc"}
		}).Once()
		cred, appErr := s.requireUserWalletCredential(&apptheory.Context{}, "wallet-abc")
		require.Nil(t, appErr)
		require.NotNil(t, cred)
		require.Equal(t, "0xabc", cred.Address)
	})

	t.Run("fallback_selects_most_recent", func(t *testing.T) {
		t.Parallel()

		s, q := newWalletCredentialTestServer()
		q.On("All", mock.AnythingOfType("*[]*models.WalletCredential")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.WalletCredential](t, args, 0)
			*dest = []*models.WalletCredential{
				{Address: "0xabc", LastUsed: time.Unix(1, 0).UTC()},
				{Address: "0xdef", LastUsed: time.Unix(2, 0).UTC()},
			}
		}).Once()
		cred, appErr := s.requireUserWalletCredential(&apptheory.Context{}, "alice")
		require.Nil(t, appErr)
		require.NotNil(t, cred)
		require.Equal(t, "0xdef", cred.Address)
	})

	t.Run("fallback_empty_returns_conflict", func(t *testing.T) {
		t.Parallel()

		s, q := newWalletCredentialTestServer()
		q.On("All", mock.AnythingOfType("*[]*models.WalletCredential")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.WalletCredential](t, args, 0)
			*dest = []*models.WalletCredential{}
		}).Once()
		cred, appErr := s.requireUserWalletCredential(&apptheory.Context{}, "alice")
		require.Nil(t, cred)
		require.NotNil(t, appErr)
		require.Equal(t, "app.conflict", appErr.Code)
	})

	t.Run("fallback_query_error_returns_internal", func(t *testing.T) {
		t.Parallel()

		s, q := newWalletCredentialTestServer()
		q.On("All", mock.AnythingOfType("*[]*models.WalletCredential")).Return(errors.New("boom")).Once()
		cred, appErr := s.requireUserWalletCredential(&apptheory.Context{}, "alice")
		require.Nil(t, cred)
		require.NotNil(t, appErr)
		require.Equal(t, "app.internal", appErr.Code)
	})
}
