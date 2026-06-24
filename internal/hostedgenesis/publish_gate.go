package hostedgenesis

import "strings"

// PublishGateInput binds a declaration-ready session to the exact registration,
// conversation, and agent that may proceed to publish/finalize. It is Host
// DynamoDB state, not transport state.
type PublishGateInput struct {
	Status                Status
	RegistrationID        string
	ConversationID        string
	AgentID               string
	DeclarationCheckpoint *DeclarationCheckpoint
}

// CanPublish enforces the fail-closed declaration evidence gate used before
// hosted/off-chain genesis publish/finalize decisions.
func CanPublish(input PublishGateInput) error {
	if NormalizeStatus(string(input.Status)) != StatusDeclarationReady || input.DeclarationCheckpoint == nil {
		return ErrInvalidPublishGate
	}
	if err := input.DeclarationCheckpoint.Validate(); err != nil {
		return ErrInvalidPublishGate
	}
	if strings.TrimSpace(input.RegistrationID) != strings.TrimSpace(input.DeclarationCheckpoint.RegistrationID) ||
		strings.TrimSpace(input.ConversationID) != strings.TrimSpace(input.DeclarationCheckpoint.ConversationID) ||
		!strings.EqualFold(strings.TrimSpace(input.AgentID), strings.TrimSpace(input.DeclarationCheckpoint.AgentID)) {
		return ErrInvalidPublishGate
	}
	return nil
}
