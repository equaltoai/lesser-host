package hostedgenesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestFiveBodyContractDocsPinSchemaGuidanceVersions(t *testing.T) {
	schema := readContractJSON(t, "soul-five-body.schema.v2.json")
	assertFiveBodyContractSchemaCore(t, schema)
	assertFiveBodyContractOptionalEvidence(t, schema)
	assertFiveBodyContractExample(t)
	assertFiveBodyContractMarkdown(t)
}

func TestHostedGenesisConversationContractCodifiesM11ActorPathBilling(t *testing.T) {
	contractBody, readErr := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "hosted-genesis-conversation.md"))
	if readErr != nil {
		t.Fatalf("read hosted-genesis conversation contract: %v", readErr)
	}
	contract := string(contractBody)
	for _, want := range []string{
		"M11 billing policy for actor-path declaration extraction",
		"does **not** create a second Host extraction debit",
		"Active M11 actor-path traffic",
		"finalization/publish",
	} {
		if !strings.Contains(contract, want) {
			t.Fatalf("hosted-genesis conversation contract missing M11 billing policy phrase %q", want)
		}
	}

	asyncBody, readErr := os.ReadFile(filepath.Join("..", "controlplane", "handlers_soul_mint_conversation_async.go"))
	if readErr != nil {
		t.Fatalf("read async mint conversation source: %v", readErr)
	}
	asyncSrc := string(asyncBody)
	progressBody, ok := between(asyncSrc, "func (s *Server) progressHostedGenesisAcceptedTurn", "func (s *Server) progressHostedGenesisAcceptedTurnSync")
	if !ok {
		t.Fatalf("could not locate progressHostedGenesisAcceptedTurn body")
	}
	if strings.Contains(progressBody, "startHostedGenesisDeclarationExtraction") ||
		strings.Contains(progressBody, "soulMintConversationExtractModule") {
		t.Fatalf("active M11 accepted-turn path must not call Host-owned extraction/debit machinery")
	}
	if !strings.Contains(asyncSrc, "startHostedGenesisDeclarationExtraction") ||
		!strings.Contains(asyncSrc, "compatibility/recovery seam") {
		t.Fatalf("legacy extraction seam must remain explicit and documented as compatibility/recovery only")
	}
}

func assertFiveBodyContractSchemaCore(t *testing.T, schema map[string]any) {
	t.Helper()
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
}

func assertFiveBodyContractOptionalEvidence(t *testing.T, schema map[string]any) {
	t.Helper()
	props := requireJSONMap(t, schema, "properties")
	fiveBodies := requireJSONMap(t, props, "fiveBodies")
	fiveProps := requireJSONMap(t, fiveBodies, "properties")
	soulBody := requireJSONMap(t, fiveProps, "soul")
	if requiredHas(requireJSONStringSlice(t, soulBody, "required"), "notes") {
		t.Fatalf("published produced-declaration schema must not require optional soul notes")
	}
	bodySection := requireJSONMap(t, requireJSONMap(t, schema, "$defs"), "bodySection")
	if requiredHas(requireJSONStringSlice(t, bodySection, "required"), "notes") {
		t.Fatalf("published produced-declaration schema must not require optional body notes")
	}
	capabilityItems := requireJSONMap(t, requireJSONMap(t, props, "capabilities"), "items")
	capabilityRequired := requireJSONStringSlice(t, capabilityItems, "required")
	for _, want := range []string{"capability", "scope", "claimLevel"} {
		if !requiredHas(capabilityRequired, want) {
			t.Fatalf("published capability schema must require %s, got %#v", want, capabilityRequired)
		}
	}
	for _, optional := range []string{"lastValidated", "validationRef", "degradesTo"} {
		if requiredHas(capabilityRequired, optional) {
			t.Fatalf("published capability schema must not require optional validation metadata %s", optional)
		}
	}
	capabilityProps := requireJSONMap(t, capabilityItems, "properties")
	capabilityName := requireJSONMap(t, capabilityProps, "capability")
	if capabilityName["pattern"] != ProducedCapabilityIdentifierPattern {
		t.Fatalf("published capability identifier pattern drift: %#v", capabilityName)
	}
	lastValidated := requireJSONMap(t, capabilityProps, "lastValidated")
	if lastValidated["pattern"] != ProducedCapabilityOptionalRFC3339Pattern {
		t.Fatalf("published lastValidated RFC3339 pattern drift: %#v", lastValidated)
	}
}

func assertFiveBodyContractExample(t *testing.T) {
	t.Helper()
	example := readContractJSON(t, "soul-five-body.example.v2.json")
	if example["schemaVersion"] != DeclarationSchemaVersionV2 || example["guidanceVersion"] != GuidanceVersionV2 {
		t.Fatalf("example version mismatch: %#v", example)
	}
	if validateErr := compileContractSchema(t, "soul-five-body.schema.v2.json").Validate(example); validateErr != nil {
		t.Fatalf("golden example does not validate against published schema: %v", validateErr)
	}
}

func assertFiveBodyContractMarkdown(t *testing.T) {
	t.Helper()
	bodyBytes, readErr := os.ReadFile(contractPath("soul-five-body-schema.md"))
	if readErr != nil {
		t.Fatalf("read markdown contract: %v", readErr)
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
	body, readErr := os.ReadFile(contractPath(name))
	if readErr != nil {
		t.Fatalf("read %s: %v", name, readErr)
	}
	var parsed map[string]any
	if unmarshalErr := json.Unmarshal(body, &parsed); unmarshalErr != nil {
		t.Fatalf("parse %s: %v", name, unmarshalErr)
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

func requireJSONStringSlice(t *testing.T, src map[string]any, key string) []string {
	t.Helper()
	raw, ok := src[key].([]any)
	if !ok {
		t.Fatalf("expected %s string array in %#v", key, src[key])
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		if !ok {
			t.Fatalf("expected %s string array item, got %#v", key, item)
		}
		out = append(out, value)
	}
	return out
}

func requiredHas(required []string, want string) bool {
	for _, got := range required {
		if got == want {
			return true
		}
	}
	return false
}

func between(body string, start string, end string) (string, bool) {
	startIdx := strings.Index(body, start)
	if startIdx < 0 {
		return "", false
	}
	tail := body[startIdx:]
	endIdx := strings.Index(tail[len(start):], end)
	if endIdx < 0 {
		return "", false
	}
	return tail[:len(start)+endIdx], true
}

func compileContractSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	schemaPath := contractPath(name)
	raw, readErr := os.ReadFile(schemaPath)
	if readErr != nil {
		t.Fatalf("read %s: %v", name, readErr)
	}
	var doc any
	if unmarshalErr := json.Unmarshal(raw, &doc); unmarshalErr != nil {
		t.Fatalf("parse %s: %v", name, unmarshalErr)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if addErr := compiler.AddResource(schemaPath, doc); addErr != nil {
		t.Fatalf("add schema resource %s: %v", name, addErr)
	}
	if id := schemaDocumentID(doc); id != "" {
		if addErr := compiler.AddResource(id, doc); addErr != nil {
			t.Fatalf("add schema resource %s: %v", id, addErr)
		}
	}
	schema, compileErr := compiler.Compile(schemaPath)
	if compileErr != nil {
		t.Fatalf("compile schema %s: %v", name, compileErr)
	}
	return schema
}

func schemaDocumentID(doc any) string {
	obj, ok := doc.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := obj["$id"].(string)
	return id
}
