package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store"
)

// stubHostedGenesisMicroVMProvider is a minimal in-memory AppTheory MicroVM
// provider for NewServer wiring tests. It implements the constrained Provider
// surface enough to satisfy runtimemicrovm.NewRealController and dispatch a run
// command; it never calls AWS. It mirrors the example controller's localProvider
// shape but is intentionally tiny — only Run + Get are exercised by the dispatch
// wiring tests.
type stubHostedGenesisMicroVMProvider struct {
	t        *testing.T
	runErr   error
	sessions map[runtimemicrovm.SessionKey]runtimemicrovm.ProviderSession
}

func newStubMicroVMProvider(t *testing.T) *stubHostedGenesisMicroVMProvider {
	t.Helper()
	return &stubHostedGenesisMicroVMProvider{t: t, sessions: map[runtimemicrovm.SessionKey]runtimemicrovm.ProviderSession{}}
}

func (p *stubHostedGenesisMicroVMProvider) Run(_ context.Context, input runtimemicrovm.ProviderRunInput) (runtimemicrovm.ProviderSession, error) {
	if err := runtimemicrovm.ValidateProviderRunInput(input); err != nil {
		return runtimemicrovm.ProviderSession{}, err
	}
	if p.runErr != nil {
		return runtimemicrovm.ProviderSession{}, p.runErr
	}
	now := time.Now().UTC()
	session := runtimemicrovm.ProviderSession{
		TenantID:          input.TenantID,
		Namespace:         input.Namespace,
		SessionID:         input.SessionID,
		ProviderMicroVMID: "stub-microvm-" + strings.TrimSpace(input.SessionID),
		State:             runtimemicrovm.StateRunning,
		ProviderState:     "running",
		ImageRef:          input.ImageRef,
		ImageVersion:      input.ImageVersion,
		StartedAt:         now,
		RegistryVersion:   1,
	}
	if err := runtimemicrovm.ValidateProviderSession(session); err != nil {
		p.t.Fatalf("stub provider produced invalid session: %v", err)
	}
	p.sessions[session.Key()] = session
	return session, nil
}

func (p *stubHostedGenesisMicroVMProvider) Get(_ context.Context, input runtimemicrovm.ProviderSessionInput) (runtimemicrovm.ProviderSession, error) {
	if err := runtimemicrovm.ValidateProviderSessionInput(runtimemicrovm.OperationGet, input); err != nil {
		return runtimemicrovm.ProviderSession{}, err
	}
	stored, ok := p.sessions[input.Binding.Key()]
	if !ok {
		return runtimemicrovm.ProviderSession{}, errors.New("stub provider: session not found")
	}
	return stored, nil
}

func (p *stubHostedGenesisMicroVMProvider) List(_ context.Context, input runtimemicrovm.ProviderListInput) (runtimemicrovm.ProviderListOutput, error) {
	if err := runtimemicrovm.ValidateProviderListInput(input); err != nil {
		return runtimemicrovm.ProviderListOutput{}, err
	}
	sessions := make([]runtimemicrovm.ProviderSession, 0, len(input.KnownSessions))
	for _, binding := range input.KnownSessions {
		if stored, ok := p.sessions[binding.Key()]; ok {
			sessions = append(sessions, stored)
		}
	}
	return runtimemicrovm.ProviderListOutput{Sessions: sessions}, nil
}

func (p *stubHostedGenesisMicroVMProvider) Suspend(_ context.Context, input runtimemicrovm.ProviderSessionInput) (runtimemicrovm.ProviderSession, error) {
	return runtimemicrovm.ProviderSession{}, errors.New("stub provider: suspend not supported")
}

func (p *stubHostedGenesisMicroVMProvider) Resume(_ context.Context, input runtimemicrovm.ProviderSessionInput) (runtimemicrovm.ProviderSession, error) {
	return runtimemicrovm.ProviderSession{}, errors.New("stub provider: resume not supported")
}

func (p *stubHostedGenesisMicroVMProvider) Terminate(_ context.Context, input runtimemicrovm.ProviderSessionInput) (runtimemicrovm.ProviderSession, error) {
	stored, ok := p.sessions[input.Binding.Key()]
	if !ok {
		return runtimemicrovm.ProviderSession{}, errors.New("stub provider: session not found")
	}
	stored.State = runtimemicrovm.StateTerminated
	stored.ProviderState = "terminated"
	stored.Terminal = true
	stored.TerminatedAt = time.Now().UTC()
	p.sessions[input.Binding.Key()] = stored
	return stored, nil
}

func (p *stubHostedGenesisMicroVMProvider) CreateAuthToken(_ context.Context, input runtimemicrovm.ProviderTokenInput) (runtimemicrovm.ProviderToken, error) {
	return runtimemicrovm.ProviderToken{}, errors.New("stub provider: auth-token not supported")
}

func (p *stubHostedGenesisMicroVMProvider) CreateShellToken(_ context.Context, input runtimemicrovm.ProviderTokenInput) (runtimemicrovm.ProviderToken, error) {
	return runtimemicrovm.ProviderToken{}, errors.New("stub provider: shell-auth-token not supported")
}

func microVMWiringTestConfig() config.Config {
	return config.Config{
		Stage: "lab",
		HostedGenesisMicroVM: config.HostedGenesisMicroVMConfig{
			Enabled:                   true,
			ImageRef:                  "arn:aws:lambda::microvm-image/hosted-genesis:test",
			NetworkConnectorRef:       "arn:aws:lambda::network-connector/egress:test",
			IngressConnectorRefs:      []string{"arn:aws:lambda::network-connector/all-ingress:test"},
			EgressConnectorRefs:       []string{"arn:aws:lambda::network-connector/egress:test"},
			SessionRegistryTable:      "hosted-genesis-microvm-sessions-lab",
			MaximumDurationSeconds:    300,
			ReconstructionStaleAfterS: 300,
		},
	}
}

// TestH1_5_NewServerWiresRealControllerRuntimeDispatcherForDeployedStages
// proves NewServer constructs a real ControllerRuntimeDispatcher against the M16
// controller runtime when the MicroVM config is enabled and complete, and sets
// it on the Server so the accept path is dispatch-only (no sync LLM). The stub
// provider + memory registry prove the wiring without calling AWS.
func TestH1_5_NewServerWiresRealControllerRuntimeDispatcherForDeployedStages(t *testing.T) {
	cfg := microVMWiringTestConfig()
	provider := newStubMicroVMProvider(t)
	st := store.New(nil) // reconstruction hook is only invoked on a cache miss; dispatch does not need a live DB

	dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, st, hostedGenesisMicroVMDispatcherOptions{
		providerFactory: func(_ context.Context) (runtimemicrovm.Provider, error) {
			return provider, nil
		},
		registryFactory: func() (runtimemicrovm.SessionRegistry, error) {
			return runtimemicrovm.NewMemorySessionRegistry(), nil
		},
		reconstructionStaleAfter: time.Minute,
	})
	if dispatcher == nil {
		t.Fatalf("expected a real ControllerRuntimeDispatcher wired for a complete enabled config, got nil")
	}

	// The dispatcher must be a *ControllerRuntimeDispatcher wrapping a non-nil
	// controller runtime; a stubMicroVMDispatcher would not satisfy this.
	crd, ok := dispatcher.(*hostedgenesis.ControllerRuntimeDispatcher)
	if !ok {
		t.Fatalf("expected *hostedgenesis.ControllerRuntimeDispatcher, got %T", dispatcher)
	}
	if crd == nil {
		t.Fatalf("controller runtime dispatcher is nil")
	}

	// Dispatching a run through the wired dispatcher must reach the stub
	// provider (proving the real M16 controller runtime is wired, not a stub
	// seam) and return a validated in_progress lifecycle ref.
	binding := hostedgenesis.MicroVMSessionBinding{
		InstanceSlug:   "acme",
		RegistrationID: "reg_123",
		AgentID:        "agent_abc",
		ConversationID: "conv_001",
		TurnID:         "turn_1",
	}
	result, err := dispatcher.DispatchMicroVMRun(context.Background(), "req_h1_5_wiring", binding)
	if err != nil {
		t.Fatalf("wired dispatcher run dispatch failed: %v", err)
	}
	if result.SessionID == "" || result.LifecycleRef.SessionID != binding.ConversationID {
		t.Fatalf("wired dispatcher returned an invalid dispatch result: %#v", result)
	}
	if result.LifecycleRef.LifecycleState != runtimemicrovm.StateRunning {
		t.Fatalf("expected running lifecycle state from stub provider, got %q", result.LifecycleRef.LifecycleState)
	}
	if len(provider.sessions) != 1 {
		t.Fatalf("expected stub provider to record one run dispatch, got %d", len(provider.sessions))
	}
}

// TestH1_5_NewServerLeavesDispatcherNilWhenConfigIncomplete proves the
// fail-closed posture: when the MicroVM config is disabled or incomplete,
// NewServer leaves the dispatcher unwired (nil) so the accept path fails closed
// and loudly with a typed 503 microvm_unavailable, never a silent sync LLM
// fallback.
func TestH1_5_NewServerLeavesDispatcherNilWhenConfigIncomplete(t *testing.T) {
	st := store.New(nil)

	disabled := microVMWiringTestConfig()
	disabled.HostedGenesisMicroVM.Enabled = false
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), disabled, st, hostedGenesisMicroVMDispatcherOptions{}); dispatcher != nil {
		t.Fatalf("disabled config must yield a nil dispatcher (fail-closed), got %T", dispatcher)
	}

	incomplete := microVMWiringTestConfig()
	incomplete.HostedGenesisMicroVM.SessionRegistryTable = ""
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), incomplete, st, hostedGenesisMicroVMDispatcherOptions{}); dispatcher != nil {
		t.Fatalf("incomplete config must yield a nil dispatcher (fail-closed), got %T", dispatcher)
	}
}

// TestH1_5_DispatcherConstructionFailureFailsLoudlyNoSyncFallback proves that
// when the MicroVM provider or session registry cannot be constructed, the
// dispatcher is left unwired (nil) — the accept path then fails closed with a
// typed 503, never a silent fallback to the synchronous control-plane LLM. This
// covers the production misconfiguration case (missing AWS credentials, missing
// session table, etc.) without calling AWS.
func TestH1_5_DispatcherConstructionFailureFailsLoudlyNoSyncFallback(t *testing.T) {
	cfg := microVMWiringTestConfig()
	st := store.New(nil)

	providerFailed := newStubMicroVMProvider(t)
	providerFailed.runErr = errors.New("aws credentials unavailable")
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, st, hostedGenesisMicroVMDispatcherOptions{
		providerFactory: func(_ context.Context) (runtimemicrovm.Provider, error) {
			return nil, providerFailed.runErr
		},
		registryFactory: func() (runtimemicrovm.SessionRegistry, error) {
			return runtimemicrovm.NewMemorySessionRegistry(), nil
		},
	}); dispatcher != nil {
		t.Fatalf("provider construction failure must yield a nil dispatcher (fail-closed, no sync fallback), got %T", dispatcher)
	}

	registryErr := errors.New("session registry table unavailable")
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, st, hostedGenesisMicroVMDispatcherOptions{
		providerFactory: func(_ context.Context) (runtimemicrovm.Provider, error) {
			return newStubMicroVMProvider(t), nil
		},
		registryFactory: func() (runtimemicrovm.SessionRegistry, error) {
			return nil, registryErr
		},
	}); dispatcher != nil {
		t.Fatalf("registry construction failure must yield a nil dispatcher (fail-closed, no sync fallback), got %T", dispatcher)
	}
}

// TestH1_5_NewServerSetsNonNilDispatcherOnServer proves NewServer wires a real
// ControllerRuntimeDispatcher onto the Server for a deployed-stage config. It
// overrides the package-level builder seam to inject stub provider/registry
// factories so the test never calls AWS, and asserts the Server's
// hostedGenesisMicroVMDispatcher is a *ControllerRuntimeDispatcher (not a stub
// seam, not nil). The seam is restored on cleanup.
func TestH1_5_NewServerSetsNonNilDispatcherOnServer(t *testing.T) {
	originalBuilder := hostedGenesisMicroVMDispatcherBuilder
	t.Cleanup(func() { hostedGenesisMicroVMDispatcherBuilder = originalBuilder })

	provider := newStubMicroVMProvider(t)
	hostedGenesisMicroVMDispatcherBuilder = func(ctx context.Context, cfg config.Config, st *store.Store, opts hostedGenesisMicroVMDispatcherOptions) hostedgenesis.MicroVMDispatcher {
		return newHostedGenesisMicroVMDispatcher(ctx, cfg, st, hostedGenesisMicroVMDispatcherOptions{
			providerFactory: func(_ context.Context) (runtimemicrovm.Provider, error) {
				return provider, nil
			},
			registryFactory: func() (runtimemicrovm.SessionRegistry, error) {
				return runtimemicrovm.NewMemorySessionRegistry(), nil
			},
			reconstructionStaleAfter: time.Minute,
		})
	}

	srv := NewServer(microVMWiringTestConfig(), store.New(nil))
	if srv == nil {
		t.Fatalf("NewServer returned nil")
	}
	if srv.hostedGenesisMicroVMDispatcher == nil {
		t.Fatalf("expected NewServer to wire a non-nil MicroVM dispatcher for a complete enabled config")
	}
	if _, ok := srv.hostedGenesisMicroVMDispatcher.(*hostedgenesis.ControllerRuntimeDispatcher); !ok {
		t.Fatalf("expected Server.hostedGenesisMicroVMDispatcher to be *ControllerRuntimeDispatcher, got %T", srv.hostedGenesisMicroVMDispatcher)
	}
}

// TestH1_5_NewServerLeavesDispatcherNilForEmptyConfig proves NewServer leaves
// the dispatcher unwired for the default/empty config (the existing
// fail-closed posture preserved across all current tests): no AWS calls, no
// sync LLM fallback, accept path 503s.
func TestH1_5_NewServerLeavesDispatcherNilForEmptyConfig(t *testing.T) {
	srv := NewServer(config.Config{}, store.New(nil))
	if srv == nil {
		t.Fatalf("NewServer returned nil")
	}
	if srv.hostedGenesisMicroVMDispatcher != nil {
		t.Fatalf("empty config must leave the dispatcher nil (fail-closed), got %T", srv.hostedGenesisMicroVMDispatcher)
	}
}

// TestH1_5_DispatcherNilWhenStoreUnavailable covers the fail-closed store-nil
// branch: a complete MicroVM config with a nil store must yield a nil
// dispatcher (the reconstruction hook cannot be built without a store).
func TestH1_5_DispatcherNilWhenStoreUnavailable(t *testing.T) {
	cfg := microVMWiringTestConfig()
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, nil, hostedGenesisMicroVMDispatcherOptions{
		providerFactory: func(_ context.Context) (runtimemicrovm.Provider, error) {
			return newStubMicroVMProvider(t), nil
		},
		registryFactory: func() (runtimemicrovm.SessionRegistry, error) {
			return runtimemicrovm.NewMemorySessionRegistry(), nil
		},
	}); dispatcher != nil {
		t.Fatalf("nil store must yield a nil dispatcher (fail-closed), got %T", dispatcher)
	}
}

// TestH1_5_DispatcherWiredViaRegistryDBElseBranch covers the production-default
// registryDBFactory else-branch (the path that builds a TableTheory
// SessionRegistry from a tablecore.DB rather than a test-injected
// SessionRegistry). It uses a tabletheory MockDB (no AWS) so
// NewTableTheorySessionRegistry succeeds and NewMicroVMControllerRuntime
// constructs the real runtime, proving the else-branch + the
// reconstruction-stale-after fallback (ReconstructionStaleAfterS=0) + the
// controller-runtime success path are all reachable without AWS.
func TestH1_5_DispatcherWiredViaRegistryDBElseBranch(t *testing.T) {
	cfg := microVMWiringTestConfig()
	cfg.HostedGenesisMicroVM.ReconstructionStaleAfterS = 0 // force the fallback stale-after path
	st := store.New(nil)

	dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, st, hostedGenesisMicroVMDispatcherOptions{
		providerFactory: func(_ context.Context) (runtimemicrovm.Provider, error) {
			return newStubMicroVMProvider(t), nil
		},
		// Leave registryFactory nil so the registryDBFactory else-branch runs.
		registryDBFactory: func() (tablecore.DB, error) {
			return new(ttmocks.MockDB), nil
		},
	})
	if dispatcher == nil {
		t.Fatalf("expected a wired dispatcher via the registryDB else-branch, got nil")
	}
	if _, ok := dispatcher.(*hostedgenesis.ControllerRuntimeDispatcher); !ok {
		t.Fatalf("expected *ControllerRuntimeDispatcher, got %T", dispatcher)
	}
}

// TestH1_5_MicroVMUnavailableAcceptPathReturnsExplicit503 is the grep-proof
// structural guard that the three MicroVM-unavailable accept-path returns in
// handlers_soul_mint_conversation_async.go (dispatcher-unavailable, invalid
// binding, dispatch-failed) carry forward G10a's explicit-status posture from
// H1.4: they return http.StatusServiceUnavailable (503), matching the error
// mapper's 503 for appErrCodeMicroVMUnavailable, not the prior silent
// http.StatusOK. The mapper-emitted 503 assertion at
// handlers_soul_mint_conversation_async_internal_test.go (TestH1_2_MicroVMUnavailableIsLoudFailureNotSyncLLFallthrough)
// stays green; this guard proves the returned response-status int is also 503.
func TestH1_5_MicroVMUnavailableAcceptPathReturnsExplicit503(t *testing.T) {
	asyncSrc := mustReadControlplaneSource(t, "handlers_soul_mint_conversation_async.go")

	// The three MicroVM-unavailable returns must reference 503, not 200.
	if strings.Contains(asyncSrc, "return failedSession, failedConv, http.StatusOK, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable") {
		t.Fatalf("H1.5 regression: a MicroVM-unavailable accept-path return still uses http.StatusOK (silent 200-on-failure), expected http.StatusServiceUnavailable")
	}
	explicit503Count := strings.Count(asyncSrc, "return failedSession, failedConv, http.StatusServiceUnavailable, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable")
	if explicit503Count != 3 {
		t.Fatalf("expected exactly three explicit 503 MicroVM-unavailable accept-path returns, got %d", explicit503Count)
	}
}
