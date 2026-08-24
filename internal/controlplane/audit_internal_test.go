package controlplane

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
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

func TestTryWriteAuditLogWithContextSurfacesCreateError(t *testing.T) {
	db := ttmocks.NewMockExtendedDB()
	qAudit := new(ttmocks.MockQuery)
	createErr := errors.New("boom")

	db.On("WithContext", mock.Anything).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Once()
	qAudit.On("Create").Return(createErr).Once()

	s := &Server{store: store.New(db)}
	entry := &models.AuditLogEntry{
		Actor:     "alice",
		Action:    "operator.fleet.remediate_mcp_drift",
		Target:    "fleet:mcp-remediation:count=1:hash=0123456789abcdef",
		RequestID: "rid-create-error",
		CreatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}

	var buf bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		if previousOutput != nil {
			log.SetOutput(previousOutput)
			return
		}
		log.SetOutput(os.Stderr)
	})

	persisted := s.tryWriteAuditLogWithContext(context.Background(), entry)
	if persisted {
		t.Fatalf("expected audit_persisted=false for Create error")
	}

	line := buf.String()
	for _, want := range []string{
		"audit_persisted=false",
		`action="operator.fleet.remediate_mcp_drift"`,
		`actor="alice"`,
		`target="fleet:mcp-remediation:count=1:hash=0123456789abcdef"`,
		`request_id="rid-create-error"`,
		"boom",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected log to contain %q, got %q", want, line)
		}
	}
}
