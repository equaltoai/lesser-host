package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulPublicGetContinuity_DualReadsLegacyReferencesAsArray(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qContinuity.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentContinuity](t, args, 0)
		*dest = []*models.SoulAgentContinuity{
			{
				AgentID:        agentIDHex,
				Type:           models.SoulContinuityEntryTypeModelChange,
				Summary:        "Updated model.",
				ReferencesJSON: `["boundary-001","boundary-002"]`,
				Timestamp:      time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			},
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID: "r1",
		Params:    map[string]string{"agentId": agentIDHex},
		Request:   apptheory.Request{Query: map[string][]string{}},
	}

	resp, err := s.handleSoulPublicGetContinuity(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulListContinuityResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if strings.TrimSpace(out.Version) != "2" {
		t.Fatalf("expected version 2, got %q", out.Version)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out.Entries))
	}
	if got := out.Entries[0].ReferencesV2; len(got) != 2 || got[0] != "boundary-001" || got[1] != "boundary-002" {
		t.Fatalf("expected typed references, got %#v", got)
	}
}
