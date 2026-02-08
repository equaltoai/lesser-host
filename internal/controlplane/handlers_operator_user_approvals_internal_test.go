package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type portalUserApprovalTestDB struct {
	db     *ttmocks.MockExtendedDB
	qUser  *ttmocks.MockQuery
	qAudit *ttmocks.MockQuery
}

func newPortalUserApprovalTestDB() portalUserApprovalTestDB {
	db, qs := newTestDBWithModelQueries(
		"*models.User",
		"*models.AuditLogEntry",
	)
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()
	return portalUserApprovalTestDB{
		db:     db,
		qUser:  qs[0],
		qAudit: qs[1],
	}
}

func TestHandleListPortalUserApprovals(t *testing.T) {
	t.Parallel()

	tdb := newPortalUserApprovalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qUser.On("All", mock.AnythingOfType("*[]*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.User](t, args, 0)
		*dest = []*models.User{
			{
				Username:       "wallet-abc",
				Role:           models.RoleCustomer,
				ApprovalStatus: models.UserApprovalStatusPending,
				CreatedAt:      time.Now().UTC(),
			},
		}
	}).Once()

	resp, err := s.handleListPortalUserApprovals(adminCtx())
	if err != nil {
		t.Fatalf("handleListPortalUserApprovals err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestHandleListPortalUserApprovals_InvalidStatus(t *testing.T) {
	t.Parallel()

	tdb := newPortalUserApprovalTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := adminCtx()
	ctx.Request.Query = map[string][]string{"status": {"nope"}}
	if _, err := s.handleListPortalUserApprovals(ctx); err == nil {
		t.Fatalf("expected error for invalid status")
	}
}

func TestHandleApproveRejectPortalUser(t *testing.T) {
	t.Parallel()

	tdb := newPortalUserApprovalTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Approve branch.
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{
			Username:       "wallet-abc",
			Role:           models.RoleCustomer,
			ApprovalStatus: models.UserApprovalStatusPending,
			CreatedAt:      time.Now().UTC(),
		}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"username": "wallet-abc"}
	ctx.Request = apptheory.Request{Body: []byte(`{"note":"ok"}`)}
	resp, err := s.handleApprovePortalUser(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("approve: resp=%#v err=%v", resp, err)
	}

	// Reject branch.
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{
			Username:       "wallet-def",
			Role:           models.RoleCustomer,
			ApprovalStatus: models.UserApprovalStatusPending,
			CreatedAt:      time.Now().UTC(),
		}
		_ = dest.UpdateKeys()
	}).Once()

	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"username": "wallet-def"}
	body, _ := json.Marshal(reviewNoteRequest{Note: "no"})
	ctx2.Request = apptheory.Request{Body: body}
	resp, err = s.handleRejectPortalUser(ctx2)
	if err != nil || resp.Status != 200 {
		t.Fatalf("reject: resp=%#v err=%v", resp, err)
	}
}

func TestHandleApprovePortalUser_NonCustomer(t *testing.T) {
	t.Parallel()

	tdb := newPortalUserApprovalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{
			Username:       "admin",
			Role:           models.RoleAdmin,
			ApprovalStatus: models.UserApprovalStatusPending,
			CreatedAt:      time.Now().UTC(),
		}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"username": "admin"}
	if _, err := s.handleApprovePortalUser(ctx); err == nil {
		t.Fatalf("expected conflict for non-customer")
	}
}
