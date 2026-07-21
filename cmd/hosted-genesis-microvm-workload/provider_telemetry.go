package main

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
)

const defaultProviderHeartbeatInterval = 10 * time.Second

// providerCallTelemetry enriches the llm package's content-free SDK events
// with the Hosted Genesis correlation tuple and emits an in-call heartbeat.
// It never receives provider keys, prompt/transcript content, raw deltas,
// declaration bodies, headers, or endpoint tokens.
type providerCallTelemetry struct {
	mu        sync.Mutex
	turn      completion.CompletionTurn
	in        turnInput
	phase     string
	provider  string
	started   time.Time
	lastEvent time.Time
	latest    llm.ProviderTelemetryEvent
}

// turnLifecycleTelemetry records the content-free phase boundaries around one
// accepted Hosted Genesis turn. It is deliberately separate from provider SDK
// sequencing: provider events retain their own exact SDK-event sequence while
// this recorder shows preflight, actor-decision, validation, persistence, and
// durable-failure progress for the same correlation tuple.
type turnLifecycleTelemetry struct {
	mu        sync.Mutex
	turn      completion.CompletionTurn
	in        turnInput
	started   time.Time
	lastEvent time.Time
	sequence  int64
}

func newTurnLifecycleTelemetry(turn completion.CompletionTurn) *turnLifecycleTelemetry {
	now := time.Now()
	return &turnLifecycleTelemetry{turn: turn, started: now, lastEvent: now}
}

func (t *turnLifecycleTelemetry) bind(in turnInput) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.in = in
	t.mu.Unlock()
}

func (t *turnLifecycleTelemetry) emit(phase, eventType, actorAction, failureClass string) {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	t.sequence++
	sequence := t.sequence
	elapsed := now.Sub(t.started).Milliseconds()
	idle := now.Sub(t.lastEvent).Milliseconds()
	t.lastEvent = now
	in := t.in
	t.mu.Unlock()
	provider, model := providerAndModel(in.modelSet)
	slog.Info(serviceName+": turn lifecycle", //nolint:gosec // fixed message plus content-free structured metadata only.
		slog.String("instance_slug", strings.TrimSpace(t.turn.InstanceSlug)),
		slog.String("registration_id", providerRegistrationID(in)),
		slog.String("conversation_id", strings.TrimSpace(t.turn.ConversationID)),
		slog.String("turn_id", strings.TrimSpace(t.turn.TurnID)),
		slog.String("request_id", strings.TrimSpace(t.turn.RequestID)),
		slog.String("correlation_id", providerCorrelationID(in)),
		slog.String("microvm_id", providerMicroVMID(in)),
		slog.String("provider", provider),
		slog.String("model", model),
		slog.String("model_set", strings.TrimSpace(in.modelSet)),
		slog.String("phase", strings.TrimSpace(phase)),
		slog.String("event_type", strings.TrimSpace(eventType)),
		slog.Int64("sequence", sequence),
		slog.Int64("elapsed_ms", elapsed),
		slog.Int64("idle_ms", idle),
		slog.String("actor_action", strings.TrimSpace(actorAction)),
		slog.String("failure_class", strings.TrimSpace(failureClass)),
	)
}

func newProviderCallTelemetry(turn completion.CompletionTurn, in turnInput, phase string) *providerCallTelemetry {
	now := time.Now()
	provider := strings.ToLower(strings.TrimSpace(strings.SplitN(in.modelSet, ":", 2)[0]))
	return &providerCallTelemetry{
		turn:      turn,
		in:        in,
		phase:     strings.TrimSpace(phase),
		provider:  provider,
		started:   now,
		lastEvent: now,
	}
}

func (t *providerCallTelemetry) observe(event llm.ProviderTelemetryEvent) {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	t.latest = event
	t.lastEvent = now
	t.mu.Unlock()
	t.log("provider_sdk_event", event, now)
}

func (t *providerCallTelemetry) heartbeat() {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	event := t.latest
	event.Provider = firstNonEmpty(strings.TrimSpace(event.Provider), t.provider)
	event.Phase = firstNonEmpty(strings.TrimSpace(event.Phase), t.phase)
	event.EventType = "heartbeat"
	event.FirstEvent = false
	event.LastEvent = false
	event.ElapsedMS = now.Sub(t.started).Milliseconds()
	event.IdleMS = now.Sub(t.lastEvent).Milliseconds()
	lastEvent := t.lastEvent
	t.mu.Unlock()
	t.log("provider_call_heartbeat", event, lastEvent)
}

func (t *providerCallTelemetry) startHeartbeat(ctx context.Context, interval time.Duration) func() {
	if t == nil || interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				t.heartbeat()
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

func (t *providerCallTelemetry) log(message string, event llm.ProviderTelemetryEvent, lastSDKEventAt time.Time) {
	if t == nil {
		return
	}
	attrs := []any{
		slog.String("instance_slug", strings.TrimSpace(t.turn.InstanceSlug)),
		slog.String("registration_id", providerRegistrationID(t.in)),
		slog.String("conversation_id", strings.TrimSpace(t.turn.ConversationID)),
		slog.String("turn_id", strings.TrimSpace(t.turn.TurnID)),
		slog.String("request_id", strings.TrimSpace(t.turn.RequestID)),
		slog.String("correlation_id", providerCorrelationID(t.in)),
		slog.String("microvm_id", providerMicroVMID(t.in)),
		slog.String("provider", firstNonEmpty(strings.TrimSpace(event.Provider), t.provider)),
		slog.String("model", strings.TrimSpace(event.Model)),
		slog.String("phase", firstNonEmpty(strings.TrimSpace(event.Phase), t.phase)),
		slog.String("event_type", strings.TrimSpace(event.EventType)),
		slog.Int64("sequence", event.Sequence),
		slog.Bool("first_event", event.FirstEvent),
		slog.Bool("first_sdk_event", event.FirstSDKEvent),
		slog.Bool("last_event", event.LastEvent),
		slog.String("last_sdk_event_at", lastSDKEventAt.UTC().Format(time.RFC3339Nano)),
		slog.Int64("elapsed_ms", event.ElapsedMS),
		slog.Int64("idle_ms", event.IdleMS),
		slog.Int("delta_bytes", event.DeltaBytes),
		slog.Int("delta_runes", event.DeltaRunes),
		slog.Int("output_bytes", event.OutputBytes),
		slog.Int("output_runes", event.OutputRunes),
		slog.String("output_sha256", strings.TrimSpace(event.OutputSHA256)),
		slog.Int("payload_bytes", event.PayloadBytes),
		slog.String("payload_sha256", strings.TrimSpace(event.PayloadSHA256)),
		slog.Int64("input_tokens", event.InputTokens),
		slog.Int64("output_tokens", event.OutputTokens),
		slog.Int64("total_tokens", event.TotalTokens),
		slog.Int64("tool_calls", event.ToolCalls),
		slog.Int64("output_count", event.OutputCount),
		slog.String("stop_reason", strings.TrimSpace(event.StopReason)),
		slog.String("failure_class", strings.TrimSpace(event.FailureClass)),
		slog.String("schema_name", strings.TrimSpace(event.SchemaName)),
		slog.String("tool_name", strings.TrimSpace(event.ToolName)),
	}
	slog.Info(serviceName+": "+message, attrs...) //nolint:gosec // fixed message plus content-free structured metadata only.
}

func providerAndModel(modelSet string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(modelSet), ":", 2)
	provider := strings.ToLower(strings.TrimSpace(parts[0]))
	if len(parts) == 1 {
		return provider, ""
	}
	return provider, strings.TrimSpace(parts[1])
}

func providerCorrelationID(in turnInput) string {
	if in.session != nil && in.session.TraceIDs != nil {
		return strings.TrimSpace(in.session.TraceIDs.CorrelationID)
	}
	if in.conv != nil {
		return strings.TrimSpace(in.conv.CorrelationID)
	}
	return ""
}

func providerRegistrationID(in turnInput) string {
	if in.session == nil {
		return ""
	}
	return strings.TrimSpace(in.session.RegistrationID)
}

func providerMicroVMID(in turnInput) string {
	if in.session == nil || in.session.MicroVMLifecycleRef == nil {
		return ""
	}
	return strings.TrimSpace(in.session.MicroVMLifecycleRef.MicroVMID)
}
