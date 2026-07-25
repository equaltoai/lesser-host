package hostedgenesis

import (
	"testing"
	"time"
)

func TestDefaultExecutionEnvelopeRetainsBoundedMargins(t *testing.T) {
	t.Parallel()

	envelope := DefaultExecutionEnvelope()
	if err := envelope.Validate(); err != nil {
		t.Fatalf("default execution envelope should validate: %v", err)
	}
	if envelope.ProviderCallTimeout <= 15*time.Minute {
		t.Fatalf("provider call timeout %v must exceed rejected 900-second ceiling", envelope.ProviderCallTimeout)
	}
	if envelope.ProviderHTTPTimeout < envelope.ProviderCallTimeout {
		t.Fatalf("provider HTTP timeout %v aborts before whole-call timeout %v", envelope.ProviderHTTPTimeout, envelope.ProviderCallTimeout)
	}
	if got := envelope.WorkloadExecutionTimeout - envelope.ProviderCallTimeout; got < envelope.TerminalPersistenceMargin {
		t.Fatalf("terminal persistence margin %v is smaller than configured %v", got, envelope.TerminalPersistenceMargin)
	}
	if got := envelope.MicroVMMaximumDuration - envelope.WorkloadExecutionTimeout; got < envelope.RuntimeCleanupMargin {
		t.Fatalf("runtime cleanup margin %v is smaller than configured %v", got, envelope.RuntimeCleanupMargin)
	}
}

func TestExecutionEnvelopeRejectsIncoherentDeadlines(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*ExecutionEnvelope){
		"HTTP attempt shorter than whole call": func(e *ExecutionEnvelope) { e.ProviderHTTPTimeout = e.ProviderCallTimeout - time.Second },
		"HTTP attempt outside workload bound":  func(e *ExecutionEnvelope) { e.ProviderHTTPTimeout = e.WorkloadExecutionTimeout + time.Second },
		"terminal persistence margin missing":  func(e *ExecutionEnvelope) { e.WorkloadExecutionTimeout = e.ProviderCallTimeout },
		"cleanup margin missing":               func(e *ExecutionEnvelope) { e.MicroVMMaximumDuration = e.WorkloadExecutionTimeout },
		"above AWS maximum":                    func(e *ExecutionEnvelope) { e.MicroVMMaximumDuration = AWSLambdaMicroVMMaximumDuration + time.Second },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			envelope := DefaultExecutionEnvelope()
			mutate(&envelope)
			if err := envelope.Validate(); err == nil {
				t.Fatal("expected incoherent envelope to fail validation")
			}
		})
	}
}
