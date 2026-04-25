package controlplane

import (
	"net/http"
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

func TestRequireMailboxInstanceKeyHashOnly(t *testing.T) {
	t.Parallel()

	t.Run("valid hash lookup", func(t *testing.T) {
		t.Parallel()
		db, qKey := newMailboxAuthTestDB()
		qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
			*dest = models.InstanceKey{ID: sha256HexTrimmed("raw-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)}
		}).Once()
		qKey.On("Update", mock.Anything).Return(nil).Once()

		s := &Server{store: store.New(db)}
		key, err := s.requireMailboxInstanceKey(newMailboxAuthCtx("Bearer raw-key"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key == nil || key.InstanceSlug != "inst1" {
			t.Fatalf("unexpected key: %#v", key)
		}
	})

	t.Run("rejects legacy plaintext key id fallback", func(t *testing.T) {
		t.Parallel()
		db, qKey := newMailboxAuthTestDB()
		qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()

		s := &Server{store: store.New(db)}
		_, err := s.requireMailboxInstanceKey(newMailboxAuthCtx("Bearer plaintext-key-id"))
		assertCommTheoryErrorCode(t, err, commCodeUnauthorized, http.StatusUnauthorized)
	})

	t.Run("rejects revoked key", func(t *testing.T) {
		t.Parallel()
		db, qKey := newMailboxAuthTestDB()
		qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
			*dest = models.InstanceKey{ID: sha256HexTrimmed("raw-key"), InstanceSlug: "inst1", RevokedAt: time.Now()}
		}).Once()

		s := &Server{store: store.New(db)}
		_, err := s.requireMailboxInstanceKey(newMailboxAuthCtx("Bearer raw-key"))
		assertCommTheoryErrorCode(t, err, commCodeUnauthorized, http.StatusUnauthorized)
	})

	t.Run("rejects missing bearer", func(t *testing.T) {
		t.Parallel()
		s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
		_, err := s.requireMailboxInstanceKey(newMailboxAuthCtx(""))
		assertCommTheoryErrorCode(t, err, commCodeUnauthorized, http.StatusUnauthorized)
	})
}

func newMailboxAuthTestDB() (*ttmocks.MockExtendedDB, *ttmocks.MockQuery) {
	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qKey).Maybe()
	qKey.On("ConsistentRead").Return(qKey).Maybe()
	qKey.On("IfExists").Return(qKey).Maybe()
	return db, qKey
}

func newMailboxAuthCtx(auth string) *apptheory.Context {
	headers := map[string][]string{}
	if auth != "" {
		headers["authorization"] = []string{auth}
	}
	return &apptheory.Context{Request: apptheory.Request{Headers: headers}}
}
