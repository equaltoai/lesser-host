package hostedgenesis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewVMCheckpointMetadataBuildsSafeDeterministicEnvelope(t *testing.T) {
	input := VMCheckpointInput{
		ConversationID:     "conv-1",
		LatestTurnID:       "turn-1",
		RequestID:          "req-1",
		Sequence:           3,
		Step:               "Assistant Turn",
		Action:             "Ask",
		StatusFrom:         StatusInProgress,
		StatusTo:           StatusAssistantTurnReady,
		Runtime:            "hosted-genesis-microvm-workload/v1",
		ProviderFamily:     "OpenAI",
		ModelID:            "gpt-test",
		ProviderSessionID:  "provider-session-1",
		TraceID:            "trace-1",
		AdditionalHashSalt: "assistant-checkpoint",
	}

	got, err := NewVMCheckpointMetadata(input)
	require.NoError(t, err)
	require.NoError(t, got.Validate())
	require.Equal(t, int64(3), got.Sequence)
	require.Equal(t, "assistant_turn", got.Step)
	require.Equal(t, "ask", got.Action)
	require.Equal(t, string(StatusInProgress), got.StatusFrom)
	require.Equal(t, string(StatusAssistantTurnReady), got.StatusTo)
	require.Equal(t, "openai", got.ProviderFamily)
	require.Equal(t, "gpt-test", got.ModelID)
	require.Equal(t, "provider-session-1", got.ProviderSessionID)
	require.Equal(t, "trace-1", got.TraceID)
	require.Equal(t, "turn-1", got.LatestTurnID)
	require.Equal(t, "req-1", got.RequestID)
	require.True(t, strings.HasPrefix(got.Ref, "checkpoint://hosted-genesis/vm-actor/"), got.Ref)
	require.True(t, isSHA256Digest(got.Hash), got.Hash)

	again, err := NewVMCheckpointMetadata(input)
	require.NoError(t, err)
	require.Equal(t, got.Ref, again.Ref)
	require.Equal(t, got.Hash, again.Hash)
	input.AdditionalHashSalt = "declaration-checkpoint"
	changed, err := NewVMCheckpointMetadata(input)
	require.NoError(t, err)
	require.Equal(t, got.Ref, changed.Ref)
	require.NotEqual(t, got.Hash, changed.Hash)
}

func TestNewVMCheckpointMetadataDefaultsSequenceAndRefKind(t *testing.T) {
	got, err := NewVMCheckpointMetadata(VMCheckpointInput{
		ConversationID: "conv-1",
		LatestTurnID:   "turn-1",
		Step:           "Wait",
		Action:         "Wait",
		StatusFrom:     StatusAssistantTurnReady,
		StatusTo:       StatusAssistantTurnReady,
		Runtime:        "runtime-1",
	})
	if err != nil {
		t.Fatalf("unexpected checkpoint error: %v", err)
	}
	if got.Sequence != 1 {
		t.Fatalf("expected default sequence 1, got %#v", got)
	}
	if !strings.Contains(got.Ref, "/vm-actor/") {
		t.Fatalf("expected vm-actor checkpoint ref, got %q", got.Ref)
	}
}

func TestVMCheckpointMetadataValidationRejectsUnsafeOrIncompleteFields(t *testing.T) {
	base := VMCheckpointMetadata{
		Sequence:     1,
		Ref:          CheckpointRef("vm-actor", "conv-1", "turn-1"),
		Hash:         "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Step:         "assistant_turn",
		Action:       "ask",
		StatusFrom:   string(StatusInProgress),
		StatusTo:     string(StatusAssistantTurnReady),
		Runtime:      "hosted-genesis-microvm-workload/v1",
		LatestTurnID: "turn-1",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("base checkpoint should validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(VMCheckpointMetadata) VMCheckpointMetadata
	}{
		{"missing sequence", func(m VMCheckpointMetadata) VMCheckpointMetadata { m.Sequence = 0; return m }},
		{"invalid hash", func(m VMCheckpointMetadata) VMCheckpointMetadata { m.Hash = "sha256:not-hex"; return m }},
		{"missing runtime", func(m VMCheckpointMetadata) VMCheckpointMetadata { m.Runtime = ""; return m }},
		{"missing turn", func(m VMCheckpointMetadata) VMCheckpointMetadata { m.LatestTurnID = ""; return m }},
		{"invalid status", func(m VMCheckpointMetadata) VMCheckpointMetadata { m.StatusTo = "unknown"; return m }},
		{"unsafe provider id", func(m VMCheckpointMetadata) VMCheckpointMetadata {
			m.ProviderSessionID = "bearer raw-provider-token"
			return m
		}},
		{"unsafe trace id", func(m VMCheckpointMetadata) VMCheckpointMetadata { m.TraceID = "x-aws-proxy-auth=secret"; return m }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(base).Validate(); err == nil {
				t.Fatal("expected invalid VM checkpoint to be rejected")
			}
		})
	}
}

func TestContainsUnsafeCheckpointMaterial(t *testing.T) {
	if !containsUnsafeCheckpointMaterial("Authorization: bearer abc") {
		t.Fatal("expected authorization material to be rejected")
	}
	if containsUnsafeCheckpointMaterial("trace-1", "provider-session-1", "gpt-test") {
		t.Fatal("expected safe ids to be allowed")
	}
}
