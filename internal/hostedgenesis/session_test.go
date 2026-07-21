package hostedgenesis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStatusTransitionTable(t *testing.T) {
	t.Parallel()

	legal := []struct {
		from Status
		to   Status
	}{
		{StatusCreated, StatusInProgress},
		{StatusInProgress, StatusAssistantTurnReady},
		{StatusInProgress, StatusDeclarationExtractionPending},
		{StatusInProgress, StatusDeclarationReady},
		{StatusAssistantTurnReady, StatusInProgress},
		{StatusAssistantTurnReady, StatusDeclarationExtractionPending},
		{StatusDeclarationExtractionPending, StatusDeclarationReady},
		{StatusDeclarationReady, StatusPublished},
		{StatusCreated, StatusFailed},
		{StatusInProgress, StatusFailed},
		{StatusAssistantTurnReady, StatusFailed},
		{StatusDeclarationExtractionPending, StatusFailed},
	}
	for _, tt := range legal {
		t.Run(string(tt.from)+"_to_"+string(tt.to), func(t *testing.T) {
			t.Parallel()
			require.NoError(t, ValidateTransition(tt.from, tt.to))
		})
	}

	illegal := []struct {
		from Status
		to   Status
	}{
		{StatusCreated, StatusDeclarationReady},
		{StatusDeclarationExtractionPending, StatusAssistantTurnReady},
		{StatusDeclarationReady, StatusInProgress},
		{StatusDeclarationReady, StatusFailed},
		{StatusPublished, StatusDeclarationReady},
		{StatusFailed, StatusInProgress},
	}
	for _, tt := range illegal {
		t.Run("reject_"+string(tt.from)+"_to_"+string(tt.to), func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, ValidateTransition(tt.from, tt.to), ErrInvalidStatusTransition)
		})
	}
}

func TestPublishedProjectionRequiresBoundPublicationCheckpoint(t *testing.T) {
	t.Parallel()

	declaration := validDeclarationCheckpoint()
	publishedAt := declaration.ProducedAt.Add(time.Minute)
	publication := PublicationCheckpoint{
		RegistrationID:       declaration.RegistrationID,
		ConversationID:       declaration.ConversationID,
		AgentID:              declaration.AgentID,
		Version:              1,
		RegistrationSHA256:   strings.Repeat("b", 64),
		RegistrationIssuedAt: declaration.ProducedAt,
		PublishedAt:          publishedAt,
	}
	projection, err := NewConversationProjection(ProjectionInput{
		RegistrationID:        declaration.RegistrationID,
		ConversationID:        declaration.ConversationID,
		AgentID:               declaration.AgentID,
		Status:                StatusPublished,
		MessageCount:          declaration.MessageCount,
		DeclarationCheckpoint: &declaration,
		Publication:           &publication,
		CompletedAt:           declaration.ProducedAt,
	}, true)
	require.NoError(t, err)
	require.Equal(t, StatusPublished, projection.Status)
	require.Equal(t, 1, projection.PublishedVersion)
	require.Equal(t, publishedAt, projection.PublishedAt)
	require.Nil(t, projection.DeclarationCheckpoint)
	require.Nil(t, projection.Failure)
	require.Zero(t, projection.PollAfterSeconds)

	publication.ConversationID = "conv_other"
	_, err = NewConversationProjection(ProjectionInput{
		RegistrationID:        declaration.RegistrationID,
		ConversationID:        declaration.ConversationID,
		AgentID:               declaration.AgentID,
		Status:                StatusPublished,
		DeclarationCheckpoint: &declaration,
		Publication:           &publication,
	}, true)
	require.ErrorIs(t, err, ErrInvalidPublicationCheckpoint)
}

func TestCreatedCollapsesForInstanceKeyRead(t *testing.T) {
	t.Parallel()

	require.Equal(t, StatusInProgress, InstanceKeyReadStatus(StatusCreated))
	require.Equal(t, StatusAssistantTurnReady, InstanceKeyReadStatus(StatusAssistantTurnReady))
}

func TestFinalizeRequiresDeclarationReadyCheckpoint(t *testing.T) {
	t.Parallel()

	checkpoint := validDeclarationCheckpoint()
	require.NoError(t, CanFinalize(StatusDeclarationReady, &checkpoint))
	require.ErrorIs(t, CanFinalize(StatusInProgress, &checkpoint), ErrInvalidDeclarationGate)
	require.ErrorIs(t, CanFinalize(StatusDeclarationReady, nil), ErrInvalidDeclarationGate)

	checkpoint.DeclarationHash = "sha256:not-a-real-digest"
	require.ErrorIs(t, CanFinalize(StatusDeclarationReady, &checkpoint), ErrInvalidDeclarationGate)
}

func TestPublishGateBindsDeclarationCheckpointToSession(t *testing.T) {
	t.Parallel()

	checkpoint := validDeclarationCheckpoint()
	require.NoError(t, CanPublish(PublishGateInput{
		Status:                StatusDeclarationReady,
		RegistrationID:        checkpoint.RegistrationID,
		ConversationID:        checkpoint.ConversationID,
		AgentID:               checkpoint.AgentID,
		DeclarationCheckpoint: &checkpoint,
	}))

	require.ErrorIs(t, CanPublish(PublishGateInput{
		Status:                StatusInProgress,
		RegistrationID:        checkpoint.RegistrationID,
		ConversationID:        checkpoint.ConversationID,
		AgentID:               checkpoint.AgentID,
		DeclarationCheckpoint: &checkpoint,
	}), ErrInvalidPublishGate)

	require.ErrorIs(t, CanPublish(PublishGateInput{
		Status:                StatusDeclarationReady,
		RegistrationID:        checkpoint.RegistrationID,
		ConversationID:        "conv_other",
		AgentID:               checkpoint.AgentID,
		DeclarationCheckpoint: &checkpoint,
	}), ErrInvalidPublishGate)
}

func TestFailedRecoveryActionsAreServerAuthoredAndBounded(t *testing.T) {
	t.Parallel()

	failure := Failure{
		Code:      FailureCodeLLMUnavailable,
		Class:     FailureClassProviderAPIFailure,
		Message:   "assistant turn failed before declaration extraction",
		Retryable: true,
		Recovery: Recovery{
			Action:            RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 30,
			Reason:            "provider_unavailable",
		},
	}
	require.NoError(t, failure.Validate())
	require.Equal(t, FailureClassProviderTimeout, NormalizeFailureClass(" provider_timeout "))
	require.Equal(t, FailureClassProviderAPIFailure, NormalizeFailureClass("private arbitrary detail"))

	failure.Recovery.Action = RecoveryAction("caller_supplied_shell_command")
	require.ErrorIs(t, failure.Validate(), ErrInvalidFailureRecovery)
	failure.Recovery.Action = RecoveryActionRetrySameStep
	failure.Class = FailureClass("private_detail")
	require.ErrorIs(t, failure.Validate(), ErrInvalidFailureRecovery)
}

func TestCompactProjectionHasNoRawTranscriptFields(t *testing.T) {
	t.Parallel()

	checkpoint := validDeclarationCheckpoint()
	projection, err := NewConversationProjection(ProjectionInput{
		RegistrationID:        " reg_123 ",
		ConversationID:        " conv_123 ",
		AgentID:               " 0x2222222222222222222222222222222222222222222222222222222222222222 ",
		Status:                StatusDeclarationReady,
		LatestTurnID:          " turn_123 ",
		MessageCount:          2,
		DeclarationCheckpoint: &checkpoint,
		RequestID:             " req_123 ",
		TraceIDs: &TraceIDs{
			HostRequestID:  "req_123",
			CorrelationID:  "correlation_123",
			IdempotencyKey: "idem_123",
		},
		CreatedAt: checkpoint.ProducedAt.Add(-time.Minute),
		UpdatedAt: checkpoint.ProducedAt,
	}, true)
	require.NoError(t, err)
	require.Equal(t, StatusDeclarationReady, projection.Status)

	payload, err := json.Marshal(projection)
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

	assertNoRawSecretFields(t, reflect.TypeOf(ConversationProjection{}))
}

func validDeclarationCheckpoint() DeclarationCheckpoint {
	return DeclarationCheckpoint{
		DeclarationID:   "decl_123",
		DeclarationHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CheckpointRef:   "checkpoint://hosted-genesis/decl_123",
		ProducedAt:      time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
		RegistrationID:  "reg_123",
		ConversationID:  "conv_123",
		AgentID:         "0x2222222222222222222222222222222222222222222222222222222222222222",
		MessageCount:    2,
		Model:           "openai:gpt-5.4",
		SchemaVersion:   DeclarationSchemaVersionV2,
		GuidanceVersion: GuidanceVersionV2,
		RequestID:       "req_123",
	}
}

func assertNoRawSecretFields(t *testing.T, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
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
		assertNoRawSecretFields(t, field.Type)
	}
}
