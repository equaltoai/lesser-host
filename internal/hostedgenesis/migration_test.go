package hostedgenesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPlanLegacySessionMigrationTerminalFailsClosed proves no legacy terminal
// conversation can migrate into declaration_ready: pre-five-body declarations
// carry no five-body contract versions, so the plan maps them to a
// deterministic restart_soul_bootstrap recovery instead of seeding a
// publishable checkpoint.
func TestPlanLegacySessionMigrationTerminalFailsClosed(t *testing.T) {
	t.Parallel()

	input := loadLegacyMigrationFixture(t, "legacy-completed-valid.json")
	plan, err := PlanLegacySessionMigration(input)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, plan.Seed.Status)
	require.Equal(t, RecoveryActionRestartSoulBootstrap, plan.RecoveryAction)
	require.NotNil(t, plan.Seed.Failure)
	require.Equal(t, FailureCodeInvalidProducedDeclarations, plan.Seed.Failure.Code)
	require.Nil(t, plan.Seed.DeclarationCheckpoint)
	require.Equal(t, "conv_legacy_ready", plan.Seed.ConversationID)
}

func TestPlanLegacySessionMigrationActiveRowsBecomeDeterministicRecovery(t *testing.T) {
	t.Parallel()

	input := loadLegacyMigrationFixture(t, "legacy-in-progress.json")
	plan, err := PlanLegacySessionMigration(input)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, plan.Seed.Status)
	require.Equal(t, RecoveryActionRestartSoulBootstrap, plan.RecoveryAction)
	require.NotNil(t, plan.Seed.Failure)
	require.Equal(t, FailureCodeInvalidCompletionState, plan.Seed.Failure.Code)
	require.Equal(t, "conv_legacy_progress", plan.Seed.ConversationID)
	require.Len(t, plan.Seed.TurnLedger, 1)
	require.Equal(t, "turn_legacy_user", plan.Seed.TurnLedger[0].TurnID)
}

func TestPlanLegacySessionMigrationFailedRetryLaneRestartsWithoutCandidate(t *testing.T) {
	t.Parallel()

	input := loadLegacyMigrationFixture(t, "legacy-in-progress.json")
	input.Status = string(StatusFailed)
	input.StatusReason = string(FailureCodeAssistantTurnFailed)
	plan, err := PlanLegacySessionMigration(input)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, plan.Seed.Status)
	require.Equal(t, RecoveryActionRestartSoulBootstrap, plan.RecoveryAction)
	require.NotNil(t, plan.Seed.Failure)
	require.Equal(t, FailureCodeAssistantTurnFailed, plan.Seed.Failure.Code)
	require.False(t, plan.Seed.Failure.Retryable)
	require.Equal(t, RecoveryActionRestartSoulBootstrap, plan.Seed.Failure.Recovery.Action)
}

func TestPlanLegacySessionMigrationInvalidDeclarationsRestartBootstrap(t *testing.T) {
	t.Parallel()

	input := loadLegacyMigrationFixture(t, "legacy-completed-invalid-declarations.json")
	plan, err := PlanLegacySessionMigration(input)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, plan.Seed.Status)
	require.Equal(t, RecoveryActionRestartSoulBootstrap, plan.RecoveryAction)
	require.NotNil(t, plan.Seed.Failure)
	require.Equal(t, FailureCodeInvalidProducedDeclarations, plan.Seed.Failure.Code)
	require.Nil(t, plan.Seed.DeclarationCheckpoint)
}

func TestPlanLegacySessionMigrationDoesNotImportRawTranscriptFields(t *testing.T) {
	t.Parallel()

	input := loadLegacyMigrationFixture(t, "legacy-in-progress.json")
	plan, err := PlanLegacySessionMigration(input)
	require.NoError(t, err)
	payload, err := json.Marshal(plan)
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

func TestPlanLegacySessionMigrationDryRunIsRepeatable(t *testing.T) {
	t.Parallel()

	input := loadLegacyMigrationFixture(t, "legacy-in-progress.json")
	first, err := PlanLegacySessionMigration(input)
	require.NoError(t, err)
	second, err := PlanLegacySessionMigration(input)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func loadLegacyMigrationFixture(t *testing.T, name string) LegacySessionInput {
	t.Helper()
	path := filepath.Join("testdata", "migration", name)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var raw struct {
		InstanceSlug                string `json:"instance_slug"`
		RegistrationID              string `json:"registration_id"`
		AgentID                     string `json:"agent_id"`
		ConversationID              string `json:"conversation_id"`
		Status                      string `json:"status"`
		StatusReason                string `json:"status_reason"`
		LatestTurnID                string `json:"latest_turn_id"`
		MessageCount                int    `json:"message_count"`
		Model                       string `json:"model"`
		RequestID                   string `json:"request_id"`
		CorrelationID               string `json:"correlation_id"`
		IdempotencyKey              string `json:"idempotency_key"`
		RequestHash                 string `json:"request_hash"`
		ProducedDeclarationsPresent bool   `json:"produced_declarations_present"`
		ProducedDeclarationsValid   bool   `json:"produced_declarations_valid"`
		DeclarationID               string `json:"declaration_id"`
		DeclarationHash             string `json:"declaration_hash"`
		DeclarationCheckpointRef    string `json:"declaration_checkpoint_ref"`
		CreatedAt                   string `json:"created_at"`
		UpdatedAt                   string `json:"updated_at"`
		CompletedAt                 string `json:"completed_at"`
	}
	require.NoError(t, json.Unmarshal(b, &raw))
	return LegacySessionInput{
		InstanceSlug:                raw.InstanceSlug,
		RegistrationID:              raw.RegistrationID,
		AgentID:                     raw.AgentID,
		ConversationID:              raw.ConversationID,
		Status:                      raw.Status,
		StatusReason:                raw.StatusReason,
		LatestTurnID:                raw.LatestTurnID,
		MessageCount:                raw.MessageCount,
		Model:                       raw.Model,
		RequestID:                   raw.RequestID,
		CorrelationID:               raw.CorrelationID,
		IdempotencyKey:              raw.IdempotencyKey,
		RequestHash:                 raw.RequestHash,
		ProducedDeclarationsPresent: raw.ProducedDeclarationsPresent,
		ProducedDeclarationsValid:   raw.ProducedDeclarationsValid,
		DeclarationID:               raw.DeclarationID,
		DeclarationHash:             raw.DeclarationHash,
		DeclarationCheckpointRef:    raw.DeclarationCheckpointRef,
		CreatedAt:                   parseFixtureTime(t, raw.CreatedAt),
		UpdatedAt:                   parseFixtureTime(t, raw.UpdatedAt),
		CompletedAt:                 parseFixtureTime(t, raw.CompletedAt),
	}
}

func parseFixtureTime(t *testing.T, value string) time.Time {
	t.Helper()
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}
