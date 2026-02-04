package ai

import (
	"regexp"
	"strings"
)

const (
	RenderSummaryLLMModule        = "render_summary_llm"
	RenderSummaryLLMPolicyVersion = "v1"
)

type RenderSummaryInputsV1 struct {
	RenderID      string `json:"render_id"`
	NormalizedURL string `json:"normalized_url"`
	ResolvedURL   string `json:"resolved_url,omitempty"`
	RenderedAt    string `json:"rendered_at,omitempty"`

	LinkRisk string `json:"link_risk,omitempty"`

	// Text is bounded evidence (e.g., RenderArtifact.textPreview).
	Text string `json:"text,omitempty"`
}

type RenderSummaryRisk struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // low|medium|high
	Summary  string `json:"summary"`
}

type RenderSummaryResultV1 struct {
	Kind string `json:"kind"`
	// Version is the module-specific result schema version (not the policyVersion).
	Version string `json:"version"`

	ShortSummary string              `json:"short_summary"`
	KeyBullets   []string            `json:"key_bullets,omitempty"`
	Risks        []RenderSummaryRisk `json:"risks,omitempty"`
}

func SummarizeTextDeterministic(in string, max int) string {
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

	if max <= 0 {
		max = 512
	}
	if len(joined) > max {
		joined = strings.TrimSpace(joined[:max])
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
