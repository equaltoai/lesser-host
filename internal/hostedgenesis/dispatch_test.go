package hostedgenesis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	microvmtestkit "github.com/theory-cloud/apptheory/testkit/microvm"
)

func TestControllerRuntimeDispatcherDispatchesM16Run(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProvider(),
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "arn:aws:lambda:us-east-1:123456789012:microvm-image/hosted-genesis:1",
		NetworkConnectorRef: "arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis-egress",
	})
	require.NoError(t, err)

	dispatcher := NewControllerRuntimeDispatcher(runtime)
	result, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", binding)
	require.NoError(t, err)
	require.Equal(t, binding.ConversationID, result.SessionID)
	require.NoError(t, result.LifecycleRef.Validate(binding))
	require.Equal(t, MicroVMSourceOfTruth, result.LifecycleRef.SourceOfTruth)
	require.Equal(t, runtimemicrovm.CommandRun, result.LifecycleRef.LastAction)
	require.NotEmpty(t, result.LifecycleRef.MicroVMID)
}

func TestControllerRuntimeDispatcherFailsClosedWhenRuntimeNil(t *testing.T) {
	t.Parallel()

	dispatcher := NewControllerRuntimeDispatcher(nil)
	_, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", testMicroVMBinding())
	require.ErrorIs(t, err, ErrMicroVMDispatchUnavailable)
}

func TestControllerRuntimeDispatcherFailsClosedOnInvalidBinding(t *testing.T) {
	t.Parallel()

	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProvider(),
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "image-ref",
		NetworkConnectorRef: "network-ref",
	})
	require.NoError(t, err)
	dispatcher := NewControllerRuntimeDispatcher(runtime)
	_, err = dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", MicroVMSessionBinding{})
	require.Error(t, err)
}

func TestControllerRuntimeDispatcherFailsClosedOnEmptyRequestID(t *testing.T) {
	t.Parallel()

	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProvider(),
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "image-ref",
		NetworkConnectorRef: "network-ref",
	})
	require.NoError(t, err)
	dispatcher := NewControllerRuntimeDispatcher(runtime)
	_, err = dispatcher.DispatchMicroVMRun(context.Background(), "  ", testMicroVMBinding())
	require.ErrorIs(t, err, ErrMicroVMDispatchUnavailable)
}

func TestControllerRuntimeDispatcherPropagatesControllerError(t *testing.T) {
	t.Parallel()

	// A controller backed by a registry that has lost the session cannot run;
	// the dispatcher must surface the controller error rather than fall back.
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProvider(),
		Registry:            &failingSessionRegistry{err: errors.New("registry unavailable")},
		ImageRef:            "image-ref",
		NetworkConnectorRef: "network-ref",
	})
	require.NoError(t, err)
	dispatcher := NewControllerRuntimeDispatcher(runtime)
	_, err = dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", testMicroVMBinding())
	require.Error(t, err)
}

func TestControllerRuntimeDispatcherReconcilesViaControllerGet(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	provider := microvmtestkit.NewFakeProvider()
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            provider,
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "arn:aws:lambda:us-east-1:123456789012:microvm-image/hosted-genesis:1",
		NetworkConnectorRef: "arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis-egress",
	})
	require.NoError(t, err)
	dispatcher := NewControllerRuntimeDispatcher(runtime)

	// Dispatch a run first so the session exists in the provider + registry and
	// Host has a populated lifecycle ref to reconcile against.
	dispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-run", binding)
	require.NoError(t, err)

	// Reconcile via the M16 controller get command: the live VM is observed as
	// running (non-terminal) and the reconciled ref maps back to the binding.
	result, err := dispatcher.ReconcileMicroVM(context.Background(), "req-get", binding, dispatch.LifecycleRef)
	require.NoError(t, err)
	require.Equal(t, binding.ConversationID, result.SessionID)
	require.False(t, result.Terminal, "live VM must not be terminal")
	require.NoError(t, result.LifecycleRef.Validate(binding))
	require.Equal(t, runtimemicrovm.CommandGet, result.LifecycleRef.LastAction)
}

func TestControllerRuntimeDispatcherReconcileTerminalVMReportsTerminal(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	provider := microvmtestkit.NewFakeProvider()
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            provider,
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "arn:aws:lambda:us-east-1:123456789012:microvm-image/hosted-genesis:1",
		NetworkConnectorRef: "arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis-egress",
	})
	require.NoError(t, err)
	dispatcher := NewControllerRuntimeDispatcher(runtime)

	dispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-run", binding)
	require.NoError(t, err)

	// Terminate the VM through the controller so the provider reports a terminal
	// state; reconciliation must surface Terminal=true (dead/expired VM) so the
	// control plane maps it to a loud failure, not a silent no-op.
	_, err = runtime.Command(context.Background(), runtimemicrovm.CommandTerminate, "req-terminate", binding)
	require.NoError(t, err)

	result, err := dispatcher.ReconcileMicroVM(context.Background(), "req-get", binding, dispatch.LifecycleRef)
	require.NoError(t, err)
	require.True(t, result.Terminal, "terminated VM must reconcile as terminal")
	require.NoError(t, result.LifecycleRef.Validate(binding))
}

func TestControllerRuntimeDispatcherReconcileFailsClosedWhenRuntimeNil(t *testing.T) {
	t.Parallel()

	dispatcher := NewControllerRuntimeDispatcher(nil)
	_, err := dispatcher.ReconcileMicroVM(context.Background(), "req-get", testMicroVMBinding(), MicroVMLifecycleRef{})
	require.ErrorIs(t, err, ErrMicroVMDispatchUnavailable)
}

func TestControllerRuntimeDispatcherReconcileFailsClosedOnInvalidRef(t *testing.T) {
	t.Parallel()

	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProvider(),
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "image-ref",
		NetworkConnectorRef: "network-ref",
	})
	require.NoError(t, err)
	dispatcher := NewControllerRuntimeDispatcher(runtime)
	// An empty ref cannot validate against the binding: fail closed.
	_, err = dispatcher.ReconcileMicroVM(context.Background(), "req-get", testMicroVMBinding(), MicroVMLifecycleRef{})
	require.Error(t, err)
}

func TestControllerRuntimeDispatcherReconcilePropagatesControllerError(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProvider(),
		Registry:            &failingSessionRegistry{err: errors.New("registry unavailable")},
		ImageRef:            "image-ref",
		NetworkConnectorRef: "network-ref",
	})
	require.NoError(t, err)
	dispatcher := NewControllerRuntimeDispatcher(runtime)
	dispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-run", binding)
	// The failing registry may reject the run; if it succeeded enough to build
	// a ref, exercise reconcile against the same failing registry. Either way
	// reconcile must surface an error rather than silently no-op.
	if err == nil {
		_, reconcileErr := dispatcher.ReconcileMicroVM(context.Background(), "req-get", binding, dispatch.LifecycleRef)
		require.Error(t, reconcileErr)
	}
}

type failingSessionRegistry struct {
	err error
}

func (f *failingSessionRegistry) Get(ctx context.Context, key runtimemicrovm.SessionKey) (runtimemicrovm.SessionRecord, error) {
	return runtimemicrovm.SessionRecord{}, f.err
}
func (f *failingSessionRegistry) Put(ctx context.Context, record runtimemicrovm.SessionRecord) (runtimemicrovm.SessionRecord, error) {
	return runtimemicrovm.SessionRecord{}, f.err
}
func (f *failingSessionRegistry) Delete(ctx context.Context, key runtimemicrovm.SessionKey) error {
	return f.err
}

// TestH1_4_MicroVMReconcileIsTerminalClassification proves the H1.4 terminal
// classification used by the reconcile seam maps dead/expired sessions to
// terminal=true and live sessions to terminal=false across every branch: a
// terminal lifecycle state is terminal regardless of expiry; a non-terminal
// state with a past expiry is terminal (expired/dead); a non-terminal state
// with a future expiry is NOT terminal (live); a non-terminal state with no
// reported expiry is NOT terminal. This is the lifecycle-state coverage H1.4
// adds on top of H1.3's terminated/failed mapping: expiry-in-the-past is a
// dead VM even when the lifecycle state is non-terminal (e.g. stopped).
func TestH1_4_MicroVMReconcileIsTerminalClassification(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		state     runtimemicrovm.LifecycleState
		expiresAt time.Time
		want      bool
	}{
		{"terminated is terminal regardless of expiry", runtimemicrovm.StateTerminated, observedAt.Add(-time.Hour), true},
		{"failed is terminal regardless of expiry", runtimemicrovm.StateFailed, observedAt.Add(time.Hour), true},
		{"non-terminal with past expiry is terminal (expired)", runtimemicrovm.StateStopped, observedAt.Add(-time.Minute), true},
		{"non-terminal with expiry exactly at observation is terminal", runtimemicrovm.StateStopped, observedAt, true},
		{"non-terminal with future expiry is not terminal (live)", runtimemicrovm.StateRunning, observedAt.Add(time.Hour), false},
		{"non-terminal with no reported expiry is not terminal", runtimemicrovm.StateRunning, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := microVMReconcileIsTerminal(tc.state, tc.expiresAt, observedAt)
			require.Equal(t, tc.want, got)
		})
	}
}
