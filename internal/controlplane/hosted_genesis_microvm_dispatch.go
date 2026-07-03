package controlplane

import (
	"context"
	"log"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	"github.com/theory-cloud/tabletheory"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store"
)

// hostedGenesisMicroVMDispatcherInitTimeout bounds the in-process MicroVM
// provider + session-registry construction NewServer performs at startup. The
// AWS SDK config load is the only network-ish step and is fast; a stuck
// construction must not wedge the control-plane Lambda cold start indefinitely.
const hostedGenesisMicroVMDispatcherInitTimeout = 5 * time.Second

// hostedGenesisMicroVMDispatcherBuilder is the seam NewServer uses to construct
// the production MicroVM dispatcher. It defaults to newHostedGenesisMicroVMDispatcher
// so production builds the real in-process ControllerRuntimeDispatcher against
// the AppTheory M16 controller runtime. Tests override it to inject stub
// provider/registry factories and prove NewServer wires a non-nil dispatcher
// onto the Server without calling AWS. The seam is package-level because the
// construction happens before a Server exists.
var hostedGenesisMicroVMDispatcherBuilder = func(ctx context.Context, cfg config.Config, st *store.Store, opts hostedGenesisMicroVMDispatcherOptions) hostedgenesis.MicroVMDispatcher {
	return newHostedGenesisMicroVMDispatcher(ctx, cfg, st, opts)
}

// hostedGenesisMicroVMDispatcherOptions exposes the AppTheory MicroVM provider
// and session-registry construction seams for test injection. Production leaves
// them nil so newHostedGenesisMicroVMDispatcher constructs the real
// NewAWSLambdaMicroVMProvider and the TableTheory session-registry DB against
// the AppTheoryMicrovmController CDK env vars. Tests inject stubs to prove the
// wiring without calling AWS.
type hostedGenesisMicroVMDispatcherOptions struct {
	// providerFactory builds the constrained MicroVM provider. Production uses
	// runtimemicrovm.NewAWSLambdaMicroVMProvider. Returning an error fails the
	// dispatcher construction loudly (no sync LLM fallback).
	providerFactory func(ctx context.Context) (runtimemicrovm.Provider, error)
	// registryDBFactory builds the TableTheory DB for the durable MicroVM
	// session registry. Production uses tabletheory.LambdaInit against the
	// SessionRegistryRecord model (table name from
	// APPTHEORY_MICROVM_SESSION_REGISTRY_TABLE). Returning an error fails loudly.
	registryDBFactory func() (tablecore.DB, error)
	// registryFactory is a test-only seam that builds the SessionRegistry
	// directly, bypassing the TableTheory DB. When non-nil it takes precedence
	// over registryDBFactory so tests can use runtimemicrovm.NewMemorySessionRegistry
	// without a DynamoDB backend. Production leaves it nil.
	registryFactory func() (runtimemicrovm.SessionRegistry, error)
	// reconstructionStaleAfter overrides the config-derived stale-after window
	// (tests use a short window). Zero falls back to the config value.
	reconstructionStaleAfter time.Duration
}

// newHostedGenesisMicroVMDispatcher constructs the production hosted genesis
// MicroVM dispatcher controlplane.NewServer wires onto the Server (P52 H1.5).
//
// It builds the real AppTheory M16 controller runtime in-process: the
// constrained AWS Lambda MicroVM provider (no raw AWS SDK surfaced to business
// code), the TableTheory durable session registry, and the Host-owned
// reconstruction hook that rehydrates execution/cache state from
// HostedGenesisSession truth. The runtime is wrapped in the H1.2
// ControllerRuntimeDispatcher seam so the accept path dispatches the controller
// run command and returns 202 without a synchronous control-plane LLM call.
//
// Fail-closed posture: when the MicroVM config is disabled or incomplete, or
// when the provider/registry/runtime construction fails, the dispatcher is NOT
// wired — the returned dispatcher is nil and the accept path fails closed and
// loudly with a typed 503 microvm_unavailable. NewServer never falls back to a
// synchronous control-plane LLM call; the retained sync assistant runner stays
// behind its defaulted-false non-production guard (H2.1 deletes it). A nil
// return with a nil error is the explicit fail-closed outcome — the caller sets
// hostedGenesisMicroVMDispatcher = nil and the accept path's existing nil-check
// surfaces the loud 503.
func newHostedGenesisMicroVMDispatcher(ctx context.Context, cfg config.Config, st *store.Store, opts hostedGenesisMicroVMDispatcherOptions) hostedgenesis.MicroVMDispatcher {
	microvmCfg := cfg.HostedGenesisMicroVM
	if !microvmCfg.Complete() {
		if microvmCfg.Enabled {
			// Enabled but incomplete is a misconfiguration the operator must see.
			log.Printf("controlplane: hosted genesis microvm dispatcher disabled (incomplete config) image_ref_set=%t network_connector_set=%t session_registry_table_set=%t",
				microvmCfg.ImageRef != "", microvmCfg.NetworkConnectorRef != "", microvmCfg.SessionRegistryTable != "")
		}
		return nil
	}
	if st == nil {
		log.Printf("controlplane: hosted genesis microvm dispatcher disabled (store unavailable)")
		return nil
	}

	providerFactory := opts.providerFactory
	if providerFactory == nil {
		providerFactory = func(ctx context.Context) (runtimemicrovm.Provider, error) {
			return runtimemicrovm.NewAWSLambdaMicroVMProvider(ctx)
		}
	}
	registryDBFactory := opts.registryDBFactory
	if registryDBFactory == nil {
		registryDBFactory = func() (tablecore.DB, error) {
			return tabletheory.LambdaInit(&runtimemicrovm.SessionRegistryRecord{})
		}
	}

	provider, err := providerFactory(ctx)
	if err != nil {
		log.Printf("controlplane: hosted genesis microvm dispatcher unavailable (provider construction failed) err=%v", err)
		return nil
	}
	var registry runtimemicrovm.SessionRegistry
	if opts.registryFactory != nil {
		registry, err = opts.registryFactory()
		if err != nil {
			log.Printf("controlplane: hosted genesis microvm dispatcher unavailable (session registry construction failed) err=%v", err)
			return nil
		}
	} else {
		registryDB, err := registryDBFactory()
		if err != nil {
			log.Printf("controlplane: hosted genesis microvm dispatcher unavailable (session registry construction failed) err=%v", err)
			return nil
		}
		registry, err = runtimemicrovm.NewTableTheorySessionRegistry(registryDB)
		if err != nil {
			log.Printf("controlplane: hosted genesis microvm dispatcher unavailable (session registry init failed) err=%v", err)
			return nil
		}
	}

	reconstructionStaleAfter := opts.reconstructionStaleAfter
	if reconstructionStaleAfter <= 0 {
		reconstructionStaleAfter = time.Duration(microvmCfg.ReconstructionStaleAfterS) * time.Second
	}
	if reconstructionStaleAfter <= 0 {
		reconstructionStaleAfter = 5 * time.Minute
	}

	runtime, err := hostedgenesis.NewMicroVMControllerRuntime(hostedgenesis.MicroVMControllerRuntimeConfig{
		Provider: provider,
		Registry: registry,
		ReconstructionHook: st.HostedGenesisMicroVMReconstructionHook(store.HostedGenesisMicroVMReconstructionConfig{
			ImageRef:                    microvmCfg.ImageRef,
			NetworkConnectorRef:         microvmCfg.NetworkConnectorRef,
			IngressNetworkConnectorRefs: append([]string(nil), microvmCfg.IngressConnectorRefs...),
			EgressNetworkConnectorRefs:  append([]string(nil), microvmCfg.EgressConnectorRefs...),
			ControllerID:                hostedgenesis.MicroVMControllerID,
			TTL:                         hostedgenesis.MicroVMRegistryReconstructionTTL,
		}),
		ImageRef:                    microvmCfg.ImageRef,
		NetworkConnectorRef:         microvmCfg.NetworkConnectorRef,
		IngressNetworkConnectorRefs: append([]string(nil), microvmCfg.IngressConnectorRefs...),
		EgressNetworkConnectorRefs:  append([]string(nil), microvmCfg.EgressConnectorRefs...),
		ControllerID:                hostedgenesis.MicroVMControllerID,
		SessionTTL:                  hostedgenesis.MicroVMRegistryReconstructionTTL,
		ReconstructionStaleAfter:    reconstructionStaleAfter,
		MaximumDurationSeconds:      microvmCfg.MaximumDurationSeconds,
	})
	if err != nil {
		log.Printf("controlplane: hosted genesis microvm dispatcher unavailable (controller runtime construction failed) err=%v", err)
		return nil
	}
	log.Printf("controlplane: hosted genesis microvm dispatcher wired stage=%s image_ref=%s max_duration_seconds=%d",
		strings.TrimSpace(cfg.Stage), microvmCfg.ImageRef, microvmCfg.MaximumDurationSeconds)
	return hostedgenesis.NewControllerRuntimeDispatcher(runtime)
}
