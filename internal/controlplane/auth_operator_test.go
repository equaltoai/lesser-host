package controlplane

import (
	"context"
	"fmt"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestBearerToken(t *testing.T) {
	t.Parallel()

	if got := bearerToken(nil); got != "" {
		t.Fatalf("expected empty token, got %q", got)
	}
	if got := bearerToken(map[string][]string{"authorization": {"nope"}}); got != "" {
		t.Fatalf("expected empty token, got %q", got)
	}
	if got := bearerToken(map[string][]string{"authorization": {"Bearer abc"}}); got != "abc" {
		t.Fatalf("expected abc, got %q", got)
	}
	if got := bearerToken(map[string][]string{"authorization": {"bearer   xyz  "}}); got != "xyz" {
		t.Fatalf("expected xyz, got %q", got)
	}
}

func TestRBAC(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{}
	if err := requireAuthenticated(ctx); err == nil {
		t.Fatalf("expected unauthorized")
	}

	ctx.AuthIdentity = "alice"
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
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("no_token", func(t *testing.T) {
		t.Parallel()

		s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
		id, err := s.OperatorAuthHook(&apptheory.Context{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "" {
			t.Fatalf("expected empty id, got %q", id)
		}
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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "" {
			t.Fatalf("expected empty id, got %q", id)
		}
	})

	t.Run("expired_session_deleted", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
		q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.OperatorSession)
			dest.ID = "t"
			dest.Username = "alice"
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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "" {
			t.Fatalf("expected empty id, got %q", id)
		}
		if got := operatorRoleFromContext(ctx); got != "" {
			t.Fatalf("expected role unset, got %q", got)
		}
	})

	t.Run("active_session_sets_context", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
		q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.OperatorSession)
			dest.ID = "t"
			dest.Username = "alice"
			dest.Role = models.RoleOperator
			dest.Method = "webauthn"
			dest.ExpiresAt = time.Now().Add(1 * time.Hour)
			_ = dest.UpdateKeys()
		})

		s := &Server{store: store.New(db)}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
			"authorization": {"Bearer t"},
		}}}

		id, err := s.OperatorAuthHook(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "alice" {
			t.Fatalf("expected alice, got %q", id)
		}
		if got := operatorRoleFromContext(ctx); got != models.RoleOperator {
			t.Fatalf("expected role set, got %q", got)
		}
		if got := operatorMethodFromContext(ctx); got != "webauthn" {
			t.Fatalf("expected method set, got %q", got)
		}
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

	token, expiresAt, err := s.createOperatorSession(context.Background(), "alice", models.RoleAdmin, "wallet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Fatalf("expected token")
	}
	if expiresAt.IsZero() {
		t.Fatalf("expected expiresAt set")
	}
}
