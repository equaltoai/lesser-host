package hostedgenesis

import (
	"fmt"
	"strings"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

const (
	// MicroVMTurnEndpointPath is Host's application-level MicroVM turn endpoint.
	// It deliberately does not use the AWS-reserved lifecycle /run hook path.
	MicroVMTurnEndpointPath = "/hosted-genesis/turn"

	// MicroVMTurnPort is the workload port the hosted-genesis turn endpoint
	// serves. AppTheory's controller invoke route uses this value only as the
	// sanitized x-apptheory-microvm-port control header; Host never handles raw
	// provider proxy tokens.
	MicroVMTurnPort = int32(8080)
)

// NewMicroVMTurnLifecycleEvent builds the sanitized M16 lifecycle event sent
// through AppTheory's canonical controller invoke route to the hosted-genesis
// workload. The body carries only HostedGenesisSession ids and tenant/session
// binding metadata — no credentials, provider tokens, endpoint URLs, or raw
// AWS SDK material.
func NewMicroVMTurnLifecycleEvent(requestID string, binding MicroVMSessionBinding) (runtimemicrovm.LifecycleEvent, error) {
	if err := binding.Validate(); err != nil {
		return runtimemicrovm.LifecycleEvent{}, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return runtimemicrovm.LifecycleEvent{}, ErrMicroVMDispatchUnavailable
	}
	return runtimemicrovm.LifecycleEvent{
		RequestID: requestID,
		TenantID:  binding.TenantID(),
		Namespace: MicroVMNamespace,
		SessionID: strings.TrimSpace(binding.ConversationID),
		Hook:      runtimemicrovm.HookRun,
		State:     runtimemicrovm.StateRunning,
		Metadata:  binding.Metadata(),
	}, nil
}

// ValidateMicroVMTurnResult proves AppTheory's invoke route reached the real
// hosted-genesis workload and that the workload accepted the exact turn Host
// dispatched. Empty bodies, catch-all responses, wrong tenant/session bindings,
// and failed lifecycle results are loud failures.
func ValidateMicroVMTurnResult(result runtimemicrovm.LifecycleResult, requestID string, binding MicroVMSessionBinding) error {
	if result.Error != nil && result.Error.Code != "" {
		return fmt.Errorf("hosted genesis microvm invoke: workload failed: %w", result.Error)
	}
	if strings.TrimSpace(result.RequestID) != strings.TrimSpace(requestID) {
		return fmt.Errorf("hosted genesis microvm invoke: workload result request binding mismatch")
	}
	if strings.TrimSpace(result.TenantID) != binding.TenantID() || strings.TrimSpace(result.Namespace) != MicroVMNamespace || strings.TrimSpace(result.SessionID) != strings.TrimSpace(binding.ConversationID) {
		return fmt.Errorf("hosted genesis microvm invoke: workload result session binding mismatch")
	}
	if result.Hook != runtimemicrovm.HookRun || result.State != runtimemicrovm.StateRunning {
		return fmt.Errorf("hosted genesis microvm invoke: workload result did not confirm turn acceptance")
	}
	return nil
}
