package trust

import (
	"context"
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/controlplane"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type fakeSoulRegistrationUpdater struct {
	instanceSlug string
	requestID    string
	agentID      string
	body         []byte
	result       *controlplane.SoulAgentUpdateRegistrationResult
	appErr       *apptheory.AppError
}

func (f *fakeSoulRegistrationUpdater) UpdateSoulAgentRegistrationForInstance(
	_ context.Context,
	instanceSlug string,
	requestID string,
	agentID string,
	body []byte,
) (*controlplane.SoulAgentUpdateRegistrationResult, *apptheory.AppError) {
	f.instanceSlug = instanceSlug
	f.requestID = requestID
	f.agentID = agentID
	f.body = append([]byte(nil), body...)
	return f.result, f.appErr
}

func TestHandleSoulAgentUpdateRegistration_RequiresInstanceAuth(t *testing.T) {
	t.Parallel()

	s := &Server{soul: &fakeSoulRegistrationUpdater{}}
	_, err := s.handleSoulAgentUpdateRegistration(&apptheory.Context{})
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.unauthorized" || appErr.Message != "unauthorized" {
		t.Fatalf("expected unauthorized app error, got %v", err)
	}
}

func TestHandleSoulAgentUpdateRegistration_ForwardsToSharedUpdater(t *testing.T) {
	t.Parallel()

	updater := &fakeSoulRegistrationUpdater{
		result: &controlplane.SoulAgentUpdateRegistrationResult{
			Agent:   models.SoulAgentIdentity{AgentID: "0xabc"},
			Version: 4,
		},
	}
	s := &Server{soul: updater}
	body := []byte(`{"registration":{"version":"3"}}`)
	ctx := &apptheory.Context{
		AuthIdentity: " inst-1 ",
		RequestID:    "rid-1",
		Params:       map[string]string{"agentId": "0xabc"},
		Request:      apptheory.Request{Body: body},
	}

	resp, err := s.handleSoulAgentUpdateRegistration(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	if updater.instanceSlug != "inst-1" || updater.requestID != "rid-1" || updater.agentID != "0xabc" {
		t.Fatalf("unexpected forwarded args: slug=%q request=%q agent=%q", updater.instanceSlug, updater.requestID, updater.agentID)
	}
	if string(updater.body) != string(body) {
		t.Fatalf("expected forwarded body %q, got %q", string(body), string(updater.body))
	}

	var out controlplane.SoulAgentUpdateRegistrationResult
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Version != 4 || out.Agent.AgentID != "0xabc" {
		t.Fatalf("unexpected response payload: %+v", out)
	}
}
