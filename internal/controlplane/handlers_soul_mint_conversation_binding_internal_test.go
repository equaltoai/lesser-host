package controlplane

import (
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestHostedGenesisRouteBindingHydratesLegacyBlankSessionFields(t *testing.T) {
	t.Parallel()

	reg := mintConversationHandleReg()
	inst := models.Instance{Slug: soulInstanceBootstrapTestInstanceSlug}
	session := &models.HostedGenesisSession{
		ConversationID: mintConversationTestConversationID,
		Status:         string(hostedgenesis.StatusAssistantTurnReady),
		Model:          "anthropic:claude-sonnet-4-6",
		CreatedAt:      time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
	}

	appErr := requireHostedGenesisMicroVMBindingReady(mintConversationRegistrationContext{
		reg:        &reg,
		inst:       &inst,
		agentIDHex: reg.AgentID,
	}, session, inst.Slug, mintConversationTestConversationID)
	if appErr != nil {
		t.Fatalf("expected route-scoped binding repair, got %#v", appErr)
	}
	if session.InstanceSlug != soulInstanceBootstrapTestInstanceSlug ||
		session.RegistrationID != reg.ID ||
		session.AgentID != reg.AgentID ||
		session.ConversationID != mintConversationTestConversationID {
		t.Fatalf("session binding was not hydrated from route context: %#v", session)
	}
	if err := session.MicroVMSessionBinding().Validate(); err != nil {
		t.Fatalf("expected hydrated binding to validate: %v", err)
	}
}

func TestHostedGenesisRouteBindingRejectsMismatchedSessionFields(t *testing.T) {
	t.Parallel()

	reg := mintConversationHandleReg()
	session := &models.HostedGenesisSession{
		InstanceSlug:   "other",
		RegistrationID: reg.ID,
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         string(hostedgenesis.StatusAssistantTurnReady),
	}

	appErr := requireHostedGenesisMicroVMBindingReady(mintConversationRegistrationContext{
		reg:        &reg,
		inst:       &models.Instance{Slug: soulInstanceBootstrapTestInstanceSlug},
		agentIDHex: reg.AgentID,
	}, session, soulInstanceBootstrapTestInstanceSlug, mintConversationTestConversationID)
	if appErr == nil || appErr.Code != appTheoryCodeConflict {
		t.Fatalf("expected binding mismatch conflict, got %#v", appErr)
	}
}
