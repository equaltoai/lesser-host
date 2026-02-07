package ai

import (
	"regexp"
	"strconv"
	"strings"
)

// ClaimVerifyLLMModule and ClaimVerifyLLMPolicyVersion identify the claim verification module and schema version.
const (
	ClaimVerifyLLMModule        = "claim_verify_llm"
	ClaimVerifyLLMPolicyVersion = "v1"

	ClaimVerifyRetrievalModeProvidedOnly    = "provided_only"
	ClaimVerifyRetrievalModeOpenAIWebSearch = "openai_web_search"

	ClaimVerifySearchContextLow    = "low"
	ClaimVerifySearchContextMedium = "medium"
	ClaimVerifySearchContextHigh   = "high"

	claimClassificationUnclear   = "unclear"
	claimClassificationCheckable = "checkable"
	claimClassificationOpinion   = "opinion"
)

// ClaimVerifyEvidenceV1 is an evidence snippet used to verify claims.
type ClaimVerifyEvidenceV1 struct {
	SourceID string `json:"source_id"`
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	RenderID string `json:"render_id,omitempty"`

	// Text is bounded untrusted evidence text provided by callers.
	Text string `json:"text"`
}

// ClaimVerifyRetrievalV1 configures optional retrieval behavior for claim verification.
type ClaimVerifyRetrievalV1 struct {
	Mode string `json:"mode,omitempty"` // provided_only|openai_web_search

	// MaxSources limits how many sources retrieval may add (bounded to 0..5).
	MaxSources int `json:"max_sources,omitempty"`
	// SearchContextSize is provider-specific; for OpenAI it maps to the web search tool context size.
	SearchContextSize string `json:"search_context_size,omitempty"` // low|medium|high
}

// ClaimVerifyInputsV1 is the input payload for claim verification.
type ClaimVerifyInputsV1 struct {
	ActorURI    string `json:"actor_uri,omitempty"`
	ObjectURI   string `json:"object_uri,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`

	// Text is the content to extract claims from. Optional if Claims is provided.
	Text string `json:"text,omitempty"`
	// Claims optionally provides explicit claims; when set, extraction is skipped.
	Claims []string `json:"claims,omitempty"`

	// Evidence is required; citations must reference these SourceIDs.
	Evidence []ClaimVerifyEvidenceV1 `json:"evidence"`

	// Retrieval controls whether the verifier may augment evidence (e.g., OpenAI web search).
	Retrieval *ClaimVerifyRetrievalV1 `json:"retrieval,omitempty"`
}

// ClaimVerifyCitationV1 references an evidence source and quote.
type ClaimVerifyCitationV1 struct {
	SourceID string `json:"source_id"`
	Quote    string `json:"quote,omitempty"`
}

// ClaimVerifyClaimV1 is a single claim verification result.
type ClaimVerifyClaimV1 struct {
	ClaimID string `json:"claim_id"`
	Text    string `json:"text"`

	Classification string  `json:"classification"` // checkable|opinion|unclear
	Verdict        string  `json:"verdict"`        // supported|refuted|inconclusive
	Confidence     float64 `json:"confidence"`     // 0-1

	Reason    string                  `json:"reason,omitempty"`
	Citations []ClaimVerifyCitationV1 `json:"citations,omitempty"`
}

// ClaimVerifyResultV1 is the output schema for claim verification.
type ClaimVerifyResultV1 struct {
	Kind     string               `json:"kind"`    // claim_verify
	Version  string               `json:"version"` // v1
	Claims   []ClaimVerifyClaimV1 `json:"claims"`
	Warnings []string             `json:"warnings,omitempty"`

	// Sources optionally echoes the bounded evidence texts used for citations (useful when web search retrieval is enabled).
	Sources []ClaimVerifyEvidenceV1 `json:"sources,omitempty"`
	// Disclaimer provides a short disclosure when retrieval/web search was used.
	Disclaimer string `json:"disclaimer,omitempty"`
}

var sentenceSplitRE = regexp.MustCompile(`[.!?]+\s+`)
var hasDigitRE = regexp.MustCompile(`\d`)

// ExtractClaimsDeterministicV1 extracts simple claims from text without any model calls.
func ExtractClaimsDeterministicV1(text string, maxClaims int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxClaims <= 0 {
		maxClaims = 10
	}

	parts := sentenceSplitRE.Split(text, -1)
	out := make([]string, 0, maxClaims)
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Join(strings.Fields(p), " ")
		if p == "" {
			continue
		}
		if len(p) > 240 {
			p = strings.TrimSpace(p[:240])
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
		if len(out) >= maxClaims {
			break
		}
	}
	return out
}

func classifyClaimDeterministic(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return claimClassificationUnclear
	}
	if hasDigitRE.MatchString(text) {
		return claimClassificationCheckable
	}
	l := strings.ToLower(text)
	switch {
	case strings.Contains(l, "i think"), strings.Contains(l, "i feel"), strings.Contains(l, "best"), strings.Contains(l, "worst"):
		return claimClassificationOpinion
	default:
		return claimClassificationUnclear
	}
}

// ClaimVerifyDeterministicV1 generates a best-effort claim verification result without any model calls.
func ClaimVerifyDeterministicV1(in ClaimVerifyInputsV1) ClaimVerifyResultV1 {
	claims := make([]string, 0, 10)
	for _, c := range in.Claims {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		claims = append(claims, c)
		if len(claims) >= 10 {
			break
		}
	}
	if len(claims) == 0 {
		claims = ExtractClaimsDeterministicV1(in.Text, 10)
	}

	out := ClaimVerifyResultV1{
		Kind:    "claim_verify",
		Version: "v1",
		Claims:  make([]ClaimVerifyClaimV1, 0, len(claims)),
	}

	for i, c := range claims {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		out.Claims = append(out.Claims, ClaimVerifyClaimV1{
			ClaimID:        "c" + strconv.Itoa(i+1),
			Text:           c,
			Classification: classifyClaimDeterministic(c),
			Verdict:        "inconclusive",
			Confidence:     0.0,
			Reason:         "deterministic_fallback",
			Citations:      []ClaimVerifyCitationV1{},
		})
	}

	return out
}
