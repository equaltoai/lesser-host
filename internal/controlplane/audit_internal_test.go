package controlplane

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestApplyAuditSourceProvenanceUsesProviderContext(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{
		Headers: map[string][]string{
			"x-forwarded-for": {"203.0.113.250"},
		},
		SourceProvenance: apptheory.SourceProvenance{
			SourceIP: "198.51.100.77",
			Provider: "lambda-url",
			Source:   "provider_request_context",
			Valid:    true,
		},
	}}
	entry := &models.AuditLogEntry{}

	applyAuditSourceProvenance(ctx, entry)

	if !entry.SourceValid || entry.SourceIP != "198.51.100.77" || entry.SourceProvider != "lambda-url" || entry.SourceProvenance != "provider_request_context" {
		t.Fatalf("unexpected audit source fields: %#v", entry)
	}
	if entry.SourceIP == "203.0.113.250" {
		t.Fatalf("forwarded header was trusted: %#v", entry)
	}
}

func TestApplyAuditSourceProvenanceUnknownForHeaderOnlyContext(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
		"x-forwarded-for": {"198.51.100.42"},
	}}}
	entry := &models.AuditLogEntry{}

	applyAuditSourceProvenance(ctx, entry)

	if entry.SourceValid || entry.SourceIP != "" || entry.SourceProvider != commMetricUnknown || entry.SourceProvenance != commMetricUnknown {
		t.Fatalf("expected unknown source fields, got %#v", entry)
	}
}
