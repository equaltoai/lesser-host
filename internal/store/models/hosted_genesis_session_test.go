package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

func TestHostedGenesisSessionModelMarshalingAndTenantScope(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	require.NoError(t, session.BeforeCreate())

	require.Equal(t, "demo", session.InstanceSlug)
	require.Equal(t, "0x2222222222222222222222222222222222222222222222222222222222222222", session.AgentID)
	require.Equal(t, "HOSTED_GENESIS#INSTANCE#demo", session.PK)
	require.Equal(t, "SESSION#conv_123", session.SK)
	require.Equal(t, "HOSTED_GENESIS#INSTANCE#demo#REGISTRATION#reg_123", session.GSI1PK)
	require.Equal(t, "HOSTED_GENESIS#INSTANCE#demo#AGENT#0x2222222222222222222222222222222222222222222222222222222222222222", session.GSI2PK)
	require.Equal(t, int64(0), session.Version)

	payload, err := json.Marshal(session)
	require.NoError(t, err)
	jsonText := string(payload)
	require.Contains(t, jsonText, `"instance_slug":"demo"`)
	require.Contains(t, jsonText, `"conversation_id":"conv_123"`)
	require.NotContains(t, jsonText, "HOSTED_GENESIS#INSTANCE#demo")
	require.NotContains(t, jsonText, "gsi1")
	require.NotContains(t, jsonText, "gsi2")
}

func TestHostedGenesisSessionRejectsUngatedTerminalStates(t *testing.T) {
	t.Parallel()

	ready := validHostedGenesisSessionModel()
	ready.Status = string(hostedgenesis.StatusDeclarationReady)
	ready.DeclarationCheckpoint = nil
	require.ErrorIs(t, ready.BeforeCreate(), hostedgenesis.ErrInvalidPublishGate)

	failed := validHostedGenesisSessionModel()
	failed.Status = string(hostedgenesis.StatusFailed)
	failed.Failure = nil
	require.ErrorIs(t, failed.BeforeCreate(), hostedgenesis.ErrInvalidFailureRecovery)
}

func TestHostedGenesisSessionRejectsInvalidTurnLedger(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	session.TurnLedger = append(session.TurnLedger, session.TurnLedger[0])
	require.ErrorIs(t, session.BeforeCreate(), hostedgenesis.ErrDuplicateTurnID)
}

func TestHostedGenesisSessionModelHasNoSecretBearingFields(t *testing.T) {
	t.Parallel()

	assertModelHasNoRawSecretFields(t, reflect.TypeOf(HostedGenesisSession{}))

	session := validHostedGenesisSessionModel()
	require.NoError(t, session.BeforeCreate())
	payload, err := json.Marshal(session)
	require.NoError(t, err)
	jsonText := strings.ToLower(string(payload))
	for _, forbiddenValue := range []string{
		"raw transcript",
		"sk-live-provider-secret",
		"instance-api-key",
		"wallet-signature",
		"aws_access_key_id",
		"microvm-endpoint-token",
	} {
		require.NotContains(t, jsonText, forbiddenValue)
	}
}

func TestHostedGenesisSessionProjectionInputIsCompact(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	session.Status = string(hostedgenesis.StatusDeclarationReady)
	checkpoint := validHostedGenesisDeclarationCheckpoint()
	session.DeclarationCheckpoint = &checkpoint
	require.NoError(t, session.BeforeCreate())

	projection, err := hostedgenesis.NewConversationProjection(session.ToProjectionInput(), true)
	require.NoError(t, err)
	require.Equal(t, hostedgenesis.StatusDeclarationReady, projection.Status)
	require.NotNil(t, projection.DeclarationCheckpoint)

	payload, err := json.Marshal(projection)
	require.NoError(t, err)
	jsonText := strings.ToLower(string(payload))
	require.NotContains(t, jsonText, "transcript")
	require.NotContains(t, jsonText, "messages")
	require.NotContains(t, jsonText, "provider_secret")
}

func TestHostedGenesisSessionFromMigrationSeed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	seed := hostedgenesis.SessionSeed{
		InstanceSlug:   " Demo ",
		RegistrationID: "reg_123",
		AgentID:        "0x2222222222222222222222222222222222222222222222222222222222222222",
		ConversationID: "conv_123",
		Status:         hostedgenesis.StatusFailed,
		LatestTurnID:   "turn_123",
		MessageCount:   1,
		TurnLedger: []hostedgenesis.TurnLedgerEntry{{
			TurnID:           "turn_123",
			IdempotencyKey:   "idem_123",
			RequestHash:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BillingLedgerRef: "legacy-mint-conversation:conv_123",
			ChargedCredits:   1,
			MessageCount:     1,
			AcceptedAt:       now,
		}},
		Failure: &hostedgenesis.Failure{
			Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
			Message:   "Assistant turn did not reach declaration extraction.",
			Retryable: true,
			Recovery:  hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRetrySameStep, MaxAttempts: 3, RetryAfterSeconds: 30},
		},
		RequestID: "req_123",
		CreatedAt: now,
		UpdatedAt: now,
	}

	session := NewHostedGenesisSessionFromSeed(seed)
	require.NoError(t, session.BeforeCreate())
	require.Equal(t, "demo", session.InstanceSlug)
	require.Equal(t, string(hostedgenesis.StatusFailed), session.Status)
	require.Len(t, session.TurnLedger, 1)
}

func validHostedGenesisSessionModel() *HostedGenesisSession {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	return &HostedGenesisSession{
		InstanceSlug:   " Demo ",
		RegistrationID: "reg_123",
		AgentID:        " 0x2222222222222222222222222222222222222222222222222222222222222222 ",
		ConversationID: "conv_123",
		Status:         string(hostedgenesis.StatusInProgress),
		LatestTurnID:   "turn_123",
		MessageCount:   1,
		TurnLedger: []hostedgenesis.TurnLedgerEntry{{
			TurnID:           "turn_123",
			IdempotencyKey:   "idem_123",
			RequestHash:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BillingLedgerRef: "usage:mint:conv_123:turn_123",
			ChargedCredits:   1,
			MessageCount:     1,
			AcceptedAt:       now,
		}},
		InputCheckpointRef: "checkpoint://hosted-genesis/input_123",
		ExecutionStateRef:  "microvm-session:conv_123",
		MicroVMExecutionID: "microvm_123",
		RequestID:          "req_123",
		TraceIDs:           &hostedgenesis.TraceIDs{HostRequestID: "req_123", CorrelationID: "corr_123", IdempotencyKey: "idem_123"},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func validHostedGenesisDeclarationCheckpoint() hostedgenesis.DeclarationCheckpoint {
	return hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   "decl_123",
		DeclarationHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CheckpointRef:   "checkpoint://hosted-genesis/decl_123",
		ProducedAt:      time.Date(2026, 6, 24, 12, 3, 0, 0, time.UTC),
		RegistrationID:  "reg_123",
		ConversationID:  "conv_123",
		AgentID:         "0x2222222222222222222222222222222222222222222222222222222222222222",
		MessageCount:    2,
		Model:           "openai:gpt-5.4",
		RequestID:       "req_123",
	}
}

func assertModelHasNoRawSecretFields(t *testing.T, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "_" || field.PkgPath != "" {
			continue
		}
		name := strings.ToLower(field.Name + " " + field.Tag.Get("json") + " " + field.Tag.Get("theorydb"))
		for _, forbidden := range []string{
			"raw",
			"transcript",
			"messages",
			"prompt",
			"apikey",
			"api_key",
			"secret",
			"credential",
			"signature",
			"ssm",
			"awscredential",
			"aws_credentials",
			"endpoint_token",
		} {
			require.NotContains(t, name, forbidden, "field %s must not be secret-bearing", field.Name)
		}
		assertModelHasNoRawSecretFields(t, field.Type)
	}
}
