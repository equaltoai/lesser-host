package hostedgenesis

// QueueMessage is the non-authoritative hosted/off-chain soul genesis recovery
// command shape. Project 51 M4 demotes this SQS payload to janitor, telemetry,
// backfill, and operator recovery use only: user-visible progress, retry
// guidance, and finalize decisions must come from HostedGenesisSession plus
// AppTheory MicroVM execution/cache state. It carries only ids, hashes, and
// bounded client-safe correlation values; raw credentials and raw transcript
// text stay out of queue payloads.
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
