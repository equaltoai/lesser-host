package trust

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestApplyAuditSourceProvenanceUsesProviderContext(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{
		Headers: map[string][]string{
			"x-forwarded-for": {"203.0.113.250"},
		},
		SourceProvenance: apptheory.SourceProvenance{
			SourceIP: "198.51.100.88",
			Provider: "lambda-url",
			Source:   "provider_request_context",
			Valid:    true,
		},
	}}
	entry := &models.AuditLogEntry{}

	applyAuditSourceProvenance(ctx, entry)

	if !entry.SourceValid || entry.SourceIP != "198.51.100.88" || entry.SourceProvider != "lambda-url" || entry.SourceProvenance != "provider_request_context" {
		t.Fatalf("unexpected audit source fields: %#v", entry)
	}
	if entry.SourceIP == "203.0.113.250" {
		t.Fatalf("forwarded header was trusted: %#v", entry)
	}
}
