package models

// AIEvidenceRef references an evidence artifact used for an AI request.
type AIEvidenceRef struct {
	_ struct{} `theorydb:"naming:camelCase"`

	Kind        string `theorydb:"attr:kind" json:"kind"`
	Ref         string `theorydb:"attr:ref" json:"ref,omitempty"`
	Hash        string `theorydb:"attr:hash" json:"hash,omitempty"`
	Bytes       int64  `theorydb:"attr:bytes" json:"bytes,omitempty"`
	ContentType string `theorydb:"attr:contentType" json:"content_type,omitempty"`
}

// AIUsage captures provider usage metadata for an AI request.
type AIUsage struct {
	_ struct{} `theorydb:"naming:camelCase"`

	Provider string `theorydb:"attr:provider" json:"provider,omitempty"`
	Model    string `theorydb:"attr:model" json:"model,omitempty"`

	InputTokens  int64 `theorydb:"attr:inputTokens" json:"input_tokens,omitempty"`
	OutputTokens int64 `theorydb:"attr:outputTokens" json:"output_tokens,omitempty"`
	TotalTokens  int64 `theorydb:"attr:totalTokens" json:"total_tokens,omitempty"`

	DurationMs int64 `theorydb:"attr:durationMs" json:"duration_ms,omitempty"`
	ToolCalls  int64 `theorydb:"attr:toolCalls" json:"tool_calls,omitempty"`
}

// AIError captures an error returned by an AI provider call.
type AIError struct {
	_ struct{} `theorydb:"naming:camelCase"`

	Code      string `theorydb:"attr:code" json:"code"`
	Message   string `theorydb:"attr:message" json:"message"`
	Retryable bool   `theorydb:"attr:retryable" json:"retryable,omitempty"`
}
