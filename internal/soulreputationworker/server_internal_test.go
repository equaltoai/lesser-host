package soulreputationworker

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type fakeTipClient struct {
	head uint64
	logs []types.Log
}

func (f *fakeTipClient) BlockNumber(ctx context.Context) (uint64, error) {
	return f.head, nil
}

func (f *fakeTipClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	start := uint64(0)
	end := uint64(^uint64(0))
	if q.FromBlock != nil {
		start = q.FromBlock.Uint64()
	}
	if q.ToBlock != nil {
		end = q.ToBlock.Uint64()
	}

	addrSet := map[common.Address]struct{}{}
	for _, addr := range q.Addresses {
		addrSet[addr] = struct{}{}
	}

	checkTopic0 := len(q.Topics) > 0 && len(q.Topics[0]) > 0
	wantTopic0 := common.Hash{}
	if checkTopic0 {
		wantTopic0 = q.Topics[0][0]
	}

	out := make([]types.Log, 0, len(f.logs))
	for _, lg := range f.logs {
		if lg.BlockNumber < start || lg.BlockNumber > end {
			continue
		}
		if len(addrSet) > 0 {
			if _, ok := addrSet[lg.Address]; !ok {
				continue
			}
		}
		if checkTopic0 {
			if len(lg.Topics) == 0 || lg.Topics[0] != wantTopic0 {
				continue
			}
		}
		out = append(out, lg)
	}

	return out, nil
}

func (f *fakeTipClient) Close() {}

type fakeSoulPackStore struct {
	key          string
	body         []byte
	contentType  string
	cacheControl string
	putCalls     int
}

func (f *fakeSoulPackStore) PutObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error {
	f.key = key
	f.body = append([]byte(nil), body...)
	f.contentType = contentType
	f.cacheControl = cacheControl
	f.putCalls++
	return nil
}

func TestPutAgentReputation_CreatesWhenMissing(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qRep := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qRep).Maybe()

	qRep.On("WithConditionExpression", mock.Anything, mock.Anything).Return(qRep).Once()
	qRep.On("Update", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		fields, ok := args.Get(0).([]string)
		if !ok {
			t.Fatalf("expected []string fields, got %T", args.Get(0))
		}

		want := map[string]struct{}{
			"AgentID":              {},
			"Integrity":            {},
			"DelegationsCompleted": {},
			"BoundaryViolations":   {},
			"FailureRecoveries":    {},
		}
		have := map[string]struct{}{}
		for _, f := range fields {
			have[f] = struct{}{}
		}
		for w := range want {
			if _, ok := have[w]; !ok {
				t.Fatalf("expected update fields to include %q (fields=%#v)", w, fields)
			}
		}
	}).Once()

	srv := NewServer(config.Config{}, store.New(db), &fakeSoulPackStore{})

	rep := models.SoulAgentReputation{AgentID: "0x00000000000000000000000000000000000000000000000000000000000000aa"}
	if err := srv.putAgentReputation(context.Background(), &rep); err != nil {
		t.Fatalf("putAgentReputation: %v", err)
	}

	db.AssertExpectations(t)
	qRep.AssertExpectations(t)
}

func TestPutAgentReputation_SkipsWhenConditionFails(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qRep := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qRep).Maybe()

	qRep.On("WithConditionExpression", mock.Anything, mock.Anything).Return(qRep).Once()
	qRep.On("Update", mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()

	srv := NewServer(config.Config{}, store.New(db), &fakeSoulPackStore{})

	rep := models.SoulAgentReputation{
		AgentID:   "0x00000000000000000000000000000000000000000000000000000000000000aa",
		BlockRef:  123,
		Composite: 0.5,
	}
	if err := srv.putAgentReputation(context.Background(), &rep); err != nil {
		t.Fatalf("putAgentReputation: %v", err)
	}

	db.AssertExpectations(t)
	qRep.AssertExpectations(t)
}

func TestHandleRecompute_EndToEndFixture(t *testing.T) {
	t.Parallel()

	fx := newRecomputeFixture(t)

	_, err := fx.srv.handleRecompute(&apptheory.EventContext{RequestID: "r1"}, events.EventBridgeEvent{})
	if err != nil {
		t.Fatalf("handleRecompute: %v", err)
	}

	snap := requireRecomputeSnapshot(t, fx.packs, "registry/v1/reputation/snapshots/chain-111/block-20.json")
	assertRecomputeSnapshotMetadata(t, snap, fx.fixedNow)
	assertRecomputeSnapshotReputations(t, snap, fx.agentA, fx.agentB, fx.agentC)

	fx.db.AssertExpectations(t)
	fx.qIdentity.AssertExpectations(t)
	fx.qRep.AssertExpectations(t)
	fx.qVal.AssertExpectations(t)
}

func TestComputeIntegritySignals_UsesDelegationOutcomesBoundariesFailuresAndEndorsements(t *testing.T) {
	t.Parallel()

	agentID := "0x00000000000000000000000000000000000000000000000000000000000000aa"

	db := ttmocks.NewMockExtendedDB()
	qRel := new(ttmocks.MockQuery)
	qFail := new(ttmocks.MockQuery)
	qComm := new(ttmocks.MockQuery)
	qBoundary := new(ttmocks.MockQuery)
	qV1Endorse := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentRelationship")).Return(qRel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentFailure")).Return(qFail).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qComm).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(qBoundary).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPeerEndorsement")).Return(qV1Endorse).Maybe()

	for _, q := range []*ttmocks.MockQuery{qRel, qFail, qComm, qBoundary, qV1Endorse} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	}
	qComm.On("OrderBy", mock.Anything, mock.Anything).Return(qComm).Maybe()
	qComm.On("Limit", mock.Anything).Return(qComm).Maybe()
	// Bounded partition walks (issue #1061 part B): each full-set helper reads
	// via Limit + AllPaginated pages instead of a no-Limit All.
	for _, q := range []*ttmocks.MockQuery{qRel, qFail, qBoundary, qV1Endorse} {
		q.On("Limit", mock.Anything).Return(q).Once()
	}

	qRel.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentRelationship")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentRelationship)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentRelationship, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentRelationship{
			{FromAgentID: "0xfrom1", ToAgentID: agentID, Type: "delegation", ContextJSON: `{"outcome":"completed","qualityScore":0.9}`},
			{FromAgentID: "0xfrom2", ToAgentID: agentID, Type: "delegation", ContextJSON: `{"outcome":"failed","qualityScore":0.2}`},
			{FromAgentID: "0xendorser2", ToAgentID: agentID, Type: "endorsement", ContextJSON: `{}`},
		}
	}).Once()

	qFail.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentFailure")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentFailure)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentFailure, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentFailure{
			{AgentID: agentID, FailureType: "boundary_violation", Status: "open"},
			{AgentID: agentID, FailureType: "operational", Status: "recovered"},
		}
	}).Once()

	qBoundary.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentBoundary)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentBoundary, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentBoundary{{AgentID: agentID, BoundaryID: "b1", Statement: "no financial advice"}}
	}).Once()

	qV1Endorse.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentPeerEndorsement")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentPeerEndorsement)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentPeerEndorsement, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentPeerEndorsement{{AgentID: agentID, EndorserAgentID: "0xendorser"}}
	}).Once()

	qComm.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentCommActivity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentCommActivity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentCommActivity{}
	}).Once()

	srv := NewServer(config.Config{}, store.New(db), &fakeSoulPackStore{})
	got, err := srv.computeIntegritySignals(context.Background(), agentID)
	if err != nil {
		t.Fatalf("computeIntegritySignals: %v", err)
	}

	if got.endorsements != 2 {
		t.Fatalf("expected endorsements=2, got %#v", got)
	}
	if got.delegationsCompleted != 1 {
		t.Fatalf("expected delegationsCompleted=1, got %#v", got)
	}
	if got.boundaryViolations != 1 || got.failureRecoveries != 1 {
		t.Fatalf("expected boundaryViolations=1 failureRecoveries=1, got %#v", got)
	}

	// Expected score:
	// base 0.5 + boundariesDeclared 0.3 + delegationOutcome (0.9/2)*0.2 = 0.09
	// + failures: total=2 recovered=1 => -0.2*(1-0.5)= -0.1, +0.1*0.5=+0.05
	// - boundaryViolations 0.15
	wantScore := 0.69
	if math.Abs(got.score-wantScore) > 1e-9 {
		t.Fatalf("expected score=%f, got %f", wantScore, got.score)
	}

	db.AssertExpectations(t)
	qRel.AssertExpectations(t)
	qFail.AssertExpectations(t)
	qBoundary.AssertExpectations(t)
	qV1Endorse.AssertExpectations(t)
}

type recomputeFixture struct {
	agentA   string
	agentB   string
	agentC   string
	fixedNow time.Time

	srv   *Server
	packs *fakeSoulPackStore

	db        *ttmocks.MockExtendedDB
	qIdentity *ttmocks.MockQuery
	qRep      *ttmocks.MockQuery
	qVal      *ttmocks.MockQuery
}

func newRecomputeFixture(t *testing.T) recomputeFixture {
	t.Helper()

	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"
	agentC := "0x00000000000000000000000000000000000000000000000000000000000000cc"
	fixedNow := time.Date(2026, 2, 21, 12, 0, 0, 0, time.UTC)

	contract := common.HexToAddress("0x0000000000000000000000000000000000000001")
	topicAgentA := common.HexToHash(agentA)
	topicAgentB := common.HexToHash(agentB)

	logs := []types.Log{
		{Address: contract, BlockNumber: 10, Topics: []common.Hash{agentTipSentTopic0, common.Hash{}, topicAgentA, common.Hash{}}},
		{Address: contract, BlockNumber: 12, Topics: []common.Hash{agentTipSentTopic0, common.Hash{}, topicAgentA, common.Hash{}}},
		{Address: contract, BlockNumber: 15, Topics: []common.Hash{agentTipSentTopic0, common.Hash{}, topicAgentA, common.Hash{}}},
		{Address: contract, BlockNumber: 11, Topics: []common.Hash{agentTipSentTopic0, common.Hash{}, topicAgentB, common.Hash{}}},
	}

	fakeClient := &fakeTipClient{head: 20, logs: logs}

	db := ttmocks.NewMockExtendedDB()
	qIdentity := new(ttmocks.MockQuery)
	qRep := new(ttmocks.MockQuery)
	qVal := new(ttmocks.MockQuery)
	qRel := new(ttmocks.MockQuery)
	qFail := new(ttmocks.MockQuery)
	qComm := new(ttmocks.MockQuery)
	qBoundary := new(ttmocks.MockQuery)
	qV1Endorse := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qRep).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentValidationRecord")).Return(qVal).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentRelationship")).Return(qRel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentFailure")).Return(qFail).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qComm).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(qBoundary).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPeerEndorsement")).Return(qV1Endorse).Maybe()

	qMarker := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentityGSI3BackfillMarker")).Return(qMarker).Maybe()
	qMarker.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qMarker).Maybe()
	qMarker.On("First", mock.AnythingOfType("*models.SoulAgentIdentityGSI3BackfillMarker")).Return(nil).Maybe()

	// Identity enumeration now walks the gsi3 status index: one bounded
	// AllPaginated page per lifecycle status, in SoulAgentIdentityStatuses
	// order (pending, active, suspended, self_suspended, archived, succeeded,
	// burned). The first three statuses carry the fixture's three identities;
	// the remaining four return empty pages.
	qIdentity.On("Index", mock.Anything).Return(qIdentity).Maybe()
	qIdentity.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qIdentity).Maybe()
	qIdentity.On("Limit", mock.Anything).Return(qIdentity).Maybe()
	qIdentity.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentIdentity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentIdentity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentIdentity{
			{AgentID: agentC, Status: models.SoulAgentStatusPending},
		}
	}).Once()
	qIdentity.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentIdentity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentIdentity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentIdentity{
			{AgentID: agentA, Status: models.SoulAgentStatusActive},
		}
	}).Once()
	qIdentity.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentIdentity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentIdentity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentIdentity{
			{AgentID: agentB, Status: models.SoulAgentStatusSuspended},
		}
	}).Once()
	qIdentity.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentIdentity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentIdentity, got %T", args.Get(0))
		}
		*dest = nil
	}).Times(4)

	qRep.On("WithConditionExpression", mock.Anything, mock.Anything).Return(qRep).Maybe()
	qRep.On("Update", mock.Anything).Return(nil).Times(3)

	qRel.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qRel).Maybe()
	qRel.On("Limit", mock.Anything).Return(qRel).Maybe()
	qRel.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Maybe()

	qFail.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qFail).Maybe()
	qFail.On("Limit", mock.Anything).Return(qFail).Maybe()
	qFail.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Maybe()

	qComm.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qComm).Maybe()
	qComm.On("OrderBy", mock.Anything, mock.Anything).Return(qComm).Maybe()
	qComm.On("Limit", mock.Anything).Return(qComm).Maybe()
	qComm.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentCommActivity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentCommActivity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentCommActivity{}
	}).Maybe()

	qBoundary.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qBoundary).Maybe()
	qBoundary.On("Limit", mock.Anything).Return(qBoundary).Maybe()
	qBoundary.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Maybe()

	qV1Endorse.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qV1Endorse).Maybe()
	qV1Endorse.On("Limit", mock.Anything).Return(qV1Endorse).Maybe()
	qV1Endorse.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Maybe()

	qVal.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qVal).Maybe()
	qVal.On("OrderBy", mock.Anything, mock.Anything).Return(qVal).Maybe()
	qVal.On("Limit", mock.Anything).Return(qVal).Maybe()
	valCalls := 0
	qVal.On("AllPaginated", mock.Anything).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentValidationRecord)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentValidationRecord, got %T", args.Get(0))
		}
		if valCalls == 0 {
			*dest = []*models.SoulAgentValidationRecord{
				{AgentID: agentA, ChallengeID: "c1", ChallengeType: "identity_verify", ValidatorID: "system", Result: "pass", Score: 0.2, EvaluatedAt: fixedNow},
			}
		} else {
			*dest = nil
		}
		valCalls++
	}).Return(&core.PaginatedResult{}, nil).Once()

	packs := &fakeSoulPackStore{}
	cfg := config.Config{
		AppName: "lesser-host",
		Stage:   "lab",

		SoulEnabled:        true,
		TipEnabled:         true,
		TipChainID:         111,
		TipRPCURL:          "https://rpc",
		TipContractAddress: contract.Hex(),

		SoulReputationTipStartBlock:     1,
		SoulReputationTipBlockChunkSize: 100,
		SoulReputationTipScale:          10,
		SoulReputationWeightEconomic:    1,
		SoulReputationWeightSocial:      0,
		SoulReputationWeightValidation:  0,
		SoulReputationWeightTrust:       0,
	}

	srv := NewServer(cfg, store.New(db), packs)
	srv.dialTip = func(ctx context.Context, rpcURL string) (tipLogClient, error) {
		return fakeClient, nil
	}

	srv.now = func() time.Time { return fixedNow }

	return recomputeFixture{
		agentA:    agentA,
		agentB:    agentB,
		agentC:    agentC,
		fixedNow:  fixedNow,
		srv:       srv,
		packs:     packs,
		db:        db,
		qIdentity: qIdentity,
		qRep:      qRep,
		qVal:      qVal,
	}
}

func requireRecomputeSnapshot(t *testing.T, packs *fakeSoulPackStore, wantKey string) reputationSnapshot {
	t.Helper()

	if packs.putCalls != 1 {
		t.Fatalf("expected 1 snapshot write, got %d", packs.putCalls)
	}
	if packs.key != wantKey {
		t.Fatalf("unexpected snapshot key %q", packs.key)
	}

	var snap reputationSnapshot
	if err := json.Unmarshal(packs.body, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return snap
}

func assertRecomputeSnapshotMetadata(t *testing.T, snap reputationSnapshot, fixedNow time.Time) {
	t.Helper()

	if snap.ToBlock != 20 || snap.FromBlock != 1 || snap.ChainID != 111 {
		t.Fatalf("unexpected snapshot metadata: %#v", snap)
	}
	if !snap.ComputedAt.Equal(fixedNow) {
		t.Fatalf("unexpected computed_at: %v", snap.ComputedAt)
	}
	if snap.Weights.Economic != 1 || snap.Weights.Social != 0 || snap.Weights.Validation != 0 || snap.Weights.Trust != 0 {
		t.Fatalf("unexpected weights: %#v", snap.Weights)
	}
}

func assertRecomputeSnapshotReputations(t *testing.T, snap reputationSnapshot, agentA string, agentB string, agentC string) {
	t.Helper()

	if len(snap.Reputations) != 3 {
		t.Fatalf("expected 3 reputations with suppressed agents zeroed, got %d", len(snap.Reputations))
	}
	assertRecomputeSnapshotOrdering(t, snap, agentA, agentB, agentC)
	assertActiveReputation(t, snap.Reputations[0])
	assertZeroReputation(t, snap.Reputations[1], "suppressed rep B")
	assertZeroReputation(t, snap.Reputations[2], "rep C")
}

func assertRecomputeSnapshotOrdering(t *testing.T, snap reputationSnapshot, agentA string, agentB string, agentC string) {
	t.Helper()

	if snap.Reputations[0].AgentID != agentA {
		t.Fatalf("unexpected first reputation: %#v", snap.Reputations)
	}
	if snap.Reputations[1].AgentID != agentB {
		t.Fatalf("unexpected second reputation: %#v", snap.Reputations)
	}
	if snap.Reputations[2].AgentID != agentC {
		t.Fatalf("unexpected third reputation: %#v", snap.Reputations)
	}
}

func assertActiveReputation(t *testing.T, got models.SoulAgentReputation) {
	t.Helper()

	wantEconomic := 1 - math.Exp(-0.3)
	if got.TipsReceived != 3 {
		t.Fatalf("unexpected active tip count: %#v", got)
	}
	if got.ValidationsPassed != 1 {
		t.Fatalf("unexpected active validation count: %#v", got)
	}
	if math.Abs(got.Validation-0.2) > 1e-9 {
		t.Fatalf("unexpected active validation score: %#v", got)
	}
	if math.Abs(got.Economic-wantEconomic) > 1e-9 {
		t.Fatalf("unexpected active economic score: %#v", got)
	}
	if math.Abs(got.Composite-wantEconomic) > 1e-9 {
		t.Fatalf("unexpected active composite score: %#v", got)
	}
}

func assertZeroReputation(t *testing.T, got models.SoulAgentReputation, label string) {
	t.Helper()

	if got.TipsReceived != 0 {
		t.Fatalf("expected zero tips for %s: %#v", label, got)
	}
	if got.ValidationsPassed != 0 {
		t.Fatalf("expected zero validations for %s: %#v", label, got)
	}
	if got.Validation != 0 || got.Composite != 0 || got.Economic != 0 {
		t.Fatalf("expected zero scores for %s: %#v", label, got)
	}
}

// newWorkerWalkQuery wires a query mock for one worker partition read that
// returns two pages (HasMore on the first) and captures the applied limits.
func newWorkerWalkQuery(t *testing.T, db *ttmocks.MockExtendedDB, modelType string) (*ttmocks.MockQuery, *[]int) {
	t.Helper()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType(modelType)).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
	appliedLimits := []int{}
	q.On("Limit", mock.Anything).Return(q).Times(2).Run(func(args mock.Arguments) {
		if n, ok := args.Get(0).(int); ok {
			appliedLimits = append(appliedLimits, n)
		}
	})
	q.On("Cursor", "after-1").Return(q).Once()
	return q, &appliedLimits
}

// TestWorkerPartitionReads_BoundedPerIdentity verifies the five per-identity
// worker reads (issue #1061 part B, sites 15-19) all issue page-capped
// queries with cursor resume and never a Scan, and that the walk fails closed
// rather than silently truncating a partition past the page cap.
func TestWorkerPartitionReads_BoundedPerIdentity(t *testing.T) {
	t.Parallel()

	agentID := "0x00000000000000000000000000000000000000000000000000000000000000aa"

	cases := []struct {
		name      string
		modelType string
		call      func(srv *Server) (int, error)
		pageOne   func(dest any)
		pageTwo   func(dest any)
	}{
		{
			name:      "validation_records",
			modelType: "*models.SoulAgentValidationRecord",
			call: func(srv *Server) (int, error) {
				items, err := srv.listAgentValidationRecords(context.Background(), agentID)
				return len(items), err
			},
			pageOne: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentValidationRecord)
				*d = []*models.SoulAgentValidationRecord{{AgentID: agentID, ChallengeID: "c1", Result: "pass", Score: 0.5}}
			},
			pageTwo: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentValidationRecord)
				*d = []*models.SoulAgentValidationRecord{{AgentID: agentID, ChallengeID: "c2", Result: "pass", Score: 0.6}}
			},
		},
		{
			name:      "relationships",
			modelType: "*models.SoulAgentRelationship",
			call: func(srv *Server) (int, error) {
				items, err := srv.listSoulRelationships(context.Background(), agentID)
				return len(items), err
			},
			pageOne: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentRelationship)
				*d = []*models.SoulAgentRelationship{{FromAgentID: "0xa", ToAgentID: agentID, Type: "delegation"}}
			},
			pageTwo: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentRelationship)
				*d = []*models.SoulAgentRelationship{{FromAgentID: "0xb", ToAgentID: agentID, Type: "endorsement"}}
			},
		},
		{
			name:      "failures",
			modelType: "*models.SoulAgentFailure",
			call: func(srv *Server) (int, error) {
				items, err := srv.listSoulAgentFailures(context.Background(), agentID)
				return len(items), err
			},
			pageOne: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentFailure)
				*d = []*models.SoulAgentFailure{{AgentID: agentID, FailureID: "f1", FailureType: "boundary_violation", Status: "open"}}
			},
			pageTwo: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentFailure)
				*d = []*models.SoulAgentFailure{{AgentID: agentID, FailureID: "f2", FailureType: "operational", Status: "recovered"}}
			},
		},
		{
			name:      "boundaries",
			modelType: "*models.SoulAgentBoundary",
			call: func(srv *Server) (int, error) {
				declared, err := srv.countDeclaredBoundaries(context.Background(), agentID)
				return int(declared), err
			},
			pageOne: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentBoundary)
				*d = []*models.SoulAgentBoundary{{AgentID: agentID, BoundaryID: "b1"}}
			},
			pageTwo: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentBoundary)
				*d = []*models.SoulAgentBoundary{{AgentID: agentID, BoundaryID: "b2"}, nil}
			},
		},
		{
			name:      "legacy_endorsements",
			modelType: "*models.SoulAgentPeerEndorsement",
			call: func(srv *Server) (int, error) {
				endorsers := map[string]struct{}{}
				err := srv.addLegacyEndorsers(context.Background(), agentID, endorsers)
				return len(endorsers), err
			},
			pageOne: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentPeerEndorsement)
				*d = []*models.SoulAgentPeerEndorsement{{AgentID: agentID, EndorserAgentID: "0xe1"}}
			},
			pageTwo: func(dest any) {
				d, _ := dest.(*[]*models.SoulAgentPeerEndorsement)
				*d = []*models.SoulAgentPeerEndorsement{{AgentID: agentID, EndorserAgentID: "0xe2"}}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := ttmocks.NewMockExtendedDB()
			q, appliedLimits := newWorkerWalkQuery(t, db, tc.modelType)
			q.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{HasMore: true, NextCursor: "after-1"}, nil).Run(func(args mock.Arguments) {
				tc.pageOne(args.Get(0))
			}).Once()
			q.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
				tc.pageTwo(args.Get(0))
			}).Once()

			srv := NewServer(config.Config{}, store.New(db), &fakeSoulPackStore{})
			count, err := tc.call(srv)
			require.NoError(t, err)
			require.Equal(t, 2, count)
			require.Equal(t, []int{workerPartitionWalkPageSize, workerPartitionWalkPageSize}, *appliedLimits)
			q.AssertNotCalled(t, "Scan", mock.Anything)
		})
	}
}
