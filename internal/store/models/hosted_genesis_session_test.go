package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/theory-cloud/tabletheory/v2/pkg/validation"

	"github.com/equaltoai/lesser-host/internal/ai/modelselection"
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

	published := validHostedGenesisSessionModel()
	published.Status = string(hostedgenesis.StatusPublished)
	checkpoint := validHostedGenesisDeclarationCheckpoint()
	published.DeclarationCheckpoint = &checkpoint
	require.ErrorIs(t, published.BeforeCreate(), hostedgenesis.ErrInvalidPublicationCheckpoint)
}

func TestHostedGenesisSessionAcceptsBoundPublishedState(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	session.Status = string(hostedgenesis.StatusPublished)
	checkpoint := validHostedGenesisDeclarationCheckpoint()
	session.DeclarationCheckpoint = &checkpoint
	session.Publication = &hostedgenesis.PublicationCheckpoint{
		RegistrationID:       session.RegistrationID,
		ConversationID:       session.ConversationID,
		AgentID:              strings.TrimSpace(session.AgentID),
		Version:              1,
		RegistrationSHA256:   strings.Repeat("b", 64),
		RegistrationIssuedAt: checkpoint.ProducedAt,
		PublishedAt:          checkpoint.ProducedAt.Add(time.Minute),
	}
	require.NoError(t, session.BeforeCreate())

	projection, err := hostedgenesis.NewConversationProjection(session.ToProjectionInput(), true)
	require.NoError(t, err)
	require.Equal(t, hostedgenesis.StatusPublished, projection.Status)
	require.Equal(t, 1, projection.PublishedVersion)
	require.Equal(t, session.Publication.PublishedAt, projection.PublishedAt)
}

func TestHostedGenesisSessionRejectsInvalidTurnLedger(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	session.TurnLedger = append(session.TurnLedger, session.TurnLedger[0])
	require.ErrorIs(t, session.BeforeCreate(), hostedgenesis.ErrDuplicateTurnID)
}

func TestHostedGenesisSessionRejectsCandidateFromAnotherTurn(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	session.Model = "openai:gpt-5.4"
	candidate, err := hostedgenesis.NewDeclarationCandidate(hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug: session.InstanceSlug, RegistrationID: session.RegistrationID, AgentID: session.AgentID,
		ConversationID: session.ConversationID, SourceTurnID: "turn_other", Model: session.Model,
	}, session.CreatedAt)
	require.NoError(t, err)
	session.DeclarationCandidate = candidate
	require.ErrorContains(t, session.BeforeCreate(), "declaration candidate binding does not match hosted genesis session")
}

func TestHostedGenesisSessionAcceptsAliasBoundCanonicalCandidate(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	session.Model = modelselection.AliasOpenAI
	candidate, err := hostedgenesis.NewDeclarationCandidate(hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug: session.InstanceSlug, RegistrationID: session.RegistrationID, AgentID: session.AgentID,
		ConversationID: session.ConversationID, SourceTurnID: session.LatestTurnID,
		Model: modelselection.CanonicalModelSet(session.Model),
	}, session.CreatedAt)
	require.NoError(t, err)
	session.DeclarationCandidate = candidate
	require.NoError(t, session.BeforeCreate())
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

func TestHostedGenesisSessionNormalizesUnsafeLegacyCheckpointRefs(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	session.ConversationID = "k6pDHgCsaBIpVXqxnO--JA"
	session.LatestTurnID = "turn_Bfyb__PXUrAynurbjgIfdg"
	session.InputCheckpointRef = "checkpoint://hosted-genesis/k6pDHgCsaBIpVXqxnO--JA/input/turn_Bfyb__PXUrAynurbjgIfdg"
	session.AssistantCheckpointRef = "checkpoint://hosted-genesis/k6pDHgCsaBIpVXqxnO--JA/assistant/turn_Bfyb__PXUrAynurbjgIfdg"
	session.TurnLedger[0].TurnID = "turn_Bfyb__PXUrAynurbjgIfdg"
	session.TurnLedger[0].InputCheckpointRef = session.InputCheckpointRef
	session.TurnLedger[0].AssistantCheckpointRef = session.AssistantCheckpointRef
	checkpoint := validHostedGenesisDeclarationCheckpoint()
	checkpoint.CheckpointRef = "checkpoint://hosted-genesis/k6pDHgCsaBIpVXqxnO--JA/declaration/turn_Bfyb__PXUrAynurbjgIfdg"
	session.DeclarationCheckpoint = &checkpoint

	require.NoError(t, session.BeforeUpdate())
	require.NotContains(t, session.InputCheckpointRef, "k6pDHgCsaBIpVXqxnO--JA")
	require.NotContains(t, session.AssistantCheckpointRef, "k6pDHgCsaBIpVXqxnO--JA")
	require.NotContains(t, session.TurnLedger[0].InputCheckpointRef, "k6pDHgCsaBIpVXqxnO--JA")
	require.NotContains(t, session.TurnLedger[0].AssistantCheckpointRef, "k6pDHgCsaBIpVXqxnO--JA")
	require.NotContains(t, session.DeclarationCheckpoint.CheckpointRef, "k6pDHgCsaBIpVXqxnO--JA")
	require.NoError(t, validation.ValidateValue(session.InputCheckpointRef))
	require.NoError(t, validation.ValidateValue(session.AssistantCheckpointRef))
	require.NoError(t, validation.ValidateValue(session.DeclarationCheckpoint.CheckpointRef))
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
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
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

func TestHostedGenesisSessionMicroVMRefIsExecutionCacheOnly(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	binding := session.MicroVMSessionBinding()
	now := time.Date(2026, 6, 24, 18, 15, 0, 0, time.UTC)
	ref := hostedgenesis.MicroVMLifecycleRef{
		SourceOfTruth:   hostedgenesis.MicroVMSourceOfTruth,
		TenantID:        binding.TenantID(),
		Namespace:       hostedgenesis.MicroVMNamespace,
		SessionID:       session.ConversationID,
		LifecycleState:  "starting",
		DesiredState:    "started",
		MicroVMID:       "microvm_123",
		LastAction:      "start",
		LastTransition:  now,
		RegistryVersion: 2,
		UpdatedAt:       now,
	}
	require.NoError(t, session.ApplyMicroVMLifecycleRef(ref))
	require.NoError(t, session.BeforeCreate())
	require.NotNil(t, session.MicroVMLifecycleRef)
	require.Contains(t, session.ExecutionStateRef, "microvm://host-dynamodb-hosted-genesis-session/slug:demo/conv_123")
	require.Equal(t, "microvm_123", session.MicroVMExecutionID)

	modelPayload, err := json.Marshal(session)
	require.NoError(t, err)
	modelText := strings.ToLower(string(modelPayload))
	require.NotContains(t, modelText, "microvm_lifecycle_ref")
	require.NotContains(t, modelText, "network_connector")

	projection, err := hostedgenesis.NewConversationProjection(session.ToProjectionInput(), true)
	require.NoError(t, err)
	require.Equal(t, hostedgenesis.StatusInProgress, projection.Status)
	payload, err := json.Marshal(projection)
	require.NoError(t, err)
	jsonText := strings.ToLower(string(payload))
	require.NotContains(t, jsonText, "microvm")
	require.NotContains(t, jsonText, "endpoint")
	require.NotContains(t, jsonText, "token")
}

func TestHostedGenesisSessionMicroVMRefRejectsCrossTenantRegistryState(t *testing.T) {
	t.Parallel()

	session := validHostedGenesisSessionModel()
	binding := session.MicroVMSessionBinding()
	now := time.Date(2026, 6, 24, 18, 15, 0, 0, time.UTC)
	ref := hostedgenesis.MicroVMLifecycleRef{
		SourceOfTruth:   hostedgenesis.MicroVMSourceOfTruth,
		TenantID:        "slug:other",
		Namespace:       hostedgenesis.MicroVMNamespace,
		SessionID:       session.ConversationID,
		LifecycleState:  "started",
		DesiredState:    "started",
		LastAction:      "status",
		LastTransition:  now,
		RegistryVersion: 1,
		UpdatedAt:       now,
	}
	require.ErrorIs(t, ref.Validate(binding), hostedgenesis.ErrInvalidMicroVMLifecycleRef)
	require.ErrorIs(t, session.ApplyMicroVMLifecycleRef(ref), hostedgenesis.ErrInvalidMicroVMLifecycleRef)

	// Host truth still reconstructs the safe AppTheory binding without registry/cache state.
	rebuilt := session.MicroVMSessionBinding()
	require.NoError(t, rebuilt.Validate())
	require.Equal(t, "slug:demo", rebuilt.TenantID())
	require.Equal(t, "conv_123", rebuilt.ConversationID)
}
