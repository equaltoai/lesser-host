package hostedgenesis

import (
	"strings"
	"testing"

	"github.com/theory-cloud/tabletheory/pkg/validation"
)

func TestCheckpointRefCompactsRawIDsIntoTableTheorySafeValue(t *testing.T) {
	conversationID := "k6pDHgCsaBIpVXqxnO--JA"
	turnID := "turn_Bfyb__PXUrAynurbjgIfdg"

	ref := CheckpointRef("input", conversationID, turnID)
	if !strings.HasPrefix(ref, "checkpoint://hosted-genesis/input/") {
		t.Fatalf("expected input checkpoint prefix, got %q", ref)
	}
	if strings.Contains(ref, conversationID) || strings.Contains(ref, turnID) {
		t.Fatalf("checkpoint ref must not embed raw ids, got %q", ref)
	}
	if err := validation.ValidateValue(ref); err != nil {
		t.Fatalf("checkpoint ref must be accepted by TableTheory value validation: %v", err)
	}
}

func TestNormalizeCheckpointRefCompactsLegacyRawIDRefs(t *testing.T) {
	legacy := "checkpoint://hosted-genesis/k6pDHgCsaBIpVXqxnO--JA/input/turn_Bfyb__PXUrAynurbjgIfdg"

	ref := NormalizeCheckpointRef(legacy)
	if !strings.HasPrefix(ref, "checkpoint://hosted-genesis/input/") {
		t.Fatalf("expected input checkpoint prefix, got %q", ref)
	}
	if strings.Contains(ref, "k6pDHgCsaBIpVXqxnO--JA") || strings.Contains(ref, "turn_Bfyb__PXUrAynurbjgIfdg") {
		t.Fatalf("normalized checkpoint ref must not embed raw ids, got %q", ref)
	}
	if err := validation.ValidateValue(ref); err != nil {
		t.Fatalf("normalized checkpoint ref must be accepted by TableTheory value validation: %v", err)
	}
}

func TestNormalizeCheckpointRefPreservesCompactRefs(t *testing.T) {
	ref := "checkpoint://hosted-genesis/assistant/0123456789abcdef"

	if got := NormalizeCheckpointRef(ref); got != ref {
		t.Fatalf("expected compact ref to be preserved, got %q", got)
	}
}
