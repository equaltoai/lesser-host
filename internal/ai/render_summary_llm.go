package ai

import (
	"regexp"
	"strings"
)

// RenderSummaryLLMModule and RenderSummaryLLMPolicyVersion identify the render-summary module and version.
const (
	RenderSummaryLLMModule        = "render_summary_llm"
	RenderSummaryLLMPolicyVersion = "v1"
)

// RenderSummaryInputsV1 is the input payload for render summarization.
type RenderSummaryInputsV1 struct {
	RenderID      string `json:"render_id"`
	NormalizedURL string `json:"normalized_url"`
	ResolvedURL   string `json:"resolved_url,omitempty"`
	RenderedAt    string `json:"rendered_at,omitempty"`

	LinkRisk string `json:"link_risk,omitempty"`

	// Text is bounded evidence (e.g., RenderArtifact.textPreview).
	Text string `json:"text,omitempty"`
}

// RenderSummaryRisk describes a single risk signal extracted during summarization.
type RenderSummaryRisk struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // low|medium|high
	Summary  string `json:"summary"`
}

// RenderSummaryResultV1 is the normalized output payload for render summarization.
type RenderSummaryResultV1 struct {
	Kind string `json:"kind"`
	// Version is the module-specific result schema version (not the policyVersion).
	Version string `json:"version"`

	ShortSummary string              `json:"short_summary"`
	KeyBullets   []string            `json:"key_bullets,omitempty"`
	Risks        []RenderSummaryRisk `json:"risks,omitempty"`
}

// SummarizeTextDeterministic returns a bounded, deterministic summary string.
func SummarizeTextDeterministic(in string, maxLen int) string {
	in = strings.ReplaceAll(in, "\r\n", "\n")
	in = strings.ReplaceAll(in, "\r", "\n")

	lines := strings.Split(in, "\n")
	out := make([]string, 0, 12)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= 12 {
			break
		}
	}

	joined := strings.Join(out, " ")
	joined = strings.Join(strings.Fields(joined), " ")
	if strings.TrimSpace(joined) == "" {
		return ""
	}

	if maxLen <= 0 {
		maxLen = 512
	}
	if len(joined) > maxLen {
		joined = strings.TrimSpace(joined[:maxLen])
	}
	return joined
}

func bulletsDeterministic(in string, maxBullets int, maxLen int) []string {
	in = strings.ReplaceAll(in, "\r\n", "\n")
	in = strings.ReplaceAll(in, "\r", "\n")

	if maxBullets <= 0 {
		maxBullets = 5
	}
	if maxLen <= 0 {
		maxLen = 160
	}

	lines := strings.Split(in, "\n")
	out := make([]string, 0, maxBullets)
	seen := map[string]struct{}{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.Join(strings.Fields(line), " ")
		if len(line) > maxLen {
			line = strings.TrimSpace(line[:maxLen])
		}
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
		if len(out) >= maxBullets {
			break
		}
	}
	return out
}

var riskyKeywordRE = regexp.MustCompile(`(?i)\b(download|install|password|verify\s+account|free\s+gift|crypto|wallet)\b`)

// RenderSummaryDeterministicV1 produces a deterministic summary and risk signals from inputs.
func RenderSummaryDeterministicV1(in RenderSummaryInputsV1) RenderSummaryResultV1 {
	text := strings.TrimSpace(in.Text)
	short := SummarizeTextDeterministic(text, 512)
	if short == "" {
		short = SummarizeTextDeterministic(in.NormalizedURL, 256)
	}

	var risks []RenderSummaryRisk
	risk := strings.ToLower(strings.TrimSpace(in.LinkRisk))
	switch risk {
	case "high":
		risks = append(risks, RenderSummaryRisk{Code: "link_risk_high", Severity: "high", Summary: "Link safety checks marked this URL as high risk."})
	case "medium":
		risks = append(risks, RenderSummaryRisk{Code: "link_risk_medium", Severity: "medium", Summary: "Link safety checks marked this URL as medium risk."})
	}
	if riskyKeywordRE.MatchString(text) {
		risks = append(risks, RenderSummaryRisk{Code: "suspicious_keywords", Severity: "medium", Summary: "Page text contains potentially risky keywords."})
	}

	return RenderSummaryResultV1{
		Kind:         "render_summary",
		Version:      "v1",
		ShortSummary: short,
		KeyBullets:   bulletsDeterministic(text, 5, 180),
		Risks:        risks,
	}
}
