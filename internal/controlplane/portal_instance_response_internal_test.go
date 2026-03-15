package controlplane

import (
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestApplyDerivedManagedUpdateSummarySetsPerKindFields(t *testing.T) {
	now := time.Date(2026, 3, 13, 18, 30, 0, 0, time.UTC)
	resp := instanceResponse{}

	applyDerivedManagedUpdateSummary(&resp, &models.UpdateJob{
		ID:        " lesser-job ",
		Status:    " ok ",
		UpdatedAt: now,
	}, updateJobKindLesser)
	applyDerivedManagedUpdateSummary(&resp, &models.UpdateJob{
		ID:        " body-job ",
		Status:    " running ",
		UpdatedAt: now.Add(2 * time.Minute),
	}, updateJobKindBody)
	applyDerivedManagedUpdateSummary(&resp, &models.UpdateJob{
		ID:        " mcp-job ",
		Status:    " error ",
		UpdatedAt: now.Add(1 * time.Minute),
	}, updateJobKindMCP)

	if got := resp.LesserUpdateJobID; got != "lesser-job" {
		t.Fatalf("LesserUpdateJobID = %q, want lesser-job", got)
	}
	if got := resp.LesserUpdateStatus; got != "ok" {
		t.Fatalf("LesserUpdateStatus = %q, want ok", got)
	}
	if got := resp.LesserBodyUpdateJobID; got != "body-job" {
		t.Fatalf("LesserBodyUpdateJobID = %q, want body-job", got)
	}
	if got := resp.LesserBodyUpdateStatus; got != "running" {
		t.Fatalf("LesserBodyUpdateStatus = %q, want running", got)
	}
	if got := resp.MCPUpdateJobID; got != "mcp-job" {
		t.Fatalf("MCPUpdateJobID = %q, want mcp-job", got)
	}
	if got := resp.MCPUpdateStatus; got != "error" {
		t.Fatalf("MCPUpdateStatus = %q, want error", got)
	}
	if !resp.UpdatedAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("UpdatedAt = %s, want %s", resp.UpdatedAt, now.Add(2*time.Minute))
	}
}

func TestApplyDerivedManagedUpdateSummaryPreservesExistingFields(t *testing.T) {
	now := time.Date(2026, 3, 13, 18, 35, 0, 0, time.UTC)
	existing := now.Add(5 * time.Minute)
	resp := instanceResponse{
		LesserBodyUpdateStatus: "queued",
		LesserBodyUpdateJobID:  "existing-body-job",
		LesserBodyUpdateAt:     existing,
		UpdatedAt:              existing,
	}

	applyDerivedManagedUpdateSummary(&resp, &models.UpdateJob{
		ID:        "new-body-job",
		Status:    "ok",
		UpdatedAt: now,
	}, updateJobKindBody)

	if got := resp.LesserBodyUpdateStatus; got != "queued" {
		t.Fatalf("LesserBodyUpdateStatus = %q, want queued", got)
	}
	if got := resp.LesserBodyUpdateJobID; got != "existing-body-job" {
		t.Fatalf("LesserBodyUpdateJobID = %q, want existing-body-job", got)
	}
	if !resp.LesserBodyUpdateAt.Equal(existing) {
		t.Fatalf("LesserBodyUpdateAt = %s, want %s", resp.LesserBodyUpdateAt, existing)
	}
	if !resp.UpdatedAt.Equal(existing) {
		t.Fatalf("UpdatedAt = %s, want %s", resp.UpdatedAt, existing)
	}
}

func TestSanitizePortalURL(t *testing.T) {
	const fallback = "https://lab.lesser.host"

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty uses fallback", raw: "", want: fallback},
		{name: "http upgraded", raw: "http://lab.lesser.host", want: "https://lab.lesser.host"},
		{name: "lambda url rejected", raw: "https://abc.lambda-url.us-east-1.on.aws", want: fallback},
		{name: "api gateway rejected", raw: "https://abc.execute-api.us-east-1.amazonaws.com/", want: fallback},
		{name: "first party kept", raw: "https://lab.lesser.host", want: "https://lab.lesser.host"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizePortalURL(tc.raw, fallback); got != tc.want {
				t.Fatalf("sanitizePortalURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
