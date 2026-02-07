package ai

import (
	"strings"
	"testing"
)

func TestModerationTextDeterministicV1_FlagsPIIAndScams(t *testing.T) {
	t.Parallel()

	text := "Contact me at test@example.com to verify account password."
	out := ModerationTextDeterministicV1(text)

	if out.Kind != "moderation_text" || out.Version != "v1" {
		t.Fatalf("unexpected envelope: %#v", out)
	}
	if out.Decision != "review" {
		t.Fatalf("expected review, got %q", out.Decision)
	}
	if len(out.Categories) < 2 {
		t.Fatalf("expected multiple categories, got %#v", out.Categories)
	}
	if len(out.Highlights) != 1 || out.Highlights[0] != text {
		t.Fatalf("unexpected highlights: %#v", out.Highlights)
	}
}

func TestModerationTextDeterministicV1_BlocksSelfHarmAndBoundsHighlights(t *testing.T) {
	t.Parallel()

	text := "please KILL YOURSELF " + strings.Repeat("x", 500)
	out := ModerationTextDeterministicV1(text)

	if out.Decision != "block" {
		t.Fatalf("expected block, got %q", out.Decision)
	}
	if len(out.Categories) == 0 || out.Categories[len(out.Categories)-1].Code != "self_harm" {
		t.Fatalf("expected self_harm category, got %#v", out.Categories)
	}
	if len(out.Highlights) != 1 || len(out.Highlights[0]) != 160 {
		t.Fatalf("expected bounded highlight, got %#v", out.Highlights)
	}
}

func TestSummarizeTextDeterministic_NormalizesWhitespaceAndBounds(t *testing.T) {
	t.Parallel()

	if got := SummarizeTextDeterministic("   \n  ", 10); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	in := "a\r\n\r\nb\n  c   d  \n\n"
	if got := SummarizeTextDeterministic(in, 10); got != "a b c d" {
		t.Fatalf("expected normalized summary, got %q", got)
	}

	long := strings.Repeat("x", 600)
	if got := SummarizeTextDeterministic(long, 0); len(got) != 512 {
		t.Fatalf("expected default bound to 512, got len=%d", len(got))
	}
}

func TestBulletsDeterministic_DedupesAndDefaults(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 300)
	in := strings.Join([]string{
		"",
		" a  ",
		"a",
		long,
		"b",
		"b",
		"c",
	}, "\n")

	got := bulletsDeterministic(in, 0, 0)
	if len(got) != 4 {
		t.Fatalf("expected 4 bullets, got %#v", got)
	}
	if got[0] != "a" || got[1] != strings.Repeat("x", 160) {
		t.Fatalf("unexpected bullets: %#v", got)
	}
}

func TestRenderSummaryDeterministicV1_EmitsRisksAndFallbackSummary(t *testing.T) {
	t.Parallel()

	out := RenderSummaryDeterministicV1(RenderSummaryInputsV1{
		NormalizedURL: "https://example.com",
		LinkRisk:      "HIGH",
		Text:          "Download now\nVerify account\nDownload now\n",
	})

	if out.Kind != "render_summary" || out.Version != "v1" {
		t.Fatalf("unexpected envelope: %#v", out)
	}
	if out.ShortSummary == "" {
		t.Fatalf("expected short summary")
	}
	if len(out.KeyBullets) == 0 {
		t.Fatalf("expected bullets")
	}
	if len(out.Risks) < 2 {
		t.Fatalf("expected multiple risks, got %#v", out.Risks)
	}

	out2 := RenderSummaryDeterministicV1(RenderSummaryInputsV1{
		NormalizedURL: " https://example.com/x ",
		Text:          " ",
	})
	if out2.ShortSummary != "https://example.com/x" {
		t.Fatalf("expected fallback summary from URL, got %q", out2.ShortSummary)
	}
}

