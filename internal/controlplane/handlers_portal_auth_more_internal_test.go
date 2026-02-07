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

type portalAuthMoreTestDB struct {
	db    *ttmocks.MockExtendedDB
	qUser *ttmocks.MockQuery
}

func newPortalAuthMoreTestDB() portalAuthMoreTestDB {
	db := ttmocks.NewMockExtendedDB()
	qUser := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()

	qUser.On("IfExists").Return(qUser).Maybe()
	qUser.On("Update", mock.Anything).Return(nil).Maybe()

	return portalAuthMoreTestDB{db: db, qUser: qUser}
}

func TestLinkPortalWalletToCustomer_RejectsNonCustomer(t *testing.T) {
	t.Parallel()

	s := &Server{}
	err := s.linkPortalWalletToCustomer(&apptheory.Context{}, models.User{Role: models.RoleAdmin}, "alice", "0xabc", 1, "", time.Now().UTC())
	if err == nil {
		t.Fatalf("expected forbidden error")
	}
}

func TestLinkPortalWalletToCustomer_LinksAndUpdatesEmailBestEffort(t *testing.T) {
	t.Parallel()

	tdb := newPortalAuthMoreTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	user := models.User{Username: "alice", Role: models.RoleCustomer, Email: ""}
	if err := s.linkPortalWalletToCustomer(ctx, user, "alice", "0xabc", 1, "a@example.com", time.Now().UTC()); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
