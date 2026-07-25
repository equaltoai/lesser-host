package main

import (
	"testing"
	"time"
)

func TestLoadExecutionEnvelopeAllowsLongFiveBodyCallWithMargins(t *testing.T) {
	for _, key := range []string{
		envProviderHTTPTimeoutSeconds,
		envProviderCallTimeoutSeconds,
		envWorkloadExecutionTimeoutSeconds,
		envMicroVMMaximumDurationSeconds,
		envTerminalPersistenceMarginSeconds,
		envRuntimeCleanupMarginSeconds,
	} {
		t.Setenv(key, "")
	}

	envelope, err := loadExecutionEnvelope()
	if err != nil {
		t.Fatalf("load default execution envelope: %v", err)
	}
	if envelope.ProviderCallTimeout <= 15*time.Minute {
		t.Fatalf("provider call %v cannot support legitimate work beyond 900 seconds", envelope.ProviderCallTimeout)
	}
	runner := &turnRunner{
		providerCallTimeout:      envelope.ProviderCallTimeout,
		workloadExecutionTimeout: envelope.WorkloadExecutionTimeout,
	}
	if runner.providerTimeout() != envelope.ProviderCallTimeout {
		t.Fatalf("runner whole-call timeout drifted: got %v want %v", runner.providerTimeout(), envelope.ProviderCallTimeout)
	}
	if runner.workloadTimeout() != envelope.WorkloadExecutionTimeout {
		t.Fatalf("runner workload timeout drifted: got %v want %v", runner.workloadTimeout(), envelope.WorkloadExecutionTimeout)
	}
}

func TestLoadExecutionEnvelopeFailsClosedWhenHTTPWouldAbortFirst(t *testing.T) {
	t.Setenv(envProviderHTTPTimeoutSeconds, "901")
	t.Setenv(envProviderCallTimeoutSeconds, "902")
	if _, err := loadExecutionEnvelope(); err == nil {
		t.Fatal("expected incoherent provider HTTP and whole-call timeouts to fail")
	}
}

func TestLoadExecutionEnvelopeRejectsDurationOutsidePlatformBound(t *testing.T) {
	t.Setenv(envProviderHTTPTimeoutSeconds, "28801")
	if _, err := loadExecutionEnvelope(); err == nil {
		t.Fatal("expected duration above the documented platform maximum to fail")
	}
}
