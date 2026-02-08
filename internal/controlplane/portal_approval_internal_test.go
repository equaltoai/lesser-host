package controlplane

import (
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func newPortalApprovalTestDB(t *testing.T, user models.User) (*ttmocks.MockExtendedDB, *Server) {
	t.Helper()

	db, qs := newTestDBWithModelQueries("*models.User")
	qUser := qs[0]
	qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.User)
		if !ok {
			t.Fatalf("expected *models.User, got %T", destAny)
		}
		*dest = user
		if dest.CreatedAt.IsZero() {
			dest.CreatedAt = time.Now().UTC()
		}
		_ = dest.UpdateKeys()
	}).Once()

	s := &Server{store: store.New(db)}
	return db, s
}

func TestRequirePortalApproved_Statuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		user      models.User
		wantError string
	}{
		{
			name: "approved",
			user: models.User{
				Username:       "alice",
				Role:           models.RoleCustomer,
				Approved:       true,
				ApprovalStatus: models.UserApprovalStatusApproved,
			},
			wantError: "",
		},
		{
			name: "pending",
			user: models.User{
				Username:       "alice",
				Role:           models.RoleCustomer,
				Approved:       false,
				ApprovalStatus: models.UserApprovalStatusPending,
			},
			wantError: "approval required",
		},
		{
			name: "rejected",
			user: models.User{
				Username:       "alice",
				Role:           models.RoleCustomer,
				Approved:       false,
				ApprovalStatus: models.UserApprovalStatusRejected,
			},
			wantError: "approval rejected",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, s := newPortalApprovalTestDB(t, test.user)
			ctx := &apptheory.Context{AuthIdentity: "alice"}
			err := s.requirePortalApproved(ctx)
			if test.wantError == "" && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if test.wantError != "" {
				if err == nil || err.Message != test.wantError {
					t.Fatalf("expected %q, got %#v", test.wantError, err)
				}
			}
		})
	}
}

func TestRequirePortalApproved_OperatorBypass(t *testing.T) {
	t.Parallel()

	ctx := adminCtx()
	s := &Server{}
	if err := s.requirePortalApproved(ctx); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWalletAddressFromUsername(t *testing.T) {
	t.Parallel()

	if got := walletAddressFromUsername("wallet-abc"); got != "0xabc" {
		t.Fatalf("expected 0xabc, got %s", got)
	}
	if got := walletAddressFromUsername("admin"); got != "" {
		t.Fatalf("expected empty, got %s", got)
	}
}
