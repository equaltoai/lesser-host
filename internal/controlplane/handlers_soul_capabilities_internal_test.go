package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

func TestHandleSoulPublicGetCapabilities_ReturnsObjectTypedConstraints(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	packs := &fakeSoulPackStore{}
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}

	agentIDHex := soulLifecycleTestAgentIDHex
	s3Key := soulRegistrationS3Key(agentIDHex)

	regBody, _ := json.Marshal(map[string]any{
		"version": "2",
		"capabilities": []any{
			map[string]any{
				"capability":  "summarization",
				"scope":       "public",
				"constraints": map[string]any{"maxTokens": 1234},
				"claimLevel":  "self-declared",
			},
		},
	})
	packs.objects = map[string]fakePut{
		s3Key: {key: s3Key, body: regBody},
	}

	ctx := &apptheory.Context{
		RequestID: "r1",
		Params:    map[string]string{"agentId": agentIDHex},
	}
	resp, err := s.handleSoulPublicGetCapabilities(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulListCapabilitiesResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Version != "2" {
		t.Fatalf("expected version 2, got %q", out.Version)
	}
	if len(out.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(out.Capabilities))
	}
	if out.Capabilities[0].Constraints == nil {
		t.Fatalf("expected constraints object, got nil")
	}
	if out.Capabilities[0].Constraints["maxTokens"] == nil {
		t.Fatalf("expected maxTokens constraint, got %#v", out.Capabilities[0].Constraints)
	}
}
