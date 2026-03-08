package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// MintConversationDeclarationsInput is the prompt payload used to extract
// structured soul registration declarations from a minting conversation transcript.
type MintConversationDeclarationsInput struct {
	Registration MintConversationRegistrationContext `json:"registration"`
	Messages     []MintConversationMessage           `json:"messages"`
}

type MintConversationRegistrationContext struct {
	Domain               string   `json:"domain,omitempty"`
	LocalID              string   `json:"local_id,omitempty"`
	AgentID              string   `json:"agent_id,omitempty"`
	DeclaredCapabilities []string `json:"declared_capabilities,omitempty"`
}

type MintConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MintConversationDeclarationsDraft is a v2-compatible set of declarations,
// but without host-populated metadata like boundary IDs and signatures.
type MintConversationDeclarationsDraft struct {
	SelfDescription soul.SelfDescriptionV2          `json:"selfDescription"`
	Capabilities    []soul.CapabilityV2             `json:"capabilities"`
	Boundaries      []MintConversationBoundaryDraft `json:"boundaries"`
	Transparency    map[string]any                  `json:"transparency"`
}

type MintConversationBoundaryDraft struct {
	Category  string `json:"category"`
	Statement string `json:"statement"`
	Rationale string `json:"rationale,omitempty"`
}

const maxMintConversationBoundaryDrafts = 4

// MintConversationDeclarationsOpenAI extracts declarations using OpenAI JSON schema output.
func MintConversationDeclarationsOpenAI(ctx context.Context, apiKey string, modelSet string, in MintConversationDeclarationsInput) (MintConversationDeclarationsDraft, models.AIUsage, error) {
	return openAIJSONSchemaBatch(
		ctx,
		apiKey,
		modelSet,
		in,
		openAIJSONSchemaBatchConfig{
			SchemaName:        "soul_mint_conversation_declarations",
			SchemaDescription: "Extract v2 Soul Registration declarations from a minting conversation transcript.",
			Schema:            mintConversationDeclarationsJSONSchemaV1(),
			SystemPrompt:      mintConversationDeclarationsSystemPromptV1(),
			Temperature:       0.2,
		},
		parseMintConversationDeclarationsDraft,
		normalizeMintConversationDeclarationsDraft,
	)
}

// MintConversationDeclarationsAnthropic extracts declarations using Anthropic JSON text output.
func MintConversationDeclarationsAnthropic(ctx context.Context, apiKey string, modelSet string, in MintConversationDeclarationsInput) (MintConversationDeclarationsDraft, models.AIUsage, error) {
	return anthropicJSONTextBatch(
		ctx,
		apiKey,
		modelSet,
		in,
		anthropicJSONTextBatchConfig{
			Schema:       mintConversationDeclarationsJSONSchemaV1(),
			SystemPrompt: mintConversationDeclarationsSystemPromptV1(),
			Temperature:  0.2,
			MaxTokens:    4096,
		},
		parseMintConversationDeclarationsDraft,
		normalizeMintConversationDeclarationsDraft,
	)
}

func parseMintConversationDeclarationsDraft(raw string) (MintConversationDeclarationsDraft, error) {
	var parsed MintConversationDeclarationsDraft
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return MintConversationDeclarationsDraft{}, fmt.Errorf("invalid json output: %w", err)
	}
	return parsed, nil
}

func normalizeMintConversationDeclarationsDraft(parsed MintConversationDeclarationsDraft) MintConversationDeclarationsDraft {
	parsed.SelfDescription.Purpose = strings.TrimSpace(parsed.SelfDescription.Purpose)
	parsed.SelfDescription.Constraints = strings.TrimSpace(parsed.SelfDescription.Constraints)
	parsed.SelfDescription.Commitments = strings.TrimSpace(parsed.SelfDescription.Commitments)
	parsed.SelfDescription.Limitations = strings.TrimSpace(parsed.SelfDescription.Limitations)
	parsed.SelfDescription.AuthoredBy = strings.ToLower(strings.TrimSpace(parsed.SelfDescription.AuthoredBy))
	parsed.SelfDescription.MintingModel = strings.TrimSpace(parsed.SelfDescription.MintingModel)

	caps := make([]soul.CapabilityV2, 0, len(parsed.Capabilities))
	for _, c := range parsed.Capabilities {
		c.Capability = strings.TrimSpace(c.Capability)
		c.Scope = strings.TrimSpace(c.Scope)
		c.ClaimLevel = strings.ToLower(strings.TrimSpace(c.ClaimLevel))
		c.LastValidated = strings.TrimSpace(c.LastValidated)
		c.ValidationRef = strings.TrimSpace(c.ValidationRef)
		c.DegradesTo = strings.TrimSpace(c.DegradesTo)
		if c.Capability == "" || c.Scope == "" {
			continue
		}
		if c.ClaimLevel == "" {
			c.ClaimLevel = "self-declared"
		}
		caps = append(caps, c)
		if len(caps) >= 25 {
			break
		}
	}
	parsed.Capabilities = caps

	bounds := make([]MintConversationBoundaryDraft, 0, len(parsed.Boundaries))
	for _, b := range parsed.Boundaries {
		b.Category = strings.ToLower(strings.TrimSpace(b.Category))
		b.Statement = strings.TrimSpace(b.Statement)
		b.Rationale = strings.TrimSpace(b.Rationale)
		if b.Category == "" || b.Statement == "" {
			continue
		}
		bounds = append(bounds, b)
		if len(bounds) >= maxMintConversationBoundaryDrafts {
			break
		}
	}
	parsed.Boundaries = bounds

	if parsed.Transparency == nil {
		parsed.Transparency = map[string]any{}
	}

	return parsed
}

func mintConversationDeclarationsSystemPromptV1() string {
	return strings.TrimSpace(`
You are assisting with "Phase 2 — Minting conversation" for lesser-soul.

Your job is to extract structured self-definition declarations from a minting conversation transcript.

You MUST return only a single JSON object that matches the provided JSON schema, with no extra keys.

Guidance:
- Self-description should be honest and specific (purpose, constraints, commitments, limitations).
- Capabilities must be concrete: what the agent can do, with explicit scope. Use claimLevel "self-declared".
- Boundaries must be concrete refusals/scope limits/ethical commitments/circuit breakers.
- Prefer the smallest durable set of high-signal boundaries. Return 2-4 boundaries unless the transcript clearly supports fewer.
- Do not emit redundant or near-duplicate boundaries that would force extra wallet signatures later.
- Transparency should describe the agent's model/provider uncertainty and any relevant operational notes.
`)
}

func mintConversationDeclarationsJSONSchemaV1() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"selfDescription": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"purpose":      map[string]any{"type": "string"},
					"constraints":  map[string]any{"type": "string"},
					"commitments":  map[string]any{"type": "string"},
					"limitations":  map[string]any{"type": "string"},
					"authoredBy":   map[string]any{"type": "string", "enum": []string{"agent"}},
					"mintingModel": map[string]any{"type": "string"},
				},
				"required": []string{"purpose", "authoredBy"},
			},
			"capabilities": map[string]any{
				"type":     "array",
				"minItems": 1,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"capability": map[string]any{"type": "string"},
						"scope":      map[string]any{"type": "string"},
						"constraints": map[string]any{
							"type":                 "object",
							"additionalProperties": true,
						},
						"claimLevel": map[string]any{
							"type": "string",
							"enum": []string{"self-declared"},
						},
						"lastValidated": map[string]any{"type": "string"},
						"validationRef": map[string]any{"type": "string"},
						"degradesTo":    map[string]any{"type": "string"},
					},
					"required": []string{"capability", "scope", "claimLevel"},
				},
			},
			"boundaries": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": maxMintConversationBoundaryDrafts,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"category": map[string]any{
							"type": "string",
							"enum": []string{"refusal", "scope_limit", "ethical_commitment", "circuit_breaker"},
						},
						"statement": map[string]any{"type": "string"},
						"rationale": map[string]any{"type": "string"},
					},
					"required": []string{"category", "statement"},
				},
			},
			"transparency": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
			},
		},
		"required": []string{"selfDescription", "capabilities", "boundaries", "transparency"},
	}
}
