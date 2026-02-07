package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/ai"
)

func TestBuildModerationPromptItems_TrimsAndBounds(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 10*1024)
	textItems := []ModerationTextBatchItem{
		{ItemID: "", Input: ai.ModerationTextInputsV1{Text: "x"}},
		{ItemID: " a ", Input: ai.ModerationTextInputsV1{Text: long}, Evidence: json.RawMessage(` {"a":1} `)},
	}
	got := buildModerationTextPromptItems(textItems)
	if len(got) != 1 {
		t.Fatalf("expected one prompt item, got %#v", got)
	}
	if got[0].ItemID != "a" {
		t.Fatalf("expected trimmed id, got %q", got[0].ItemID)
	}
	if len(got[0].Text) != 8*1024 {
		t.Fatalf("expected bounded text, got len=%d", len(got[0].Text))
	}
	if string(got[0].Signals) != `{"a":1}` {
		t.Fatalf("expected trimmed signals, got %s", string(got[0].Signals))
	}

	imageItems := []ModerationImageBatchItem{
		{ItemID: "", Input: ai.ModerationImageInputsV1{ObjectKey: "x"}},
		{ItemID: " b ", Input: ai.ModerationImageInputsV1{ObjectKey: " k "}, Evidence: json.RawMessage(` { } `)},
	}
	got = buildModerationImagePromptItems(imageItems)
	if len(got) != 1 || got[0].ItemID != "b" || got[0].ObjectKey != "k" {
		t.Fatalf("unexpected image prompt items: %#v", got)
	}
	if string(got[0].Signals) != `{ }` {
		t.Fatalf("expected trimmed signals, got %s", string(got[0].Signals))
	}
}

func TestNormalizeModerationHelpers(t *testing.T) {
	t.Parallel()

	if got := normalizeModerationDecision(" "); got != moderationDecisionReview {
		t.Fatalf("expected default review, got %q", got)
	}
	if got := normalizeModerationDecision("ALLOW"); got != "allow" {
		t.Fatalf("expected allow, got %q", got)
	}

	cats := normalizeModerationCategories([]ai.ModerationCategoryV1{
		{Code: " ", Severity: "high", Summary: "x", Confidence: 0.9},
		{Code: "pii", Severity: "NOPE", Summary: strings.Repeat("s", 300), Confidence: 2},
	})
	if len(cats) != 1 || cats[0].Code != "pii" || cats[0].Severity != renderSummarySeverityMedium {
		t.Fatalf("unexpected categories: %#v", cats)
	}
	if cats[0].Confidence != 1 || len(cats[0].Summary) != 240 {
		t.Fatalf("expected confidence/summary bounds, got %#v", cats[0])
	}

	highlights := normalizeModerationHighlights([]string{" ", strings.Repeat("h", 300), "ok"})
	if len(highlights) != 2 || len(highlights[0]) != 160 || highlights[1] != "ok" {
		t.Fatalf("unexpected highlights: %#v", highlights)
	}
}

func TestNormalizeModerationBatchOutput(t *testing.T) {
	t.Parallel()

	notes := strings.Repeat("n", 400)
	out := normalizeModerationBatchOutput(moderationBatchOutput{
		Items: []moderationBatchOutputItem{
			{ItemID: "", Decision: "allow"},
			{
				ItemID:     " id ",
				Decision:   "NOPE",
				Categories: []ai.ModerationCategoryV1{{Code: "pii", Severity: "high", Summary: "x", Confidence: 0.5}},
				Highlights: []string{"a"},
				Notes:      notes,
			},
		},
	}, "moderation_text")
	item, ok := out["id"]
	if !ok {
		t.Fatalf("expected output for id")
	}
	if item.Kind != "moderation_text" || item.Version != "v1" {
		t.Fatalf("unexpected envelope: %#v", item)
	}
	if item.Decision != moderationDecisionReview {
		t.Fatalf("expected decision normalized to review, got %q", item.Decision)
	}
	if len(item.Notes) != 240 {
		t.Fatalf("expected notes bounded to 240, got len=%d", len(item.Notes))
	}
}

func TestParseModerationBatchOutput(t *testing.T) {
	t.Parallel()

	if _, err := parseModerationBatchOutput("{"); err == nil {
		t.Fatalf("expected json error")
	}

	parsed, err := parseModerationBatchOutput(`{"items":[{"item_id":"x","decision":"allow","categories":[],"highlights":[],"notes":""}]}`)
	if err != nil {
		t.Fatalf("parseModerationBatchOutput: %v", err)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].ItemID != "x" {
		t.Fatalf("unexpected parsed output: %#v", parsed)
	}
}
