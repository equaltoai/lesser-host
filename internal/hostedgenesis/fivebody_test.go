package hostedgenesis

import (
	"strings"
	"testing"
	"time"
)

func TestDeclarationContractFromEnvDefaultsToLegacyAndOptInV2(t *testing.T) {
	t.Setenv(EnvDeclarationSchemaVersion, "")
	t.Setenv(EnvGuidanceVersion, "")
	if got := DeclarationContractFromEnv(); got != LegacyDeclarationContract() || got.IsFiveBody() {
		t.Fatalf("expected legacy default, got %#v", got)
	}

	t.Setenv(EnvDeclarationSchemaVersion, "v2")
	got := DeclarationContractFromEnv()
	if !got.IsFiveBody() || got.SchemaVersion != DeclarationSchemaVersionV2 || got.GuidanceVersion != GuidanceVersionV2 {
		t.Fatalf("expected v2 opt-in, got %#v", got)
	}
}

func TestFiveBodyValidationStableFieldCodes(t *testing.T) {
	body := validFiveBodyDeclaration()
	if err := ValidateFiveBodyDeclaration(body); err != nil {
		t.Fatalf("valid five body: %v", err)
	}

	missingIdentity := body
	missingIdentity.Identity.Summary = "  "
	if got := DeclarationValidationCodeFromError(ValidateFiveBodyDeclaration(missingIdentity)); got != DeclarationCodeFiveBodyIdentity {
		t.Fatalf("expected identity field code, got %q", got)
	}

	missingRefusals := body
	missingRefusals.Soul.Refusals = missingRefusals.Soul.Refusals[:2]
	if got := DeclarationValidationCodeFromError(ValidateFiveBodyDeclaration(missingRefusals)); got != DeclarationCodeSoulRefusals {
		t.Fatalf("expected refusal floor field code, got %q", got)
	}

	genericRefusal := body
	genericRefusal.Soul.Refusals[0].Bypass = "unsafe requests"
	if got := DeclarationValidationCodeFromError(ValidateFiveBodyDeclaration(genericRefusal)); got != DeclarationCodeSoulRefusalsBad {
		t.Fatalf("expected generic refusal field code, got %q", got)
	}
}

func TestValidateDeclarationContractVersionsRequiresExactV2Evidence(t *testing.T) {
	contract := FiveBodyDeclarationContract()
	if err := ValidateDeclarationContractVersions(contract.SchemaVersion, contract.GuidanceVersion, contract); err != nil {
		t.Fatalf("expected exact v2 versions to pass: %v", err)
	}
	if got := DeclarationValidationCodeFromError(ValidateDeclarationContractVersions("", contract.GuidanceVersion, contract)); got != DeclarationCodeInvalid {
		t.Fatalf("expected missing schema version to fail closed, got %q", got)
	}
	if got := DeclarationValidationCodeFromError(ValidateDeclarationContractVersions(contract.SchemaVersion, GuidanceVersionV1, contract)); got != DeclarationCodeInvalid {
		t.Fatalf("expected mismatched guidance version to fail closed, got %q", got)
	}
	if err := ValidateDeclarationContractVersions("", "", LegacyDeclarationContract()); err != nil {
		t.Fatalf("legacy lane must remain no-version compatible: %v", err)
	}
}

func TestFiveBodyBoundariesAndAdversarialReviewShape(t *testing.T) {
	body := validFiveBodyDeclaration()
	bounds := FiveBodyBoundaries(body.Soul.Refusals, time.Unix(1234, 0).UTC())
	if len(bounds) != 3 {
		t.Fatalf("expected three mapped boundaries, got %#v", bounds)
	}
	for _, b := range bounds {
		if b.Category != "refusal" || !strings.Contains(b.Statement, "closest safe path") || !strings.Contains(b.Rationale, "Five-body soul refusal") {
			t.Fatalf("unexpected mapped boundary: %#v", b)
		}
	}

	review, err := BuildAdversarialReviewV2(body)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if review.Version != AdversarialReviewVersionV1 || review.Reviewer != AdversarialReviewerV1 || review.Result != "pass" || len(review.Findings) < 3 {
		t.Fatalf("unexpected review shape: %#v", review)
	}
	for _, finding := range review.Findings {
		if finding.Finding == "" || finding.Refutation == "" || finding.Report == "" {
			t.Fatalf("review finding must preserve find/refute/report shape: %#v", finding)
		}
	}
}

func TestDeclarationCheckpointAllowsLegacyAndPinsV2Versions(t *testing.T) {
	checkpoint := DeclarationCheckpoint{
		DeclarationID:   "decl_1234567890abcdef",
		DeclarationHash: "sha256:" + strings.Repeat("a", 64),
		CheckpointRef:   "checkpoint://hosted-genesis/declaration/1234567890abcdef",
		ProducedAt:      time.Unix(1234, 0).UTC(),
		RegistrationID:  "reg-1",
		ConversationID:  "conv-1",
		AgentID:         "agent-1",
		MessageCount:    2,
		RequestID:       "req-1",
	}
	if err := checkpoint.Validate(); err != nil {
		t.Fatalf("legacy checkpoint should remain valid: %v", err)
	}
	checkpoint.SchemaVersion = DeclarationSchemaVersionV2
	checkpoint.GuidanceVersion = GuidanceVersionV2
	if err := checkpoint.Validate(); err != nil {
		t.Fatalf("v2 checkpoint should be valid: %v", err)
	}
	checkpoint.GuidanceVersion = GuidanceVersionV1
	if err := checkpoint.Validate(); err == nil {
		t.Fatalf("mismatched schema/guidance versions must fail closed")
	}
}

func validFiveBodyDeclaration() FiveBodyDeclaration {
	return FiveBodyDeclaration{
		Identity:   FiveBodySection{Summary: "I am Acme Steward, a hosted/off-chain operations assistant for Acme."},
		Philosophy: FiveBodySection{Summary: "I prefer transparent trade-offs, bounded authority, and careful escalation."},
		Discipline: FiveBodySection{Summary: "I follow the named cadence, collect evidence, and pause when authority is unclear."},
		Boundaries: FiveBodySection{Summary: "I keep tenant data isolated, reject credential handling, and refuse unsafe shortcuts."},
		Soul: FiveBodySoulBody{
			Summary: "I protect the managed instance by preserving safety gates even under schedule pressure.",
			Refusals: []FiveBodyRefusalRule{
				{Bypass: "Skip checksum verification for a release", Invariant: "consumer release verification must fail closed", ClosestSafePath: "run certification against the exact published artifact before rollout"},
				{Bypass: "Log a raw Instance API key for debugging", Invariant: "raw credentials never enter Host logs", ClosestSafePath: "compare the sha256 key hash and log only a redacted correlation id"},
				{Bypass: "Publish without the human gate", Invariant: "hosted genesis requires explicit human publication", ClosestSafePath: "return declaration_ready and wait for finalize authorization"},
			},
		},
	}
}
