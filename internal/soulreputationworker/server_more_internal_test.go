package soulreputationworker

import (
	"context"
	"errors"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

func TestServerRegister_RegistersRule(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	srv := NewServer(config.Config{AppName: "lesser-host", Stage: "lab"}, store.New(db), &fakeSoulPackStore{})

	app := apptheory.New()
	srv.Register(app)
}

func TestDialTipLogClient_ValidatesInputs(t *testing.T) {
	t.Parallel()

	client, err := dialTipLogClient(context.Background(), " ")
	require.Error(t, err)
	require.Nil(t, client)

	client, err = dialTipLogClient(context.Background(), "http://127.0.0.1:0")
	require.NoError(t, err)
	require.NotNil(t, client)
	client.Close()

	// Invalid URL should fail dial.
	client, err = dialTipLogClient(context.Background(), "http://%")
	require.Error(t, err)
	require.Nil(t, client)
}

func TestRequireRecomputePrereqs_ErrorsAndSuccess(t *testing.T) {
	t.Parallel()

	var nilSrv *Server
	require.Error(t, nilSrv.requireRecomputePrereqs(&apptheory.EventContext{}))

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	srv := &Server{store: st}
	require.Error(t, srv.requireRecomputePrereqs(&apptheory.EventContext{}))

	srv = &Server{store: st, packs: &fakeSoulPackStore{}}
	require.Error(t, srv.requireRecomputePrereqs(nil))

	require.NoError(t, srv.requireRecomputePrereqs(&apptheory.EventContext{}))
}

func TestTipRecomputeConfig_SkipReasons(t *testing.T) {
	t.Parallel()

	srv := &Server{cfg: config.Config{SoulEnabled: false}}
	_, _, skip := srv.tipRecomputeConfig()
	require.Equal(t, "soul_disabled", skip)

	srv = &Server{cfg: config.Config{SoulEnabled: true, TipEnabled: false}}
	_, _, skip = srv.tipRecomputeConfig()
	require.Equal(t, "tip_disabled", skip)

	srv = &Server{cfg: config.Config{SoulEnabled: true, TipEnabled: true, TipRPCURL: ""}}
	_, _, skip = srv.tipRecomputeConfig()
	require.Equal(t, "tip_rpc_not_configured", skip)

	srv = &Server{cfg: config.Config{SoulEnabled: true, TipEnabled: true, TipRPCURL: "https://rpc", TipContractAddress: "not-an-address"}}
	_, _, skip = srv.tipRecomputeConfig()
	require.Equal(t, "tip_contract_not_configured", skip)
}

func TestTipIngestRange_ClampsAndDefaults(t *testing.T) {
	t.Parallel()

	srv := &Server{cfg: config.Config{SoulReputationTipStartBlock: 100, SoulReputationTipBlockChunkSize: 0}}
	from, chunk := srv.tipIngestRange(20)
	require.Equal(t, uint64(20), from)
	require.Equal(t, uint64(5000), chunk)
}

func TestRecomputePhaseError_LogsOnlySanitizedPhase(t *testing.T) {
	t.Parallel()

	const rawAgentID = "0x00000000000000000000000000000000000000000000000000000000000000aa"
	var logged []string
	srv := &Server{
		logPhaseFailure: func(ctx *apptheory.EventContext, phase string) {
			require.Equal(t, "rid", ctx.RequestID)
			logged = append(logged, phase)
		},
	}

	err := srv.recomputePhaseError(
		&apptheory.EventContext{RequestID: "rid"},
		"compute_and_persist_reputations",
		errors.New("failed to persist reputation for "+rawAgentID),
	)
	require.Error(t, err)
	require.ErrorContains(t, err, rawAgentID)
	require.Equal(t, []string{"compute_and_persist_reputations"}, logged)
	require.NotContains(t, logged[0], rawAgentID)
}

func TestNormalizeRecomputePhase(t *testing.T) {
	t.Parallel()

	require.Equal(t, "list_identities", normalizeRecomputePhase(" LIST_IDENTITIES "))
	require.Equal(t, "unknown", normalizeRecomputePhase("list_identities\nextra"))
	require.Equal(t, "unknown", normalizeRecomputePhase(""))
}
