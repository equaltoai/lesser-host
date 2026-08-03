package models

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/theory-cloud/tabletheory/v3/pkg/marshal"
	theorymodel "github.com/theory-cloud/tabletheory/v3/pkg/model"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/soul"
)

type nestedPersistenceProbe[T any] struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK     string `theorydb:"pk,attr:PK"`
	SK     string `theorydb:"sk,attr:SK"`
	Nested T      `theorydb:"attr:nested"`
}

func (nestedPersistenceProbe[T]) TableName() string { return MainTableName() }

type nestedFieldSpec struct {
	name string
	attr string
}

func TestTableTheoryV3PreservesNestedZeroValueShapes(t *testing.T) {
	t.Run("link safety summary", func(t *testing.T) {
		item := marshalTableTheoryV3Model(t, &LinkSafetyBasicResult{
			PK: "LINK_SAFETY_BASIC#test",
			SK: "RESULT",
		})
		summary := requireAttributeMap(t, item["summary"])
		if len(summary) != 0 {
			t.Fatalf("zero summary must retain the v2 empty-map shape, got %#v", summary)
		}

		populated := marshalTableTheoryV3Model(t, &LinkSafetyBasicResult{
			PK:      "LINK_SAFETY_BASIC#test",
			SK:      "RESULT",
			Summary: LinkSafetyBasicSummary{TotalLinks: 1, OverallRisk: "low"},
		})
		summary = requireAttributeMap(t, populated["summary"])
		if len(summary) != 2 {
			t.Fatalf("non-zero summary fields must remain persisted, got %#v", summary)
		}
	})

	t.Run("hosted genesis refusal", func(t *testing.T) {
		item := marshalTableTheoryV3Model(t, &HostedGenesisSession{
			PK: "HOSTED_GENESIS#INSTANCE#test",
			SK: "SESSION#test",
			DeclarationCandidate: &hostedgenesis.DeclarationCandidate{
				FiveBodies: hostedgenesis.FiveBodyDeclaration{
					Soul: hostedgenesis.FiveBodySoulBody{
						Refusals: []hostedgenesis.FiveBodyRefusalRule{{}},
					},
				},
			},
		})
		candidate := requireAttributeMap(t, item["declarationCandidate"])
		bodies := requireAttributeMap(t, candidate["five_bodies"])
		soul := requireAttributeMap(t, bodies["soul"])
		refusals, ok := soul["refusals"].(*types.AttributeValueMemberL)
		if !ok || len(refusals.Value) != 1 {
			t.Fatalf("expected one refusal map, got %#v", soul["refusals"])
		}
		refusal := requireAttributeMap(t, refusals.Value[0])
		if len(refusal) != 0 {
			t.Fatalf("zero refusal must retain the v2 empty-map shape, got %#v", refusal)
		}

		item = marshalTableTheoryV3Model(t, &HostedGenesisSession{
			PK: "HOSTED_GENESIS#INSTANCE#test",
			SK: "SESSION#test",
			DeclarationCandidate: &hostedgenesis.DeclarationCandidate{
				FiveBodies: hostedgenesis.FiveBodyDeclaration{
					Soul: hostedgenesis.FiveBodySoulBody{
						Refusals: []hostedgenesis.FiveBodyRefusalRule{{
							Bypass:          "bypass",
							Invariant:       "invariant",
							ClosestSafePath: "safe path",
						}},
					},
				},
			},
		})
		candidate = requireAttributeMap(t, item["declarationCandidate"])
		bodies = requireAttributeMap(t, candidate["five_bodies"])
		soul = requireAttributeMap(t, bodies["soul"])
		refusals, ok = soul["refusals"].(*types.AttributeValueMemberL)
		if !ok || len(refusals.Value) != 1 {
			t.Fatalf("expected one populated refusal map, got %#v", soul["refusals"])
		}
		refusal = requireAttributeMap(t, refusals.Value[0])
		if len(refusal) != 3 {
			t.Fatalf("non-zero refusal fields must remain persisted, got %#v", refusal)
		}
	})
}

func TestTableTheoryV3NestedStructAdaptations(t *testing.T) {
	t.Run("declaration candidate", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.DeclarationCandidate](t, []nestedFieldSpec{
			{"Version", "version"}, {"InstanceSlug", "instance_slug"}, {"RegistrationID", "registration_id"},
			{"AgentID", "agent_id"}, {"ConversationID", "conversation_id"}, {"SourceTurnID", "source_turn_id"},
			{"SchemaVersion", "schema_version"}, {"GuidanceVersion", "guidance_version"}, {"Model", "model"},
			{"Phase", "phase"}, {"FiveBodies", "five_bodies"}, {"SelfDescription", "self_description"},
			{"Capabilities", "capabilities"}, {"Transparency", "transparency"}, {"Revision", "revision"},
			{"CandidateHash", "candidate_hash"}, {"CanonicalJSON", "canonical_json"},
			{"EstablishedAt", "established_at"}, {"UpdatedAt", "updated_at"},
		})
	})
	t.Run("declaration provider attempt", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.DeclarationProviderAttempt](t, []nestedFieldSpec{
			{"Sequence", "sequence"}, {"Provider", "provider"}, {"Model", "model"}, {"Phase", "phase"},
			{"Section", "section"}, {"SourceTurnID", "source_turn_id"},
			{"CandidateRevision", "candidate_revision"}, {"CandidateHash", "candidate_hash"},
			{"SDKAttemptOrdinal", "sdk_attempt_ordinal"}, {"SDKRetryBudget", "sdk_retry_budget"},
			{"ObservedAt", "observed_at"},
		})
	})
	t.Run("declaration review checkpoint", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.DeclarationReviewCheckpoint](t, []nestedFieldSpec{
			{"RendererVersion", "renderer_version"}, {"SchemaVersion", "schema_version"},
			{"GuidanceVersion", "guidance_version"}, {"SourceTurnID", "source_turn_id"},
			{"CandidateHash", "candidate_hash"}, {"ReviewHash", "review_hash"},
			{"CandidateRevision", "candidate_revision"}, {"ReviewText", "review_text"},
			{"ReviewedAt", "reviewed_at"},
		})
	})
	t.Run("declaration tool record", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.DeclarationToolRecord](t, []nestedFieldSpec{
			{"ToolCallHash", "tool_call_hash"}, {"InputHash", "input_hash"}, {"ToolName", "tool_name"},
			{"Section", "section"}, {"SourceTurnID", "source_turn_id"}, {"Revision", "revision"},
			{"SectionHash", "section_hash"}, {"CandidateHash", "candidate_hash"},
		})
	})
	t.Run("declaration affirmation checkpoint", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.DeclarationAffirmationCheckpoint](t, []nestedFieldSpec{
			{"CandidateRevision", "candidate_revision"}, {"CandidateHash", "candidate_hash"},
			{"ReviewHash", "review_hash"}, {"SourceTurnID", "source_turn_id"}, {"AffirmedAt", "affirmed_at"},
		})
	})
	t.Run("declaration transparency", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.DeclarationTransparency](t, []nestedFieldSpec{
			{"ModelProviderUncertainty", "modelProviderUncertainty"}, {"OperationalNotes", "operationalNotes"},
			{"SelfDeclaredNotice", "selfDeclaredNotice"},
		})
	})
	t.Run("vm checkpoint metadata", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.VMCheckpointMetadata](t, []nestedFieldSpec{
			{"Sequence", "sequence"}, {"Ref", "ref"}, {"Hash", "hash"}, {"Step", "step"},
			{"Action", "action"}, {"StatusFrom", "status_from"}, {"StatusTo", "status_to"},
			{"Runtime", "runtime"}, {"LatestTurnID", "latest_turn_id"},
		})
	})
	t.Run("declaration checkpoint", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.DeclarationCheckpoint](t, []nestedFieldSpec{
			{"DeclarationID", "declaration_id"}, {"DeclarationHash", "declaration_hash"},
			{"CheckpointRef", "checkpoint_ref"}, {"ProducedAt", "produced_at"},
			{"RegistrationID", "registration_id"}, {"ConversationID", "conversation_id"},
			{"AgentID", "agent_id"}, {"MessageCount", "message_count"}, {"RequestID", "request_id"},
		})
	})
	t.Run("publication checkpoint", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.PublicationCheckpoint](t, []nestedFieldSpec{
			{"RegistrationID", "registration_id"}, {"ConversationID", "conversation_id"},
			{"AgentID", "agent_id"}, {"Version", "version"},
			{"RegistrationSHA256", "registration_sha256"}, {"RegistrationIssuedAt", "registration_issued_at"},
		})
	})
	t.Run("failure", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.Failure](t, []nestedFieldSpec{
			{"Code", "code"}, {"Message", "message"}, {"Retryable", "retryable"}, {"Recovery", "recovery"},
		})
	})
	t.Run("recovery", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.Recovery](t, []nestedFieldSpec{{"Action", "action"}})
	})
	t.Run("microvm lifecycle ref", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.MicroVMLifecycleRef](t, []nestedFieldSpec{
			{"SourceOfTruth", "source_of_truth"}, {"TenantID", "tenant_id"}, {"Namespace", "namespace"},
			{"SessionID", "session_id"}, {"LifecycleState", "lifecycle_state"},
			{"LastAction", "last_action"}, {"UpdatedAt", "updated_at"},
		})
	})
	t.Run("five body declaration", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.FiveBodyDeclaration](t, []nestedFieldSpec{
			{"Identity", "identity"}, {"Philosophy", "philosophy"}, {"Discipline", "discipline"},
			{"Boundaries", "boundaries"}, {"Soul", "soul"},
		})
	})
	t.Run("five body soul", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.FiveBodySoulBody](t, []nestedFieldSpec{
			{"Summary", "summary"}, {"Refusals", "refusals"},
		})
	})
	t.Run("five body section", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.FiveBodySection](t, []nestedFieldSpec{{"Summary", "summary"}})
	})
	t.Run("turn ledger entry", func(t *testing.T) {
		assertNestedZeroShape[hostedgenesis.TurnLedgerEntry](t, []nestedFieldSpec{
			{"TurnID", "turn_id"}, {"MessageCount", "message_count"}, {"AcceptedAt", "accepted_at"},
		})
	})
	t.Run("soul capability", func(t *testing.T) {
		assertNestedZeroShape[soul.CapabilityV2](t, []nestedFieldSpec{
			{"Capability", "capability"}, {"Scope", "scope"}, {"ClaimLevel", "claimLevel"},
		})
	})
	t.Run("soul self-description", func(t *testing.T) {
		assertNestedZeroShape[soul.SelfDescriptionV2](t, []nestedFieldSpec{
			{"Purpose", "purpose"}, {"AuthoredBy", "authoredBy"},
		})
	})
	t.Run("contact availability window", func(t *testing.T) {
		assertNestedZeroShape[SoulContactAvailabilityWindow](t, []nestedFieldSpec{
			{"Days", "days"}, {"StartTime", "startTime"}, {"EndTime", "endTime"},
		})
	})
	t.Run("link safety result", func(t *testing.T) {
		assertNestedZeroShape[LinkSafetyBasicLinkResult](t, []nestedFieldSpec{{"URL", "url"}, {"Risk", "risk"}})
	})
	t.Run("ai error", func(t *testing.T) {
		assertNestedZeroShape[AIError](t, []nestedFieldSpec{{"Code", "code"}, {"Message", "message"}})
	})
	t.Run("ai evidence", func(t *testing.T) {
		assertNestedZeroShape[AIEvidenceRef](t, []nestedFieldSpec{{"Kind", "kind"}})
	})
}

func assertNestedZeroShape[T any](t *testing.T, fields []nestedFieldSpec) {
	t.Helper()

	typ := reflect.TypeOf((*T)(nil)).Elem()
	omittedAttrs := make([]string, 0, len(fields))
	for _, expected := range fields {
		field, ok := typ.FieldByName(expected.name)
		if !ok {
			t.Fatalf("%s.%s is missing", typ.Name(), expected.name)
		}
		tag := field.Tag.Get("theorydb")
		if !strings.Contains(tag, "attr:"+expected.attr) || !hasTagOption(tag, "omitempty") {
			t.Fatalf("%s.%s must preserve attr %q with explicit theorydb omitempty, got %q", typ.Name(), expected.name, expected.attr, tag)
		}
		if field.Type.Kind() != reflect.Struct || field.Type.PkgPath() == "time" {
			omittedAttrs = append(omittedAttrs, expected.attr)
		}
	}

	item := marshalTableTheoryV3Model(t, &nestedPersistenceProbe[T]{PK: "PROBE", SK: typ.Name()})
	nested := requireAttributeMap(t, item["nested"])
	for _, attr := range omittedAttrs {
		if value, ok := nested[attr]; ok {
			t.Fatalf("zero %s.%s must retain the v2 omission, got %#v", typ.Name(), attr, value)
		}
	}
}

func hasTagOption(tag string, want string) bool {
	parts := strings.Split(tag, ",")
	for _, part := range parts[1:] {
		if part == want {
			return true
		}
	}
	return false
}

func marshalTableTheoryV3Model(t *testing.T, value any) map[string]types.AttributeValue {
	t.Helper()
	registry := theorymodel.NewRegistry()
	if err := registry.Register(value); err != nil {
		t.Fatalf("register model: %v", err)
	}
	metadata, err := registry.GetMetadata(value)
	if err != nil {
		t.Fatalf("model metadata: %v", err)
	}
	item, err := marshal.NewSafeMarshaler().MarshalItem(value, metadata)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	return item
}

func requireAttributeMap(t *testing.T, value types.AttributeValue) map[string]types.AttributeValue {
	t.Helper()
	mapped, ok := value.(*types.AttributeValueMemberM)
	if !ok {
		t.Fatalf("expected map attribute, got %#v", value)
	}
	return mapped.Value
}
