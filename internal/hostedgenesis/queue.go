package hostedgenesis

// QueueMessage is the non-authoritative hosted/off-chain soul genesis recovery
// command shape. HostedGenesisSession remains the business source of truth:
// user-visible progress, retry guidance, and finalize decisions must come from
// HostedGenesisSession plus AppTheory MicroVM execution/cache state. SQS is only
// durable transport for worker/backfill/recovery commands; every consumer must
// reload and validate session truth before acting. It carries only ids, hashes,
// and bounded client-safe correlation values; raw credentials and raw transcript
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
	QueueMessageKind = "hosted_genesis_conversation"
	// StepMicroVMDispatch asks a worker to dispatch the already-accepted turn
	// through the AppTheory MicroVM controller. It is non-authoritative transport:
	// the worker must reload HostedGenesisSession and drop the message unless the
	// session/turn/idempotency still match.
	StepMicroVMDispatch       = "microvm_dispatch"
	StepAssistantTurn         = "assistant_turn"
	StepDeclarationExtraction = "declaration_extraction"
)
