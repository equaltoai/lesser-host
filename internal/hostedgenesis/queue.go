package hostedgenesis

// QueueMessage is the durable async worker command for hosted/off-chain soul
// genesis. It carries only ids, hashes, and bounded client-safe correlation
// values; raw credentials and raw transcript text stay in Host's store.
type QueueMessage struct {
	Kind           string `json:"kind"`
	Step           string `json:"step"`
	RegistrationID string `json:"registration_id"`
	InstanceSlug   string `json:"instance_slug"`
	AgentID        string `json:"agent_id"`
	ConversationID string `json:"conversation_id"`
	TurnID         string `json:"turn_id,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

const (
	QueueMessageKind          = "hosted_genesis_conversation"
	StepAssistantTurn         = "assistant_turn"
	StepDeclarationExtraction = "declaration_extraction"
)
