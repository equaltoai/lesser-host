package hostedgenesis

import (
	"errors"
	"time"
)

const (
	// AWSLambdaMicroVMMaximumDuration is the documented Lambda MicroVM Run
	// maximum. Host uses the full bounded platform envelope for five-body work
	// instead of imposing another short orchestration guess.
	AWSLambdaMicroVMMaximumDuration = 8 * time.Hour

	// DefaultProviderCallTimeout bounds the complete provider retry/streaming
	// lifecycle. It deliberately exceeds the rejected 900-second ceiling while
	// retaining a guarded persistence and runtime-cleanup margin below the AWS
	// MicroVM maximum.
	DefaultProviderCallTimeout = 7*time.Hour + 45*time.Minute
	// DefaultProviderHTTPTimeout bounds one provider SDK HTTP attempt. It must
	// never be shorter than the whole-call deadline.
	DefaultProviderHTTPTimeout = DefaultProviderCallTimeout
	// DefaultWorkloadExecutionTimeout is the detached workload deadline. The
	// five-minute difference from the provider call is reserved for a guarded
	// terminal write to Host/TableTheory truth.
	DefaultWorkloadExecutionTimeout = 7*time.Hour + 50*time.Minute
	// DefaultTerminalPersistenceMargin is the minimum time retained after the
	// provider deadline for canonical terminal persistence.
	DefaultTerminalPersistenceMargin = 5 * time.Minute
	// DefaultRuntimeCleanupMargin is retained after the workload deadline for
	// AppTheory/AWS lifecycle cleanup before the hard MicroVM maximum.
	DefaultRuntimeCleanupMargin = 10 * time.Minute
)

var ErrInvalidExecutionEnvelope = errors.New("hosted genesis execution envelope is invalid")

// ExecutionEnvelope is Host's single bounded timing contract for one official
// AppTheory MicroVM provider operation. Durations contain no provider or
// transcript content and are safe deployment/configuration evidence.
type ExecutionEnvelope struct {
	ProviderHTTPTimeout       time.Duration
	ProviderCallTimeout       time.Duration
	WorkloadExecutionTimeout  time.Duration
	MicroVMMaximumDuration    time.Duration
	TerminalPersistenceMargin time.Duration
	RuntimeCleanupMargin      time.Duration
}

// DefaultExecutionEnvelope returns the deployed five-body timing contract.
func DefaultExecutionEnvelope() ExecutionEnvelope {
	return ExecutionEnvelope{
		ProviderHTTPTimeout:       DefaultProviderHTTPTimeout,
		ProviderCallTimeout:       DefaultProviderCallTimeout,
		WorkloadExecutionTimeout:  DefaultWorkloadExecutionTimeout,
		MicroVMMaximumDuration:    AWSLambdaMicroVMMaximumDuration,
		TerminalPersistenceMargin: DefaultTerminalPersistenceMargin,
		RuntimeCleanupMargin:      DefaultRuntimeCleanupMargin,
	}
}

// Validate proves the provider transport cannot abort before the whole-call
// context and that both persistence and platform-cleanup margins fit below the
// documented AWS maximum.
func (e ExecutionEnvelope) Validate() error {
	if e.ProviderHTTPTimeout <= 0 || e.ProviderCallTimeout <= 0 ||
		e.WorkloadExecutionTimeout <= 0 || e.MicroVMMaximumDuration <= 0 ||
		e.TerminalPersistenceMargin <= 0 || e.RuntimeCleanupMargin <= 0 {
		return ErrInvalidExecutionEnvelope
	}
	if e.MicroVMMaximumDuration > AWSLambdaMicroVMMaximumDuration ||
		e.ProviderHTTPTimeout < e.ProviderCallTimeout ||
		e.ProviderHTTPTimeout > e.WorkloadExecutionTimeout ||
		e.WorkloadExecutionTimeout-e.ProviderCallTimeout < e.TerminalPersistenceMargin ||
		e.MicroVMMaximumDuration-e.WorkloadExecutionTimeout < e.RuntimeCleanupMargin {
		return ErrInvalidExecutionEnvelope
	}
	return nil
}
