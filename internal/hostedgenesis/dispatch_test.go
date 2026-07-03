package hostedgenesis

import (
	"context"
	"errors"
	"testing"

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
