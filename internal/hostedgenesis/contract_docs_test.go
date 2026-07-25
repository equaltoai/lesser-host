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

func TestHostedGenesisConversationContractCodifiesM11TypedCandidateCutover(t *testing.T) {
	assertHostedGenesisConversationContractM11Policy(t)
	assertHostedGenesisConversationSourceM11Cutover(t)
	assertHostedGenesisConversationCandidateResponseSchema(t)
	assertHostedGenesisConversationReviewFixtures(t)
	assertHostedGenesisConversationCandidateOpenAPI(t)
}

func assertHostedGenesisConversationContractM11Policy(t *testing.T) {
	t.Helper()
	contractBody, readErr := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "hosted-genesis-conversation.md"))
	if readErr != nil {
		t.Fatalf("read hosted-genesis conversation contract: %v", readErr)
	}
	contract := string(contractBody)
	for _, want := range []string{
		"typed declaration candidate",
		"candidate_action",
		"exact canonical JSON",
		"No provider request occurs after affirmation",
		"hard cutover",
	} {
		if !strings.Contains(contract, want) {
			t.Fatalf("hosted-genesis conversation contract missing M11 billing policy phrase %q", want)
		}
	}
}

func assertHostedGenesisConversationSourceM11Cutover(t *testing.T) {
	t.Helper()
	asyncBody, readErr := os.ReadFile(filepath.Join("..", "controlplane", "handlers_soul_mint_conversation_async.go"))
	if readErr != nil {
		t.Fatalf("read async mint conversation source: %v", readErr)
	}
	asyncSrc := string(asyncBody)
	for _, forbidden := range []string{"startHostedGenesisDeclarationExtraction", "soulMintConversationExtractModule", "hostedGenesisSyncAssistantFallbackEnabled"} {
		if strings.Contains(asyncSrc, forbidden) {
			t.Fatalf("active M11 accepted-turn path retains removed compatibility symbol %q", forbidden)
		}
	}
	for _, required := range []string{"NewDeclarationCandidate", "ApplyDeclarationCandidateAction"} {
		if !strings.Contains(asyncSrc, required) {
			t.Fatalf("active M11 accepted-turn path missing structural candidate operation %q", required)
		}
	}

	if _, statErr := os.Stat(filepath.Join("..", "ai", "llm", "mint_conversation_declarations.go")); !os.IsNotExist(statErr) {
		t.Fatalf("whole-transcript declaration extractor must be deleted, stat err=%v", statErr)
	}
}

func assertHostedGenesisConversationCandidateResponseSchema(t *testing.T) {
	t.Helper()
	responseSchemaBody, readErr := os.ReadFile(filepath.Join("..", "..", "docs", "spec", "v3", "schemas", "hosted-genesis.conversation.response.schema.json"))
	if readErr != nil {
		t.Fatalf("read hosted-genesis response schema: %v", readErr)
	}
	var responseSchema map[string]any
	if err := json.Unmarshal(responseSchemaBody, &responseSchema); err != nil {
		t.Fatalf("parse hosted-genesis response schema: %v", err)
	}
	defs := requireJSONMap(t, responseSchema, "$defs")
	candidate := requireJSONMap(t, defs, "declaration_candidate")
	candidateProps := requireJSONMap(t, candidate, "properties")
	for _, field := range []string{"phase", "current_section", "completed_sections", "revision", "candidate_hash", "review"} {
		if _, ok := candidateProps[field]; !ok {
			t.Fatalf("typed candidate response schema missing %s", field)
		}
	}
	review := requireJSONMap(t, defs, "candidate_review")
	reviewText := requireJSONMap(t, requireJSONMap(t, review, "properties"), "review_text")
	if got, ok := reviewText["maxLength"].(float64); !ok || int(got) != MaxDeclarationOwnerReviewRunes {
		t.Fatalf("candidate review API limit drift: %#v", reviewText["maxLength"])
	}
	if description, _ := reviewText["description"].(string); !strings.Contains(description, "exact canonical JSON") {
		t.Fatalf("candidate review schema does not describe the lossless payload: %#v", reviewText)
	}
}

func assertHostedGenesisConversationReviewFixtures(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"hosted-genesis.conversation.assistant-turn-ready.example.json",
		"hosted-genesis.conversation.completed-declaration-ready.example.json",
		"hosted-genesis.conversation.published.example.json",
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "docs", "spec", "v3", "fixtures", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var fixture struct {
			Conversation struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
				DeclarationCandidate struct {
					CandidateHash string `json:"candidate_hash"`
					Review        struct {
						CandidateHash string `json:"candidate_hash"`
						ReviewHash    string `json:"review_hash"`
						ReviewText    string `json:"review_text"`
					} `json:"review"`
				} `json:"declaration_candidate"`
			} `json:"conversation"`
		}
		if unmarshalErr := json.Unmarshal(body, &fixture); unmarshalErr != nil {
			t.Fatalf("parse %s: %v", name, unmarshalErr)
		}
		candidate := fixture.Conversation.DeclarationCandidate
		recovered, err := RecoverDeclarationOwnerReviewCanonicalJSON(candidate.Review.ReviewText)
		if err != nil {
			t.Fatalf("recover %s review payload: %v", name, err)
		}
		if candidate.CandidateHash != candidate.Review.CandidateHash || hashText(recovered) != candidate.CandidateHash {
			t.Fatalf("%s candidate review hash does not authenticate exact canonical bytes", name)
		}
		if hashText(candidate.Review.ReviewText) != candidate.Review.ReviewHash {
			t.Fatalf("%s review hash does not authenticate exact review text", name)
		}
		for _, message := range fixture.Conversation.Messages {
			if message.Role == "assistant" && message.Content != candidate.Review.ReviewText {
				t.Fatalf("%s assistant review projection diverges from candidate review", name)
			}
		}
	}
}

func assertHostedGenesisConversationCandidateOpenAPI(t *testing.T) {
	t.Helper()
	openAPIBody, readErr := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "openapi.yaml"))
	if readErr != nil {
		t.Fatalf("read Host OpenAPI: %v", readErr)
	}
	openAPI := string(openAPIBody)
	requestStart := strings.Index(openAPI, "    SoulHostedGenesisMintConversationRequest:")
	requestEnd := strings.Index(openAPI, "    SoulHostedGenesisConversationResponse:")
	if requestStart < 0 || requestEnd <= requestStart {
		t.Fatal("Host OpenAPI missing bounded Hosted Genesis request component")
	}
	requestComponent := openAPI[requestStart:requestEnd]
	if !strings.Contains(requestComponent, "candidate_action:") || !strings.Contains(requestComponent, "candidate_revision:") || !strings.Contains(requestComponent, "review_hash:") {
		t.Fatal("Host OpenAPI request does not expose structural candidate_action bindings")
	}
	if strings.Contains(requestComponent, "declarations:") {
		t.Fatal("Host OpenAPI request still permits client-authored declarations")
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
