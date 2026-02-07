package trust

import (
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestPublishJobRendersHelpers(t *testing.T) {
	t.Parallel()

	if got := normalizeRenderPolicy(" always "); got != renderPolicyAlways {
		t.Fatalf("unexpected policy: %q", got)
	}
	if got := normalizeRenderPolicy("bad"); got != renderPolicySuspicious {
		t.Fatalf("expected default suspicious, got %q", got)
	}

	if shouldRenderLink(renderPolicyAlways, riskLow) != true {
		t.Fatalf("expected always renders low")
	}
	if shouldRenderLink(renderPolicySuspicious, riskLow) != false {
		t.Fatalf("expected suspicious skips low")
	}
	if shouldRenderLink(renderPolicySuspicious, riskMedium) != true {
		t.Fatalf("expected suspicious renders medium")
	}
	if shouldRenderLink(renderPolicyAlways, statusInvalid) {
		t.Fatalf("expected never render invalid")
	}
	if shouldRenderLink(renderPolicyAlways, statusBlocked) {
		t.Fatalf("expected never render blocked")
	}

	if retentionClassForRisk(riskHigh) != models.RenderRetentionClassEvidence {
		t.Fatalf("expected evidence for high risk")
	}
	if retentionClassForRisk(riskLow) != models.RenderRetentionClassBenign {
		t.Fatalf("expected benign for low risk")
	}
}

func TestSetMissingLinkRenderStatuses(t *testing.T) {
	t.Parallel()

	out := &linkRenderResult{
		Links:   []linkRenderLinkResult{{Status: statusQueued}, {Status: statusQueued}},
		Summary: linkRenderSummary{TotalLinks: 2},
	}
	setMissingLinkRenderStatuses(out, []missingRenderRequest{{Index: 1}}, statusError)
	if out.Links[1].Status != statusError {
		t.Fatalf("unexpected status: %#v", out.Links)
	}
}

func TestBuildLinkRenderResult_OfflineDecisions(t *testing.T) {
	t.Parallel()

	s := &Server{store: nil}
	ctx := &apptheory.Context{}
	now := time.Now().UTC()

	out, missing := s.buildLinkRenderResult(ctx, now, renderPolicySuspicious, "inst", []string{
		"not a url",
		"http://127.0.0.1/",
		"https://example.com/",
	})

	if len(out.Links) != 3 {
		t.Fatalf("expected 3 links, got %#v", out.Links)
	}
	if out.Summary.TotalLinks != 3 {
		t.Fatalf("unexpected summary: %#v", out.Summary)
	}
	// With suspicious policy and a low-risk public URL, we should not attempt to render.
	if len(missing) != 0 {
		t.Fatalf("expected no missing renders, got %#v", missing)
	}
}
