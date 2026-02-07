package controlplane

import (
	"context"
	"testing"
	"time"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestCreateWalletChallenge_Validates(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if _, err := s.createWalletChallenge(context.Background(), "0xabc", 1, "u"); err == nil {
		t.Fatalf("expected error for missing store")
	}

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Create").Return(nil).Maybe()

	s = &Server{store: store.New(db)}
	if _, err := s.createWalletChallenge(context.Background(), "0xabc", 1, " "); err == nil {
		t.Fatalf("expected error for empty username")
	}
	if _, err := s.createWalletChallenge(context.Background(), " ", 1, "u"); err == nil {
		t.Fatalf("expected error for empty address")
	}
}

func TestCreateWalletChallenge_Success(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Create").Return(nil).Once()

	s := &Server{store: store.New(db)}
	ch, err := s.createWalletChallenge(context.Background(), "0xAbC", 0, " alice ")
	if err != nil {
		t.Fatalf("createWalletChallenge: %v", err)
	}
	if ch == nil {
		t.Fatalf("expected challenge")
	}
	if ch.ID == "" || ch.Nonce == "" || ch.Message == "" {
		t.Fatalf("expected id/nonce/message set: %#v", ch)
	}
	if ch.Username != testUsernameAlice || ch.Address != "0xabc" {
		t.Fatalf("expected trimmed/normalized fields, got %#v", ch)
	}
	if ch.ExpiresAt.Sub(ch.IssuedAt) != walletChallengeDuration {
		t.Fatalf("expected duration %v, got %v", walletChallengeDuration, ch.ExpiresAt.Sub(ch.IssuedAt))
	}
}

func TestGetWalletChallenge_ExpiredAndSpent(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("Delete").Return(nil).Maybe()

	s := &Server{store: store.New(db)}

	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
		*dest = models.WalletChallenge{
			ID:        "c1",
			Username:  "u",
			Address:   "0xabc",
			ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
		}
	}).Once()

	_, err := s.getWalletChallenge(context.Background(), "c1")
	if !theoryErrors.IsNotFound(err) {
		t.Fatalf("expected not found for expired, got %v", err)
	}

	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
		*dest = models.WalletChallenge{
			ID:        "c2",
			Username:  "u",
			Address:   "0xabc",
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
			Spent:     true,
		}
	}).Once()

	_, err = s.getWalletChallenge(context.Background(), "c2")
	if !theoryErrors.IsNotFound(err) {
		t.Fatalf("expected not found for spent, got %v", err)
	}
}

func TestGetWalletChallenge_Success(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()

	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletChallenge](t, args, 0)
		*dest = models.WalletChallenge{
			ID:        "c1",
			Username:  "u",
			Address:   "0xabc",
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		}
	}).Once()

	s := &Server{store: store.New(db)}
	ch, err := s.getWalletChallenge(context.Background(), "c1")
	if err != nil {
		t.Fatalf("getWalletChallenge: %v", err)
	}
	if ch == nil || ch.ID != "c1" {
		t.Fatalf("unexpected challenge: %#v", ch)
	}
}

func TestDeleteWalletChallenge_NoOpAndSuccess(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("Delete").Return(nil).Once()

	s := &Server{store: store.New(db)}
	if err := s.deleteWalletChallenge(context.Background(), " "); err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}
	if err := s.deleteWalletChallenge(context.Background(), "c1"); err != nil {
		t.Fatalf("deleteWalletChallenge: %v", err)
	}
}
