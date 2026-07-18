package hostedgenesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFiveBodyContractDocsPinSchemaGuidanceVersions(t *testing.T) {
	schema := readContractJSON(t, "soul-five-body.schema.v2.json")
	props := requireJSONMap(t, schema, "properties")
	assertJSONConst(t, requireJSONMap(t, props, "schemaVersion"), DeclarationSchemaVersionV2)
	assertJSONConst(t, requireJSONMap(t, props, "guidanceVersion"), GuidanceVersionV2)
	fiveBodies := requireJSONMap(t, props, "fiveBodies")
	fiveProps := requireJSONMap(t, fiveBodies, "properties")
	for _, body := range []string{"identity", "philosophy", "discipline", "boundaries", "soul"} {
		if _, ok := fiveProps[body]; !ok {
			t.Fatalf("contract schema missing fiveBodies.%s", body)
		}
	}
	soulBody := requireJSONMap(t, fiveProps, "soul")
	refusals := requireJSONMap(t, requireJSONMap(t, soulBody, "properties"), "refusals")
	minItems, ok := refusals["minItems"].(float64)
	if !ok {
		t.Fatalf("expected soul.refusals minItems number, got %#v", refusals["minItems"])
	}
	if got := int(minItems); got != 3 {
		t.Fatalf("expected soul.refusals minItems=3, got %d", got)
	}

	example := readContractJSON(t, "soul-five-body.example.v2.json")
	if example["schemaVersion"] != DeclarationSchemaVersionV2 || example["guidanceVersion"] != GuidanceVersionV2 {
		t.Fatalf("example version mismatch: %#v", example)
	}

	bodyBytes, err := os.ReadFile(contractPath("soul-five-body-schema.md"))
	if err != nil {
		t.Fatalf("read markdown contract: %v", err)
	}
	body := string(bodyBytes)
	for _, want := range []string{DeclarationSchemaVersionV2, GuidanceVersionV2, "soul.refusals.invalid", "HOSTED_GENESIS_DECLARATION_SCHEMA_VERSION=v2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("markdown contract missing %q", want)
		}
	}
}

func readContractJSON(t *testing.T, name string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(contractPath(name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return parsed
}

func contractPath(name string) string {
	return filepath.Join("..", "..", "docs", "contracts", name)
}

func requireJSONMap(t *testing.T, src map[string]any, key string) map[string]any {
	t.Helper()
	child, ok := src[key].(map[string]any)
	if !ok {
		t.Fatalf("expected %s object in %#v", key, src[key])
	}
	return child
}

func assertJSONConst(t *testing.T, src map[string]any, want string) {
	t.Helper()
	if got, ok := src["const"].(string); !ok || got != want {
		t.Fatalf("expected const %q, got %#v", want, src["const"])
	}
}
