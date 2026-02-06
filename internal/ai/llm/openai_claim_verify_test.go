package llm

import (
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/ai"
)

func TestTrimClaimVerifyClaims_BoundsAndNormalizes(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 300)
	in := []string{"  a  ", "", long, "b"}
	for i := 0; i < 20; i++ {
		in = append(in, "c")
	}

	got := trimClaimVerifyClaims(in)
	if len(got) != 10 {
		t.Fatalf("expected bounded to 10, got %#v", got)
	}
	if got[0] != "a" {
		t.Fatalf("expected trimmed first claim, got %q", got[0])
	}
	if len(got[1]) != 240 {
		t.Fatalf("expected long claim trimmed to 240, got len=%d", len(got[1]))
	}
}

func TestTrimClaimVerifyEvidence_BoundsAndNormalizes(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 10*1024)
	in := []ai.ClaimVerifyEvidenceV1{
		{SourceID: " ", Text: "x"},
		{SourceID: "s1", Text: " "},
		{SourceID: " s2 ", URL: " u ", Title: " t ", Text: long},
	}
	for i := 0; i < 10; i++ {
		in = append(in, ai.ClaimVerifyEvidenceV1{SourceID: "s", Text: "ok"})
	}

	got := trimClaimVerifyEvidence(in)
	if len(got) != 5 {
		t.Fatalf("expected bounded to 5, got %#v", got)
	}
	if got[0].SourceID != "s2" || got[0].URL != "u" || got[0].Title != "t" {
		t.Fatalf("expected trimmed evidence fields, got %#v", got[0])
	}
	if len(got[0].Text) != 8*1024 {
		t.Fatalf("expected text trimmed to 8KiB, got len=%d", len(got[0].Text))
	}
}

func TestBuildClaimVerifyPromptItems_SkipsInvalidAndTrims(t *testing.T) {
	t.Parallel()

	longText := strings.Repeat("z", 10*1024)
	items := []ClaimVerifyBatchItem{
		{ItemID: "", Input: ai.ClaimVerifyInputsV1{Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: "s", Text: "x"}}}},
		{ItemID: "a", Input: ai.ClaimVerifyInputsV1{Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: "", Text: "x"}}}},
		{
			ItemID: " b ",
			Input: ai.ClaimVerifyInputsV1{
				Text:     longText,
				Claims:   []string{" c1 ", "", "c2"},
				Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: " s ", Text: " t "}},
			},
		},
	}

	got := buildClaimVerifyPromptItems(items)
	if len(got) != 1 {
		t.Fatalf("expected only one valid prompt item, got %#v", got)
	}
	if got[0].ItemID != "b" {
		t.Fatalf("expected trimmed item id, got %q", got[0].ItemID)
	}
	if len(got[0].Text) != 8*1024 {
		t.Fatalf("expected bounded text, got len=%d", len(got[0].Text))
	}
	if len(got[0].Claims) != 2 || got[0].Claims[0] != "c1" || got[0].Claims[1] != "c2" {
		t.Fatalf("unexpected claims: %#v", got[0].Claims)
	}
	if len(got[0].Evidence) != 1 || got[0].Evidence[0].SourceID != "s" || got[0].Evidence[0].Text != "t" {
		t.Fatalf("unexpected evidence: %#v", got[0].Evidence)
	}
}

func TestNormalizeClaimVerifyClaim_CleansFieldsAndBounds(t *testing.T) {
	t.Parallel()

	longText := strings.Repeat("x", 400)
	longReason := strings.Repeat("r", 400)
	longQuote := strings.Repeat("q", 400)
	tooLongWarn := strings.Repeat("w", 400)

	out := normalizeClaimVerifyBatchOutput(claimVerifyBatchOutput{
		Items: []claimVerifyBatchOutputItem{{
			ItemID: " item ",
			Claims: []ai.ClaimVerifyClaimV1{{
				ClaimID:        " id ",
				Text:           longText,
				Classification: "NOPE",
				Verdict:        "NOPE",
				Confidence:     2,
				Reason:         longReason,
				Citations: []ai.ClaimVerifyCitationV1{
					{SourceID: " ", Quote: "x"},
					{SourceID: "s", Quote: " "},
					{SourceID: "s1", Quote: longQuote},
					{SourceID: "s2", Quote: "ok"},
					{SourceID: "s3", Quote: "ok"},
					{SourceID: "s4", Quote: "ok"},
				},
			}},
			Warnings: []string{" ", tooLongWarn, "ok"},
		}},
	})

	item, ok := out["item"]
	if !ok {
		t.Fatalf("expected output for item")
	}
	if len(item.Claims) != 1 {
		t.Fatalf("expected 1 claim, got %#v", item.Claims)
	}
	c := item.Claims[0]
	if c.Classification != "unclear" || c.Verdict != "inconclusive" {
		t.Fatalf("expected defaults for invalid enum values, got %#v", c)
	}
	if c.Confidence != 1 {
		t.Fatalf("expected confidence clamped, got %v", c.Confidence)
	}
	if len(c.Text) != 240 || len(c.Reason) != 240 {
		t.Fatalf("expected text/reason clamped to 240, got len(text)=%d len(reason)=%d", len(c.Text), len(c.Reason))
	}
	if len(c.Citations) != 3 || c.Citations[0].SourceID != "s1" {
		t.Fatalf("unexpected citations normalization: %#v", c.Citations)
	}
	if len(c.Citations[0].Quote) != 200 {
		t.Fatalf("expected quote clamped to 200, got len=%d", len(c.Citations[0].Quote))
	}
	if len(item.Warnings) != 2 || item.Warnings[0] == "" {
		t.Fatalf("unexpected warnings normalization: %#v", item.Warnings)
	}
	if len(item.Warnings[0]) != 160 {
		t.Fatalf("expected warning clamped to 160, got len=%d", len(item.Warnings[0]))
	}
}

func TestParseClaimVerifyBatchOutput(t *testing.T) {
	t.Parallel()

	if _, err := parseClaimVerifyBatchOutput("{"); err == nil {
		t.Fatalf("expected json error")
	}

	parsed, err := parseClaimVerifyBatchOutput(`{"items":[{"item_id":"x","claims":[],"warnings":[]} ]}`)
	if err != nil {
		t.Fatalf("parseClaimVerifyBatchOutput: %v", err)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].ItemID != "x" {
		t.Fatalf("unexpected parsed output: %#v", parsed)
	}
}

