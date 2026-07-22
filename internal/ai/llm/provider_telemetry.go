package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

// ProviderTelemetryEvent is the content-free observation emitted at provider
// call boundaries and for every SDK stream event. It deliberately carries only
// bounded metadata: raw prompts, transcripts, deltas, model output, declaration
// bodies, provider keys, and request headers never cross this seam.
type ProviderTelemetryEvent struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Phase         string `json:"phase"`
	EventType     string `json:"event_type"`
	Sequence      int64  `json:"sequence"`
	FirstEvent    bool   `json:"first_event"`
	FirstSDKEvent bool   `json:"first_sdk_event"`
	LastEvent     bool   `json:"last_event"`
	ElapsedMS     int64  `json:"elapsed_ms"`
	IdleMS        int64  `json:"idle_ms"`
	DeltaBytes    int    `json:"delta_bytes,omitempty"`
	DeltaRunes    int    `json:"delta_runes,omitempty"`
	OutputBytes   int    `json:"output_bytes,omitempty"`
	OutputRunes   int    `json:"output_runes,omitempty"`
	OutputSHA256  string `json:"output_sha256,omitempty"`
	PayloadBytes  int    `json:"payload_bytes,omitempty"`
	PayloadSHA256 string `json:"payload_sha256,omitempty"`
	InputTokens   int64  `json:"input_tokens,omitempty"`
	OutputTokens  int64  `json:"output_tokens,omitempty"`
	TotalTokens   int64  `json:"total_tokens,omitempty"`
	ToolCalls     int64  `json:"tool_calls,omitempty"`
	OutputCount   int64  `json:"output_count,omitempty"`
	StopReason    string `json:"stop_reason,omitempty"`
	FailureClass  string `json:"failure_class,omitempty"`
	SchemaName    string `json:"schema_name,omitempty"`
	ToolName      string `json:"tool_name,omitempty"`
}

// ProviderTelemetrySink receives content-free provider observations.
type ProviderTelemetrySink func(ProviderTelemetryEvent)

type providerTelemetryRecorder struct {
	provider  string
	model     string
	phase     string
	sink      ProviderTelemetrySink
	started   time.Time
	last      time.Time
	sequence  int64
	sdkEvents int64
}

type providerClassifiedError struct {
	class string
	err   error
}

func (e *providerClassifiedError) Error() string { return e.err.Error() }
func (e *providerClassifiedError) Unwrap() error { return e.err }

func withProviderFailureClass(err error, class string) error {
	if err == nil {
		return nil
	}
	return &providerClassifiedError{class: string(hostedgenesis.NormalizeFailureClass(class)), err: err}
}

func (r *providerTelemetryRecorder) emitSDK(event ProviderTelemetryEvent) {
	if r == nil {
		return
	}
	r.sdkEvents++
	event.FirstSDKEvent = r.sdkEvents == 1
	r.emit(event)
}

func newProviderTelemetryRecorder(provider, model, phase string, sink ProviderTelemetrySink) *providerTelemetryRecorder {
	now := time.Now()
	return &providerTelemetryRecorder{
		provider: strings.TrimSpace(provider),
		model:    strings.TrimSpace(model),
		phase:    strings.TrimSpace(phase),
		sink:     sink,
		started:  now,
		last:     now,
	}
}

func (r *providerTelemetryRecorder) emit(event ProviderTelemetryEvent) {
	if r == nil || r.sink == nil {
		return
	}
	now := time.Now()
	r.sequence++
	event.Provider = r.provider
	event.Model = r.model
	event.Phase = r.phase
	event.Sequence = r.sequence
	event.FirstEvent = r.sequence == 1
	event.ElapsedMS = now.Sub(r.started).Milliseconds()
	event.IdleMS = now.Sub(r.last).Milliseconds()
	r.last = now
	r.sink(event)
}

func providerOutputMetadata(raw string) (bytes int, runes int, digest string) {
	bytes = len(raw)
	runes = utf8.RuneCountInString(raw)
	sum := sha256.Sum256([]byte(raw))
	return bytes, runes, hex.EncodeToString(sum[:])
}

func providerPayloadMetadata(raw []byte) (bytes int, digest string) {
	sum := sha256.Sum256(raw)
	return len(raw), hex.EncodeToString(sum[:])
}

// ProviderFailureClass maps an error to a stable, content-free class. Provider
// error strings are intentionally not exposed because SDK errors can echo
// request or response material.
func ProviderFailureClass(err error) string {
	var classified *providerClassifiedError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &classified):
		return classified.class
	case errors.Is(err, context.DeadlineExceeded):
		return string(hostedgenesis.FailureClassProviderTimeout)
	case errors.Is(err, context.Canceled):
		return string(hostedgenesis.FailureClassProviderCanceled)
	default:
		return string(hostedgenesis.FailureClassProviderAPIFailure)
	}
}
