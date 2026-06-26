package hostedgenesis

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplyTurnLedgerAppendsNewTurnAndDebits(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	decision, err := ApplyTurnLedger(nil, TurnLedgerEntry{
		TurnID:           " turn_1 ",
		IdempotencyKey:   " idem_1 ",
		RequestHash:      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		BillingLedgerRef: "usage:mint:conv_1:turn_1",
		ChargedCredits:   1,
		AcceptedAt:       now,
	})
	require.NoError(t, err)
	require.False(t, decision.Replayed)
	require.True(t, decision.ShouldDebit)
	require.Equal(t, "turn_1", decision.LatestTurnID)
	require.Equal(t, 1, decision.MessageCount)
	require.Len(t, decision.Entries, 1)
	require.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", decision.Turn.RequestHash)
}

func TestApplyTurnLedgerReplaysSameIdempotencyWithoutDuplicateTurnOrDebit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	existing := []TurnLedgerEntry{{
		TurnID:           "turn_original",
		IdempotencyKey:   "idem_1",
		RequestHash:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BillingLedgerRef: "usage:mint:conv_1:turn_original",
		ChargedCredits:   1,
		MessageCount:     1,
		AcceptedAt:       now,
	}}
	decision, err := ApplyTurnLedger(existing, TurnLedgerEntry{
		TurnID:           "turn_retry_must_not_persist",
		IdempotencyKey:   "idem_1",
		RequestHash:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BillingLedgerRef: "usage:mint:conv_1:turn_retry_must_not_persist",
		ChargedCredits:   1,
		MessageCount:     2,
		AcceptedAt:       now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.True(t, decision.Replayed)
	require.False(t, decision.ShouldDebit)
	require.Equal(t, "turn_original", decision.LatestTurnID)
	require.Equal(t, 1, decision.MessageCount)
	require.Len(t, decision.Entries, 1)
}

func TestApplyTurnLedgerRejectsIdempotencyHashMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	_, err := ApplyTurnLedger([]TurnLedgerEntry{{
		TurnID:         "turn_original",
		IdempotencyKey: "idem_1",
		RequestHash:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MessageCount:   1,
		AcceptedAt:     now,
	}}, TurnLedgerEntry{
		TurnID:         "turn_conflict",
		IdempotencyKey: "idem_1",
		RequestHash:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		MessageCount:   2,
		AcceptedAt:     now.Add(time.Minute),
	})
	require.ErrorIs(t, err, ErrIdempotencyConflict)
}

func TestTurnLedgerContainsNoSecretBearingPayload(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(TurnLedgerEntry{
		TurnID:             "turn_1",
		IdempotencyKey:     "idem_1",
		RequestHash:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		InputCheckpointRef: "checkpoint://hosted-genesis/input_1",
		BillingLedgerRef:   "usage:mint:conv_1:turn_1",
		ChargedCredits:     1,
		MessageCount:       1,
		AcceptedAt:         time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	jsonText := strings.ToLower(string(payload))
	for _, forbidden := range []string{
		"raw_transcript",
		"transcript",
		"messages",
		"prompt",
		"provider_key",
		"instance_api_key",
		"wallet_signature",
		"signing_material",
		"ssm_value",
		"aws_credentials",
		"provider_secret",
		"microvm_endpoint_token",
		"browser_host_credential",
	} {
		require.NotContains(t, jsonText, forbidden)
	}
}
