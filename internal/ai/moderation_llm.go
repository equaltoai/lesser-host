package ai

import (
	"regexp"
	"strings"
)

// Moderation*LLMModule and Moderation*LLMPolicyVersion identify moderation modules and versions.
const (
	ModerationTextLLMModule        = "moderation_text_llm"
	ModerationTextLLMPolicyVersion = "v1"

	ModerationImageLLMModule        = "moderation_image_llm"
	ModerationImageLLMPolicyVersion = "v1"
)

// ModerationTextInputsV1 is the input payload for text moderation.
type ModerationTextInputsV1 struct {
	// Text is bounded untrusted content to scan.
	Text string `json:"text"`
}

// ModerationImageInputsV1 is the input payload for image moderation.
type ModerationImageInputsV1 struct {
	ObjectKey string `json:"object_key"`

	// ObjectETag/Bytes/ContentType are for stable caching + guardrails without reading the object.
	ObjectETag  string `json:"object_etag,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// ModerationCategoryV1 describes a single moderation category signal.
type ModerationCategoryV1 struct {
	Code       string  `json:"code"`
	Confidence float64 `json:"confidence"`
	Severity   string  `json:"severity"` // low|medium|high
	Summary    string  `json:"summary,omitempty"`
}

// ModerationResultV1 is the normalized output payload for moderation modules.
type ModerationResultV1 struct {
	Kind    string `json:"kind"`    // moderation_text|moderation_image
	Version string `json:"version"` // v1

	Decision string `json:"decision"` // allow|review|block

	Categories []ModerationCategoryV1 `json:"categories,omitempty"`
	Highlights []string               `json:"highlights,omitempty"`
	Notes      string                 `json:"notes,omitempty"`
}

var moderationPIIRE = regexp.MustCompile(`(?i)(\b\d{3}-\d{2}-\d{4}\b|\b\d{12,19}\b|\b[\w.+-]+@[\w.-]+\.[a-z]{2,}\b)`)

// ModerationTextDeterministicV1 performs a deterministic moderation pass over text.
func ModerationTextDeterministicV1(text string) ModerationResultV1 {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)

	out := ModerationResultV1{
		Kind:     "moderation_text",
		Version:  "v1",
		Decision: "allow",
	}

	if moderationPIIRE.MatchString(text) {
		out.Decision = "review"
		out.Categories = append(out.Categories, ModerationCategoryV1{
			Code:       "pii",
			Confidence: 0.7,
			Severity:   "medium",
			Summary:    "Text may contain personally identifiable information.",
		})
	}

	switch {
	case strings.Contains(lower, "kill yourself") || strings.Contains(lower, "suicide"):
		out.Decision = "block"
		out.Categories = append(out.Categories, ModerationCategoryV1{
			Code:       "self_harm",
			Confidence: 0.8,
			Severity:   "high",
			Summary:    "Text references self-harm.",
		})
	case strings.Contains(lower, "password") || strings.Contains(lower, "verify account") || strings.Contains(lower, "seed phrase"):
		if out.Decision != "block" {
			out.Decision = "review"
		}
		out.Categories = append(out.Categories, ModerationCategoryV1{
			Code:       "spam_or_scams",
			Confidence: 0.6,
			Severity:   "medium",
			Summary:    "Text contains phrases commonly used in scams/phishing.",
		})
	}

	// Keep highlights bounded and deterministic.
	if out.Decision != "allow" && len(text) > 0 {
		h := text
		if len(h) > 160 {
			h = strings.TrimSpace(h[:160])
		}
		out.Highlights = []string{h}
	}

	return out
}
