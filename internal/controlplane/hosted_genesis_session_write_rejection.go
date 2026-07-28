package controlplane

import (
	"errors"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

// Durable hosted genesis session writes are validated before the accepted-turn
// transaction is built: models.HostedGenesisSession.BeforeCreate/BeforeUpdate
// re-derives keys and re-binds the typed declaration candidate, and
// hostedgenesis.ValidateTransition checks the status move. Both run inside the
// credit-debit transaction builder, so a rejection aborts the builder and no
// usage ledger entry, budget update, session row, idempotency row, or
// conversation row is ever written.
//
// Those builder errors used to escape untyped through
// debitSoulMintConversationCredits, which reports any non-condition transaction
// error as app.internal "failed to debit credits" — surfaced to the instance as
// an untyped 500 soul_instance.internal. That was wrong twice over: the message
// blamed billing for a durable-state rejection, and the untyped 500 carried no
// recovery affordance. Because the rejection is deterministic on the stored row,
// every later advance on the lane repeated it, and the lane never reached the
// failed status that the /recover path keys its retry_same_step /
// restart_soul_bootstrap machinery off — a permanently wedged lane with no way
// out (equaltoai/lesser-host#1003).
//
// Classifying the rejection here keeps the fail-closed behavior (nothing is
// written, the turn is not accepted) while giving the caller the typed,
// recoverable state it needs to act on.
const (
	// hostedGenesisSessionWriteReasonInvalidState marks a durable session row
	// that no longer satisfies its own model invariants (candidate binding,
	// MicroVM lifecycle ref, VM checkpoint). Replaying the same advance cannot
	// clear it; the lane needs a fresh soul bootstrap.
	hostedGenesisSessionWriteReasonInvalidState = "session_state_invalid"
	// hostedGenesisSessionWriteReasonInvalidTransition marks a status move that
	// is illegal from the status the request loaded, which is what a concurrent
	// advance on the same lane looks like. Re-reading and retrying the same step
	// is the correct recovery.
	hostedGenesisSessionWriteReasonInvalidTransition = "session_transition_invalid"

	hostedGenesisSessionWriteDetailReason         = "reason"
	hostedGenesisSessionWriteDetailRetryable      = "retryable"
	hostedGenesisSessionWriteDetailRecoveryAction = "recovery_action"
	hostedGenesisSessionWriteDetailRestartPath    = "restart_path"

	hostedGenesisRestartPath = "/api/v1/soul/instance/agents/register/begin"

	hostedGenesisSessionWriteRejectedMessage = "conversation cannot accept a new turn from its stored state"
)

// hostedGenesisSessionWriteRejection carries the classification of a durable
// hosted genesis session write that was rejected before the transaction ran.
type hostedGenesisSessionWriteRejection struct {
	reason         string
	retryable      bool
	recoveryAction hostedgenesis.RecoveryAction
	cause          error
}

func (e *hostedGenesisSessionWriteRejection) Error() string {
	return e.reason + ": " + e.cause.Error()
}

func (e *hostedGenesisSessionWriteRejection) Unwrap() error { return e.cause }

// appTheoryError renders the rejection as the typed, recoverable conflict the
// instance sees. The details mirror the recover path's restart envelope so a
// caller handles both the same way.
func (e *hostedGenesisSessionWriteRejection) appTheoryError() *apptheory.AppTheoryError {
	details := map[string]any{
		hostedGenesisSessionWriteDetailReason:         e.reason,
		hostedGenesisSessionWriteDetailRetryable:      e.retryable,
		hostedGenesisSessionWriteDetailRecoveryAction: string(e.recoveryAction),
	}
	if e.recoveryAction == hostedgenesis.RecoveryActionRestartSoulBootstrap {
		details[hostedGenesisSessionWriteDetailRestartPath] = hostedGenesisRestartPath
	}
	return newAppTheoryError(appErrCodeConflict, hostedGenesisSessionWriteRejectedMessage).WithDetails(details)
}

// newHostedGenesisSessionStateRejection classifies a model-invariant rejection
// as a non-retryable restart_soul_bootstrap state.
func newHostedGenesisSessionStateRejection(err error) error {
	return &hostedGenesisSessionWriteRejection{
		reason:         hostedGenesisSessionWriteReasonInvalidState,
		retryable:      false,
		recoveryAction: hostedgenesis.RecoveryActionRestartSoulBootstrap,
		cause:          err,
	}
}

// newHostedGenesisSessionTransitionRejection classifies an illegal status move
// as a retryable retry_same_step state.
func newHostedGenesisSessionTransitionRejection(err error) error {
	return &hostedGenesisSessionWriteRejection{
		reason:         hostedGenesisSessionWriteReasonInvalidTransition,
		retryable:      true,
		recoveryAction: hostedgenesis.RecoveryActionRetrySameStep,
		cause:          err,
	}
}

// hostedGenesisSessionWriteRejectionFrom reports the classification carried by
// err, or nil when err is not a session write rejection.
func hostedGenesisSessionWriteRejectionFrom(err error) *hostedGenesisSessionWriteRejection {
	var rejection *hostedGenesisSessionWriteRejection
	if errors.As(err, &rejection) {
		return rejection
	}
	return nil
}
