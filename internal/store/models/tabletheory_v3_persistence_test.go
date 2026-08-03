package models

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/theory-cloud/tabletheory/v3/pkg/marshal"
	theorymodel "github.com/theory-cloud/tabletheory/v3/pkg/model"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

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
