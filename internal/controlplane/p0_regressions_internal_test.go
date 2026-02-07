package controlplane

import (
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestP0_RequireSetupSession_UnauthorizedWithoutBearer(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{BootstrapWalletAddress: "0xbootstrap"}}
	ctx := &apptheory.Context{}

	_, err := s.requireSetupSession(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected *apptheory.AppError, got %T: %v", err, err)
	}
	if appErr.Code != "app.unauthorized" {
		t.Fatalf("expected app.unauthorized, got %q", appErr.Code)
	}
}

func TestP0_SetupFinalizeRejectsNonAdminRole(t *testing.T) {
	t.Parallel()

	db, qs := newTestDBWithModelQueries(
		"*models.ControlPlaneConfig",
	)
	qCP := qs[0]

	s := &Server{cfg: config.Config{BootstrapWalletAddress: "0xbootstrap"}, store: store.New(db)}

	qCP.On("First", mock.AnythingOfType("*models.ControlPlaneConfig")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ControlPlaneConfig](t, args, 0)
		*dest = models.ControlPlaneConfig{PrimaryAdminUsername: "admin"}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "admin", RequestID: "rid"}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, err := s.handleSetupFinalize(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected *apptheory.AppError, got %T: %v", err, err)
	}
	if appErr.Code != "app.forbidden" {
		t.Fatalf("expected app.forbidden, got %q", appErr.Code)
	}
}

func TestP0_OperatorAuthHook_ExpiredSessionIsNotAuthenticated(t *testing.T) {
	t.Parallel()

	db, qs := newTestDBWithModelQueries(
		"*models.OperatorSession",
	)
	qSession := qs[0]

	s := &Server{store: store.New(db)}

	qSession.On("First", mock.AnythingOfType("*models.OperatorSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.OperatorSession](t, args, 0)
		*dest = models.OperatorSession{
			ID:        "tok",
			Role:      models.RoleAdmin,
			Username:  "admin",
			Method:    "wallet",
			ExpiresAt: time.Now().Add(-1 * time.Minute),
		}
		_ = dest.UpdateKeys()
	}).Once()

	qSession.On("Delete").Return(nil).Once()

	ctx := &apptheory.Context{
		RequestID: "rid",
		Request: apptheory.Request{
			Headers: map[string][]string{"authorization": {"Bearer tok"}},
		},
	}

	username, err := s.OperatorAuthHook(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if username != "" {
		t.Fatalf("expected empty username, got %q", username)
	}
}
