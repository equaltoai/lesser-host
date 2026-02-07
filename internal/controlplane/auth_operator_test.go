package controlplane

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	testUsernameAlice         = "alice"
	testSessionMethodWebAuthn = "webauthn"
)

func TestBearerToken(t *testing.T) {
	t.Parallel()

	if got := httpx.BearerToken(nil); got != "" {
		t.Fatalf("expected empty token, got %q", got)
	}
	if got := httpx.BearerToken(map[string][]string{"authorization": {testNope}}); got != "" {
		t.Fatalf("expected empty token, got %q", got)
	}
	if got := httpx.BearerToken(map[string][]string{"authorization": {"Bearer abc"}}); got != "abc" {
		t.Fatalf("expected abc, got %q", got)
	}
	if got := httpx.BearerToken(map[string][]string{"authorization": {"bearer   xyz  "}}); got != "xyz" {
		t.Fatalf("expected xyz, got %q", got)
	}
}

func TestRBAC(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{}
	if err := requireAuthenticated(ctx); err == nil {
		t.Fatalf("expected unauthorized")
	}

	ctx.AuthIdentity = testUsernameAlice
	if err := requireAuthenticated(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := requireOperator(ctx); err == nil {
		t.Fatalf("expected forbidden")
	}

	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)
	if err := requireOperator(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := requireAdmin(ctx); err == nil {
		t.Fatalf("expected forbidden")
	}

	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	if err := requireAdmin(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOperatorAuthHook(t *testing.T) {
	t.Parallel()

	t.Run("nil_server", func(t *testing.T) {
		t.Parallel()

		var s *Server
		_, err := s.OperatorAuthHook(&apptheory.Context{})
		require.Error(t, err)
	})

	t.Run("no_token", func(t *testing.T) {
		t.Parallel()

		s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
		id, err := s.OperatorAuthHook(&apptheory.Context{})
		require.NoError(t, err)
		require.Empty(t, id)
	})

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Where", "PK", "=", fmt.Sprintf(models.KeyPatternSession, "t")).Return(q)
		q.On("Where", "SK", "=", "SESSION").Return(q)
		q.On("First", mock.Anything).Return(theoryErrors.ErrItemNotFound)

		s := &Server{store: store.New(db)}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
			"authorization": {"Bearer t"},
		}}}
		id, err := s.OperatorAuthHook(ctx)
		require.NoError(t, err)
		require.Empty(t, id)
	})

	t.Run("expired_session_deleted", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
		q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.OperatorSession](t, args, 0)
			dest.ID = "t"
			dest.Username = testUsernameAlice
			dest.Role = models.RoleAdmin
			dest.Method = "wallet"
			dest.ExpiresAt = time.Unix(1, 0).UTC()
			_ = dest.UpdateKeys()
		})
		q.On("Delete").Return(nil).Once()

		s := &Server{store: store.New(db)}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
			"authorization": {"Bearer t"},
		}}}

		id, err := s.OperatorAuthHook(ctx)
		require.NoError(t, err)
		require.Empty(t, id)
		require.Empty(t, operatorRoleFromContext(ctx))
	})

	t.Run("active_session_sets_context", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
		q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.OperatorSession](t, args, 0)
			dest.ID = "t"
			dest.Username = testUsernameAlice
			dest.Role = models.RoleOperator
			dest.Method = testSessionMethodWebAuthn
			dest.ExpiresAt = time.Now().Add(1 * time.Hour)
			_ = dest.UpdateKeys()
		})

		s := &Server{store: store.New(db)}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
			"authorization": {"Bearer t"},
		}}}

		id, err := s.OperatorAuthHook(ctx)
		require.NoError(t, err)
		require.Equal(t, testUsernameAlice, id)
		require.Equal(t, models.RoleOperator, operatorRoleFromContext(ctx))
		require.Equal(t, testSessionMethodWebAuthn, operatorMethodFromContext(ctx))
	})
}

func TestCreateOperatorSession_WritesSession(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)

	db.On("Model", mock.Anything).Return(q).Run(func(args mock.Arguments) {
		if _, ok := args.Get(0).(*models.OperatorSession); !ok {
			t.Fatalf("expected OperatorSession model, got %T", args.Get(0))
		}
	})
	q.On("Create").Return(nil)

	s := &Server{store: store.New(db)}

	token, expiresAt, err := s.createOperatorSession(context.Background(), testUsernameAlice, models.RoleAdmin, "wallet")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.False(t, expiresAt.IsZero())
}
