package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const testInstanceSlug = "slug"

func TestInstanceAuthHook_Basics(t *testing.T) {
	t.Parallel()

	t.Run("nil_server", func(t *testing.T) {
		t.Parallel()

		var s *Server
		_, err := s.InstanceAuthHook(&apptheory.Context{})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("nil_ctx", func(t *testing.T) {
		t.Parallel()

		s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
		_, err := s.InstanceAuthHook(nil)
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("no_token", func(t *testing.T) {
		t.Parallel()

		s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
		slug, err := s.InstanceAuthHook(&apptheory.Context{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slug != "" {
			t.Fatalf("expected empty slug, got %q", slug)
		}
	})
}

func TestInstanceAuthHook_LookupAndRevocation(t *testing.T) {
	t.Parallel()

	rawKey := "raw-instance-key"
	sum := sha256.Sum256([]byte(rawKey))
	keyID := hex.EncodeToString(sum[:])

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Where", "PK", "=", fmt.Sprintf("INSTANCE_KEY#%s", keyID)).Return(q)
		q.On("Where", "SK", "=", "KEY").Return(q)
		q.On("First", mock.Anything).Return(theoryErrors.ErrItemNotFound)

		s := &Server{store: store.New(db)}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
			"authorization": {"Bearer " + rawKey},
		}}}
		slug, err := s.InstanceAuthHook(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slug != "" {
			t.Fatalf("expected empty slug, got %q", slug)
		}
	})

	t.Run("revoked_key", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
		q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
			dest.ID = keyID
			dest.InstanceSlug = "example"
			dest.RevokedAt = time.Unix(1, 0).UTC()
		})

		s := &Server{store: store.New(db)}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
			"authorization": {"Bearer " + rawKey},
		}}}
		slug, err := s.InstanceAuthHook(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slug != "" {
			t.Fatalf("expected empty slug, got %q", slug)
		}
	})

	t.Run("active_key_sets_context", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
		q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
			dest.ID = keyID
			dest.InstanceSlug = testInstanceSlug
		})

		s := &Server{store: store.New(db)}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
			"authorization": {"Bearer " + rawKey},
		}}}
		slug, err := s.InstanceAuthHook(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slug != testInstanceSlug {
			t.Fatalf("expected slug, got %q", slug)
		}
		if got := ctx.Get(ctxKeyInstanceSlug); got != testInstanceSlug {
			t.Fatalf("expected ctx instance slug set, got %#v", got)
		}
		if got := ctx.Get(ctxKeyInstanceKey); got != keyID {
			t.Fatalf("expected ctx key id set, got %#v", got)
		}
	})
}
