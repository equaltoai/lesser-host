package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulPublicGetDisputes_ReturnsDisputes(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qDispute.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentDispute](t, args, 0)
		*dest = []*models.SoulAgentDispute{
			{
				AgentID:   agentIDHex,
				DisputeID: "dispute-1",
				SignalRef: "rep:signal:abc",
				Evidence:  "s3://bucket/evidence.json",
				Statement: "This is incorrect.",
				Status:    models.SoulDisputeStatusOpen,
				CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			},
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID: "r1",
		Params:    map[string]string{"agentId": agentIDHex},
		Request:   apptheory.Request{Query: map[string][]string{}},
	}

	resp, err := s.handleSoulPublicGetDisputes(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulListDisputesResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Disputes) != 1 {
		t.Fatalf("expected 1 dispute, got %d", len(out.Disputes))
	}
	if out.Disputes[0].DisputeID != "dispute-1" {
		t.Fatalf("expected dispute_id %q, got %q", "dispute-1", out.Disputes[0].DisputeID)
	}
	if string(resp.Body) == "" || jsonContainsAny(resp.Body, "signal_ref", "evidence", "statement", "resolution", "rep:signal:abc", "s3://bucket/evidence.json", "This is incorrect.") {
		t.Fatalf("public dispute list leaked raw evidence fields: %s", string(resp.Body))
	}
}

func TestHandleSoulPublicGetDispute_ReturnsDispute(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qDispute.On("First", mock.AnythingOfType("*models.SoulAgentDispute")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentDispute](t, args, 0)
		*dest = models.SoulAgentDispute{
			AgentID:   agentIDHex,
			DisputeID: "dispute-1",
			SignalRef: "rep:signal:abc",
			Statement: "This is incorrect.",
			Status:    models.SoulDisputeStatusOpen,
			CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID: "r1",
		Params:    map[string]string{"agentId": agentIDHex, "disputeId": "dispute-1"},
		Request:   apptheory.Request{Query: map[string][]string{}},
	}

	resp, err := s.handleSoulPublicGetDispute(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulPublicDispute
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.DisputeID != "dispute-1" {
		t.Fatalf("expected dispute_id %q, got %q", "dispute-1", out.DisputeID)
	}
	if jsonContainsAny(resp.Body, "signal_ref", "evidence", "statement", "resolution", "rep:signal:abc", "This is incorrect.") {
		t.Fatalf("public dispute detail leaked raw evidence fields: %s", string(resp.Body))
	}
}

func jsonContainsAny(body []byte, needles ...string) bool {
	raw := string(body)
	for _, needle := range needles {
		if needle != "" && strings.Contains(raw, needle) {
			return true
		}
	}
	return false
}
