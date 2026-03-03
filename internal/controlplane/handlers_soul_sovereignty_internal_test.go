package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulSovereigntyTestDB struct {
	db        *ttmocks.MockExtendedDB
	qDomain   *ttmocks.MockQuery
	qInstance *ttmocks.MockQuery
	qIdentity *ttmocks.MockQuery
	qChal     *ttmocks.MockQuery
	qRec      *ttmocks.MockQuery
}

func newSoulSovereigntyTestDB() soulSovereigntyTestDB {
	db := ttmocks.NewMockExtendedDB()
	qDomain := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qChal := new(ttmocks.MockQuery)
	qRec := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentValidationChallenge")).Return(qChal).Maybe()

	for _, q := range []*ttmocks.MockQuery{qDomain, qInstance, qIdentity, qChal, qRec} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
	}

	return soulSovereigntyTestDB{
		db:        db,
		qDomain:   qDomain,
		qInstance: qInstance,
		qIdentity: qIdentity,
		qChal:     qChal,
		qRec:      qRec,
	}
}

func TestHandleSoulValidationOptIn_DeclineCreatesZeroScoreRecord(t *testing.T) {
	t.Parallel()

	tdb := newSoulSovereigntyTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := "0x" + strings.Repeat("11", 32)
	chalID := "chal-1"

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentIDHex,
			Domain:    "example.com",
			LocalID:   "agent",
			Wallet:    "0x0000000000000000000000000000000000000001",
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()

	tdb.qChal.On("First", mock.AnythingOfType("*models.SoulAgentValidationChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentValidationChallenge](t, args, 0)
		*dest = models.SoulAgentValidationChallenge{
			AgentID:       agentIDHex,
			ChallengeID:   chalID,
			ChallengeType: "identity_verify",
			ValidatorID:   soulValidatorSystem,
			Request:       "req",
			Status:        models.SoulValidationChallengeStatusIssued,
			OptInStatus:   models.SoulValidationOptInStatusPending,
			IssuedAt:      time.Now().Add(-time.Minute).UTC(),
			UpdatedAt:     time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()

	var createdRec *models.SoulAgentValidationRecord
	tdb.db.On("Model", mock.AnythingOfType("*models.SoulAgentValidationRecord")).Return(tdb.qRec).Run(func(args mock.Arguments) {
		rec := testutil.RequireMockArg[*models.SoulAgentValidationRecord](t, args, 0)
		createdRec = rec
	}).Once()

	body, _ := json.Marshal(map[string]any{"accepted": false})
	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex, "challengeId": chalID},
		Request:      apptheory.Request{Body: body},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulValidationOptIn(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	if createdRec == nil {
		t.Fatalf("expected validation record to be created")
	}
	if createdRec.Result != models.SoulValidationResultDeclined {
		t.Fatalf("expected declined result, got %q", createdRec.Result)
	}
	if createdRec.Score != 0 {
		t.Fatalf("expected score 0, got %v", createdRec.Score)
	}
	if createdRec.OptInStatus != models.SoulValidationOptInStatusDeclined {
		t.Fatalf("expected optInStatus declined, got %q", createdRec.OptInStatus)
	}
}

func TestHandleSoulValidationOptIn_NotFoundChallenge(t *testing.T) {
	t.Parallel()

	tdb := newSoulSovereigntyTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := "0x" + strings.Repeat("11", 32)

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentIDHex,
			Domain:    "example.com",
			LocalID:   "agent",
			Wallet:    "0x0000000000000000000000000000000000000001",
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()

	tdb.qChal.On("First", mock.AnythingOfType("*models.SoulAgentValidationChallenge")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(map[string]any{"accepted": true})
	ctx := &apptheory.Context{
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex, "challengeId": "missing"},
		Request:      apptheory.Request{Body: body},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	_, err := s.handleSoulValidationOptIn(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
}
