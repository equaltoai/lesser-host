package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandlePortalProvisionConsentChallenge_CreatesChallenge(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: "lab"}}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	now := time.Now().UTC()
	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WalletCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WalletCredential](t, args, 0)
		*dest = []*models.WalletCredential{{
			Username: "alice",
			Address:  "0x00000000000000000000000000000000000000aa",
			ChainID:  1,
			Type:     "ethereum",
			LinkedAt: now,
			LastUsed: now,
		}}
	}).Once()

	tdb.qConsent.On("Create").Return(nil).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
		Request:      apptheory.Request{Body: []byte(`{"admin_username":"demo"}`)},
	}
	resp, err := s.handlePortalProvisionConsentChallenge(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var out provisionConsentChallengeResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.InstanceSlug != "demo" || out.Stage != "lab" || out.AdminUsername != "demo" {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Wallet.ID == "" || out.Wallet.Address == "" || out.Wallet.Message == "" {
		t.Fatalf("expected wallet challenge fields, got %#v", out.Wallet)
	}
}

func TestHandlePortalProvisionConsentChallenge_RequiresApproval(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	tdb.stubUser.Approved = false
	s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: "lab"}}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	if _, err := s.handlePortalProvisionConsentChallenge(ctx); err == nil {
		t.Fatalf("expected forbidden for unapproved user")
	}
}

func TestHandlePortalProvisionConsentChallenge_BlocksReservedWallet(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: "lab"}}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WalletCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WalletCredential](t, args, 0)
		*dest = []*models.WalletCredential{{
			Username: "alice",
			Address:  reservedWalletLesserHostAdmin,
			ChainID:  1,
			Type:     "ethereum",
			LinkedAt: time.Now().UTC(),
			LastUsed: time.Now().UTC(),
		}}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	if _, err := s.handlePortalProvisionConsentChallenge(ctx); err == nil {
		t.Fatalf("expected reserved wallet error")
	}
}

func TestGetProvisionConsentChallenge_NormalizesNotFoundToUnauthorized(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qConsent.On("First", mock.AnythingOfType("*models.ProvisionConsentChallenge")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	_, err := s.getProvisionConsentChallenge(ctx, "missing")
	if err == nil {
		t.Fatalf("expected error")
	}

	if appErr := normalizeNotFound(err); appErr == nil || appErr.Code != "app.unauthorized" {
		t.Fatalf("expected app.unauthorized, got %#v", appErr)
	}
}
