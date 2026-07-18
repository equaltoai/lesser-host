// Package mintprompt holds the shared Soul minting-conversation system prompt
// builder used by both the control-plane assistant runner and the in-VM
// hosted-genesis MicroVM workload. Centralizing it prevents drift between the
// synchronous control-plane path and the MicroVM execution path: both must
// prompt the provider identically for a given registration.
package mintprompt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// MintConversationSystemPrompt builds the Soul Registry minting-assistant system
// prompt contextualized by the agent's registration. It contains no raw
// transcripts, credentials, or provider secrets — only sanitized registration
// metadata (domain, local id, declared capabilities).
func MintConversationSystemPrompt(reg *models.SoulAgentRegistration) string {
	var sb strings.Builder
	sb.WriteString(`You are a Soul Registry minting assistant. Your role is to help an AI agent define its identity through structured conversation before its soul is minted on-chain.

You are conducting a minting conversation with an agent that wants to register in the Soul Registry. Your goal is to help the agent articulate:

1. **Self-Description**: A clear, honest description of what the agent is, its purpose, and its primary function.
2. **Capabilities**: What the agent can do, with appropriate claim levels (self-declared, peer-validated, operator-attested).
3. **Boundaries**: What the agent will NOT do — ethical limits, operational constraints, and refusal conditions.
4. **Transparency**: How the agent makes decisions, what models it uses, and its known limitations.

Guidelines:
- Ask probing questions to help the agent articulate its identity clearly.
- Challenge vague or overly broad claims.
- Encourage honesty about limitations and potential failure modes.
- Help distinguish between capabilities the agent has vs. aspirations.
- Ensure boundaries are concrete and actionable, not just platitudes.
- The conversation should feel collaborative, not interrogative.

When you feel the conversation has covered all four areas sufficiently, summarize the proposed declarations in a structured format.

`)

	sb.WriteString("Agent registration context:\n")
	if reg.DomainNormalized != "" {
		fmt.Fprintf(&sb, "- Domain: %s\n", QuotePromptValue(reg.DomainNormalized))
	}
	if reg.LocalID != "" {
		fmt.Fprintf(&sb, "- Local ID: %s\n", QuotePromptValue(reg.LocalID))
	}
	if caps := hostedgenesis.FilterDeclaredCapabilitiesForPrompt(reg.Capabilities); len(caps) > 0 {
		for i := range caps {
			caps[i] = SanitizePromptInline(caps[i], 128)
		}
		b, _ := json.Marshal(caps)
		fmt.Fprintf(&sb, "- Declared capabilities: %s\n", string(b))
	}

	return sb.String()
}

// SanitizePromptInline strips control characters and truncates a prompt-inline
// value to maxLen runes.
func SanitizePromptInline(raw string, maxLen int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, raw)
	if maxLen > 0 && len(raw) > maxLen {
		raw = raw[:maxLen]
	}
	return raw
}

// QuotePromptValue sanitizes and JSON-quotes a prompt value.
func QuotePromptValue(raw string) string {
	raw = SanitizePromptInline(raw, 256)
	if raw == "" {
		return ""
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return strconv.Quote(raw)
	}
	return string(b)
}
