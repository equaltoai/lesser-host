package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

const (
	envProviderHTTPTimeoutSeconds       = "HOSTED_GENESIS_PROVIDER_HTTP_TIMEOUT_SECONDS"
	envProviderCallTimeoutSeconds       = "HOSTED_GENESIS_PROVIDER_CALL_TIMEOUT_SECONDS"
	envWorkloadExecutionTimeoutSeconds  = "HOSTED_GENESIS_WORKLOAD_EXECUTION_TIMEOUT_SECONDS"
	envMicroVMMaximumDurationSeconds    = "HOSTED_GENESIS_MICROVM_MAXIMUM_DURATION_SECONDS"
	envTerminalPersistenceMarginSeconds = "HOSTED_GENESIS_TERMINAL_PERSISTENCE_MARGIN_SECONDS"
	envRuntimeCleanupMarginSeconds      = "HOSTED_GENESIS_RUNTIME_CLEANUP_MARGIN_SECONDS"
)

func loadExecutionEnvelope() (hostedgenesis.ExecutionEnvelope, error) {
	defaults := hostedgenesis.DefaultExecutionEnvelope()
	envelope := hostedgenesis.ExecutionEnvelope{
		ProviderHTTPTimeout:       durationSecondsFromEnv(envProviderHTTPTimeoutSeconds, defaults.ProviderHTTPTimeout),
		ProviderCallTimeout:       durationSecondsFromEnv(envProviderCallTimeoutSeconds, defaults.ProviderCallTimeout),
		WorkloadExecutionTimeout:  durationSecondsFromEnv(envWorkloadExecutionTimeoutSeconds, defaults.WorkloadExecutionTimeout),
		MicroVMMaximumDuration:    durationSecondsFromEnv(envMicroVMMaximumDurationSeconds, defaults.MicroVMMaximumDuration),
		TerminalPersistenceMargin: durationSecondsFromEnv(envTerminalPersistenceMarginSeconds, defaults.TerminalPersistenceMargin),
		RuntimeCleanupMargin:      durationSecondsFromEnv(envRuntimeCleanupMarginSeconds, defaults.RuntimeCleanupMargin),
	}
	if err := envelope.Validate(); err != nil {
		return hostedgenesis.ExecutionEnvelope{}, fmt.Errorf("validate hosted genesis execution envelope: %w", err)
	}
	return envelope, nil
}

func durationSecondsFromEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 || seconds > int64(hostedgenesis.AWSLambdaMicroVMMaximumDuration/time.Second) {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
