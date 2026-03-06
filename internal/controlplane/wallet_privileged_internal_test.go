package controlplane

import (
	"context"
	"testing"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestValidateNotPrivilegedWalletAddress(t *testing.T) {
	ctx := context.Background()

	if appErr := (*Server)(nil).validateNotPrivilegedWalletAddress(ctx, "", "", "wallet"); appErr == nil || appErr.Code != "app.internal" {
		t.Fatalf("expected internal error for nil server, got %#v", appErr)
	}

	db := ttmocks.NewMockExtendedDB()
	qWallet := new(ttmocks.MockQuery)
	qUser := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(qWallet).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()

	for _, q := range []*ttmocks.MockQuery{qWallet, qUser} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
	}

	s := &Server{store: store.New(db)}

	if appErr := s.validateNotPrivilegedWalletAddress(ctx, "", " ", "wallet"); appErr == nil || appErr.Code != "app.bad_request" {
		t.Fatalf("expected bad request for empty address, got %#v", appErr)
	}

	qWallet.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	if appErr := s.validateNotPrivilegedWalletAddress(ctx, "", "0xabc", "wallet"); appErr != nil {
		t.Fatalf("expected nil for unknown wallet, got %#v", appErr)
	}

	qWallet.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletIndex](t, args, 0)
		dest.Username = "admin-user"
	}).Once()
	qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		dest.Role = models.RoleAdmin
	}).Once()
	if appErr := s.validateNotPrivilegedWalletAddress(ctx, "", "0xdef", "sender_wallet"); appErr == nil || appErr.Message != "sender_wallet is not allowed" {
		t.Fatalf("expected privileged wallet rejection, got %#v", appErr)
	}

	qWallet.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletIndex](t, args, 0)
		dest.Username = "customer-user"
	}).Once()
	qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		dest.Role = models.RoleCustomer
	}).Once()
	if appErr := s.validateNotPrivilegedWalletAddress(ctx, walletTypeEthereum, "0x123", ""); appErr != nil {
		t.Fatalf("expected non-privileged wallet to pass, got %#v", appErr)
	}
}
