package soulreputationworker

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type tipClientWithErrors struct {
	head        uint64
	blockErr    error
	filterErr   error
	logs        []types.Log
	closeCalled bool
}

func (c *tipClientWithErrors) BlockNumber(ctx context.Context) (uint64, error) {
	if c.blockErr != nil {
		return 0, c.blockErr
	}
	return c.head, nil
}

func (c *tipClientWithErrors) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	if c.filterErr != nil {
		return nil, c.filterErr
	}
	return c.logs, nil
}

func (c *tipClientWithErrors) Close() {
	c.closeCalled = true
}

func TestHandleRecompute_SkipAndFailureBranches(t *testing.T) {
	t.Parallel()

	makeServer := func(cfg config.Config, db *ttmocks.MockExtendedDB) *Server {
		t.Helper()
		return NewServer(cfg, store.New(db), &fakeSoulPackStore{})
	}

	t.Run("skip reason returned when config disables recompute", func(t *testing.T) {
		t.Parallel()

		srv := makeServer(config.Config{SoulEnabled: false}, ttmocks.NewMockExtendedDB())
		got, err := srv.handleRecompute(&apptheory.EventContext{}, events.EventBridgeEvent{})
		require.NoError(t, err)
		require.Equal(t, map[string]any{"skipped": "soul_disabled"}, got)
	})

	t.Run("dial tip failure is wrapped", func(t *testing.T) {
		t.Parallel()

		srv := makeServer(config.Config{
			SoulEnabled:        true,
			TipEnabled:         true,
			TipRPCURL:          "https://rpc.example",
			TipContractAddress: "0x0000000000000000000000000000000000000001",
		}, ttmocks.NewMockExtendedDB())
		srv.dialTip = func(ctx context.Context, rpcURL string) (tipLogClient, error) {
			return nil, errors.New("dial boom")
		}

		_, err := srv.handleRecompute(&apptheory.EventContext{}, events.EventBridgeEvent{})
		require.ErrorContains(t, err, "failed to dial tip rpc: dial boom")
	})

	t.Run("head block failure closes client and is wrapped", func(t *testing.T) {
		t.Parallel()

		srv := makeServer(config.Config{
			SoulEnabled:        true,
			TipEnabled:         true,
			TipRPCURL:          "https://rpc.example",
			TipContractAddress: "0x0000000000000000000000000000000000000001",
		}, ttmocks.NewMockExtendedDB())
		client := &tipClientWithErrors{blockErr: errors.New("head boom")}
		srv.dialTip = func(ctx context.Context, rpcURL string) (tipLogClient, error) {
			return client, nil
		}

		_, err := srv.handleRecompute(&apptheory.EventContext{}, events.EventBridgeEvent{})
		require.ErrorContains(t, err, "failed to read head block: head boom")
		require.True(t, client.closeCalled)
	})

	t.Run("identity listing failure is wrapped", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDB()
		qIdentity := new(ttmocks.MockQuery)
		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
		qIdentity.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qIdentity).Maybe()
		qIdentity.On("All", mock.Anything).Return(errors.New("identity boom")).Once()

		srv := makeServer(config.Config{
			SoulEnabled:        true,
			TipEnabled:         true,
			TipRPCURL:          "https://rpc.example",
			TipContractAddress: "0x0000000000000000000000000000000000000001",
		}, db)
		client := &tipClientWithErrors{head: 10}
		srv.dialTip = func(ctx context.Context, rpcURL string) (tipLogClient, error) {
			return client, nil
		}

		_, err := srv.handleRecompute(&apptheory.EventContext{}, events.EventBridgeEvent{})
		require.ErrorContains(t, err, "failed to list identities: identity boom")
		require.True(t, client.closeCalled)
	})
}

func TestComputeCommunicationSignals_AggregatesRecentTrafficAndResponses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	agentID := "0x00000000000000000000000000000000000000000000000000000000000000aa"

	db := ttmocks.NewMockExtendedDB()
	qComm := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qComm).Maybe()
	qComm.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qComm).Maybe()
	qComm.On("OrderBy", mock.Anything, mock.Anything).Return(qComm).Maybe()
	qComm.On("Limit", mock.Anything).Return(qComm).Maybe()
	qComm.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentCommActivity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentCommActivity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentCommActivity{
			nil,
			{AgentID: agentID, ActivityID: "old", ChannelType: "email", Direction: models.SoulCommDirectionInbound, Action: "receive", MessageID: "old-1", Timestamp: now.Add(-31 * 24 * time.Hour)},
			{AgentID: agentID, ActivityID: "skip-action", ChannelType: "email", Direction: models.SoulCommDirectionInbound, Action: "open", MessageID: "ignored", Timestamp: now.Add(-2 * time.Hour)},
			{AgentID: agentID, ActivityID: "in-1", ChannelType: "email", Direction: models.SoulCommDirectionInbound, Action: "receive", MessageID: "m1", Timestamp: now.Add(-5 * time.Hour)},
			{AgentID: agentID, ActivityID: "in-2", ChannelType: "sms", Direction: models.SoulCommDirectionInbound, Action: "receive", MessageID: "m2", Timestamp: now.Add(-4 * time.Hour)},
			{AgentID: agentID, ActivityID: "in-3", ChannelType: "voice", Direction: models.SoulCommDirectionInbound, Action: "receive", MessageID: "m3", Timestamp: now.Add(-3 * time.Hour)},
			{AgentID: agentID, ActivityID: "out-before", ChannelType: "email", Direction: models.SoulCommDirectionOutbound, Action: "send", InReplyTo: "m1", Timestamp: now.Add(-6 * time.Hour)},
			{AgentID: agentID, ActivityID: "out-1", ChannelType: "email", Direction: models.SoulCommDirectionOutbound, Action: "send", InReplyTo: "m1", Timestamp: now.Add(-4 * time.Hour)},
			{AgentID: agentID, ActivityID: "out-2", ChannelType: "sms", Direction: models.SoulCommDirectionOutbound, Action: "send", InReplyTo: "m2", BoundaryCheck: models.SoulCommBoundaryCheckViolated, Timestamp: now.Add(-3 * time.Hour)},
			{AgentID: agentID, ActivityID: "out-3", ChannelType: "voice", Direction: models.SoulCommDirectionOutbound, Action: "send", InReplyTo: "m3", Timestamp: now.Add(-2 * time.Hour)},
			{AgentID: agentID, ActivityID: "out-dup", ChannelType: "email", Direction: models.SoulCommDirectionOutbound, Action: "send", InReplyTo: "m1", Timestamp: now.Add(-1 * time.Hour)},
			{AgentID: agentID, ActivityID: "out-missing", ChannelType: "email", Direction: models.SoulCommDirectionOutbound, Action: "send", InReplyTo: "missing", Timestamp: now.Add(-30 * time.Minute)},
		}
	}).Once()

	srv := NewServer(config.Config{}, store.New(db), &fakeSoulPackStore{})
	got, err := srv.computeCommunicationSignals(context.Background(), agentID, now)
	require.NoError(t, err)

	require.Equal(t, int64(1), got.emailsReceived)
	require.Equal(t, int64(1), got.smsReceived)
	require.Equal(t, int64(1), got.callsReceived)
	require.Equal(t, int64(4), got.emailsSent)
	require.Equal(t, int64(1), got.smsSent)
	require.Equal(t, int64(1), got.callsMade)
	require.Equal(t, int64(1), got.boundaryViolations)
	require.Equal(t, int64(0), got.spamReports)
	require.InDelta(t, 1.0, got.responseRate, 1e-9)
	require.InDelta(t, 60.0, got.avgResponseTimeMinutes, 1e-9)
	require.InDelta(t, 0.5+0.4+0.3*math.Exp(-1)-0.1, got.score, 1e-9)
}

func TestComputeCommunicationSignals_ErrorsAndGuards(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)

	var nilSrv *Server
	_, err := nilSrv.computeCommunicationSignals(context.Background(), "0xabc", now)
	require.ErrorContains(t, err, "store not initialized")

	srv := NewServer(config.Config{}, store.New(ttmocks.NewMockExtendedDB()), &fakeSoulPackStore{})
	_, err = srv.computeCommunicationSignals(context.Background(), " ", now)
	require.ErrorContains(t, err, "agent id is required")

	db := ttmocks.NewMockExtendedDB()
	qComm := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qComm).Maybe()
	qComm.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qComm).Maybe()
	qComm.On("OrderBy", mock.Anything, mock.Anything).Return(qComm).Maybe()
	qComm.On("Limit", mock.Anything).Return(qComm).Maybe()
	qComm.On("All", mock.Anything).Return(errors.New("comm boom")).Once()

	srv = NewServer(config.Config{}, store.New(db), &fakeSoulPackStore{})
	_, err = srv.computeCommunicationSignals(context.Background(), "0xabc", now)
	require.ErrorContains(t, err, "comm boom")
}

func TestServerHelperCoverage_MiscBranches(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", soulIdentitySortKey(nil))
	require.Equal(t, "0xabc", soulIdentitySortKey(&models.SoulAgentIdentity{AgentID: " 0xabc "}))

	require.Equal(t, "registry/v1/reputation/snapshots/block-7.json", reputationSnapshotS3Key(0, 7))
	require.Equal(t, "", reputationSnapshotSignatureS3Key(" "))
	require.Equal(t, "registry/v1/reputation/snapshots/block-7.json.sig.jws", reputationSnapshotSignatureS3Key(" registry/v1/reputation/snapshots/block-7.json "))

	require.Equal(t, 0.0, clamp01(math.NaN()))
	require.Equal(t, 0.0, clamp01(math.Inf(1)))
	require.Equal(t, 0.0, clamp01(-1))
	require.Equal(t, 1.0, clamp01(2))
	require.Equal(t, 0.25, clamp01(0.25))

	var nilSrv *Server
	_, err := nilSrv.listAgentIdentities(context.Background())
	require.ErrorContains(t, err, "store not initialized")

	_, err = nilSrv.listAgentValidationRecords(context.Background(), "0xabc")
	require.ErrorContains(t, err, "store not initialized")

	srv := NewServer(config.Config{}, store.New(ttmocks.NewMockExtendedDB()), &fakeSoulPackStore{})
	_, err = srv.listAgentValidationRecords(context.Background(), " ")
	require.ErrorContains(t, err, "agent id is required")

	err = nilSrv.putAgentReputation(context.Background(), &models.SoulAgentReputation{})
	require.ErrorContains(t, err, "store not initialized")

	err = srv.putAgentReputation(context.Background(), nil)
	require.ErrorContains(t, err, "reputation is nil")

	require.False(t, isBoundaryViolationFailureType(""))
}

func TestExtractRelationshipOutcomeAndQualityFromMap_CoversSupportedTypes(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		input      map[string]any
		outcome    string
		quality    float64
		hasQuality bool
	}

	cases := []testCase{
		{name: "nil map", input: nil, outcome: "", quality: 0, hasQuality: false},
		{name: "float qualityScore", input: map[string]any{"outcome": " Completed ", "qualityScore": 0.75}, outcome: "completed", quality: 0.75, hasQuality: true},
		{name: "int snake case", input: map[string]any{"outcome": "success", "quality_score": 1}, outcome: "success", quality: 1, hasQuality: true},
		{name: "int64", input: map[string]any{"outcome": "success", "qualityScore": int64(2)}, outcome: "success", quality: 2, hasQuality: true},
		{name: "json number", input: map[string]any{"outcome": "complete", "qualityScore": json.Number("0.5")}, outcome: "complete", quality: 0.5, hasQuality: true},
		{name: "string", input: map[string]any{"outcome": "done", "qualityScore": "0.9"}, outcome: "done", quality: 0.9, hasQuality: true},
		{name: "invalid string", input: map[string]any{"outcome": "done", "qualityScore": "nope"}, outcome: "done", quality: 0, hasQuality: false},
		{name: "unsupported type", input: map[string]any{"outcome": "done", "qualityScore": true}, outcome: "done", quality: 0, hasQuality: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			outcome, quality, hasQuality := extractRelationshipOutcomeAndQualityFromMap(tc.input)
			require.Equal(t, tc.outcome, outcome)
			require.Equal(t, tc.quality, quality)
			require.Equal(t, tc.hasQuality, hasQuality)
		})
	}

	outcome, quality, hasQuality := extractRelationshipOutcomeAndQuality(&models.SoulAgentRelationship{
		ContextJSON: `{"outcome":"completed","qualityScore":"0.6"}`,
	})
	require.Equal(t, "completed", outcome)
	require.Equal(t, 0.6, quality)
	require.True(t, hasQuality)
}
