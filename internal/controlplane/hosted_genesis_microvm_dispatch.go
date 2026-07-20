package controlplane

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

// hostedGenesisMicroVMDispatcherInitTimeout bounds the HTTP MicroVM dispatcher
// construction NewServer performs at startup. The only network-ish step is the
// one-shot SSM GetParameter fetch for the authorizer bearer token; a stuck
// construction must not wedge the control-plane Lambda cold start indefinitely.
const hostedGenesisMicroVMDispatcherInitTimeout = 5 * time.Second

// hostedGenesisMicroVMHTTPTimeout bounds each HTTP call the dispatcher makes to
// the governed AppTheoryMicrovmController API. The accept path must return 202
// well under 2s; the controller Lambda's own timeout (30s) is the outer bound.
// A non-2xx or timed-out call fails closed and loudly (no sync LLM fallback).
const hostedGenesisMicroVMHTTPTimeout = 10 * time.Second

// hostedGenesisMicroVMDispatcherBuilder is the seam NewServer uses to construct
// the production MicroVM dispatcher. It defaults to newHostedGenesisMicroVMDispatcher
// so production builds the real HTTPControllerDispatcher against the governed
// AppTheoryMicrovmController HTTP API. Tests override it to inject an
// HTTP-stub dispatcher and prove NewServer wires a non-nil dispatcher onto the
// Server without calling AWS or SSM. The seam is package-level because the
// construction happens before a Server exists. ssmGetParameter is the Server's
// SSM getter used to fetch the authorizer bearer token (a credential).
var hostedGenesisMicroVMDispatcherBuilder = func(ctx context.Context, cfg config.Config, ssmGetParameter func(ctx context.Context, name string) (string, error), opts hostedGenesisMicroVMDispatcherOptions) hostedgenesis.MicroVMDispatcher {
	return newHostedGenesisMicroVMDispatcher(ctx, cfg, ssmGetParameter, opts)
}

// hostedGenesisMicroVMDispatcherOptions exposes the HTTP transport + auth seams
// for test injection. Production leaves them nil so newHostedGenesisMicroVMDispatcher
// builds a real net/http client and fetches the authorizer bearer token from SSM
// via the store. Tests inject an http.Client pointed at an httptest.Server stub
// controller (and/or an ssmGetParameter stub returning a test token) to prove the
// wiring without calling AWS or SSM.
type hostedGenesisMicroVMDispatcherOptions struct {
	// httpClient is the HTTP client used to call the controller API. Production
	// leaves nil so a bounded net/http client is constructed. Tests inject a
	// client pointed at an httptest.Server stub controller.
	httpClient *http.Client
	// authToken overrides the SSM-fetched authorizer bearer token (tests use a
	// stub token). When non-empty it takes precedence over the SSM fetch so
	// tests do not need a store. Production leaves it empty.
	authToken string
	// ssmGetParameter overrides the store SSM getter for the auth-token fetch.
	// When non-nil it takes precedence over the store getter. Production leaves
	// it nil.
	ssmGetParameter func(ctx context.Context, name string) (string, error)
}

// newHostedGenesisMicroVMDispatcher constructs the production hosted genesis
// MicroVM dispatcher controlplane.NewServer wires onto the Server (P52 H1.5).
//
// It builds the real HTTPControllerDispatcher against the governed
// AppTheoryMicrovmController HTTP API: POST /microvms to run, GET
// /microvms/{session_id} to reconcile. The authorizer bearer token is fetched
// at construction time from the SSM SecureString named by the config (a
// credential — never committed or logged); the controller endpoint + image/
// network refs come from CDK-provided env vars. The control plane never makes
// raw AWS RunMicrovm/GetMicrovm SDK calls or touches the session registry: the
// controller Lambda is the single governed surface (auth, lifecycle validation,
// registry shape, fail-closed env). There is no dual path.
//
// Fail-closed posture: when the MicroVM config is disabled or incomplete, the
// SSM auth-token fetch fails or returns an empty token, or the HTTP client /
// dispatcher construction fails, the dispatcher is NOT wired — the returned
// dispatcher is nil and the accept path fails closed and loudly with a typed
// 503 microvm_unavailable. NewServer never falls back to a synchronous
// control-plane LLM call; the retained sync assistant runner stays behind its
// defaulted-false non-production guard (H2.1 deletes it). A nil return is the
// explicit fail-closed outcome — the caller sets hostedGenesisMicroVMDispatcher
// = nil and the accept path's existing nil-check surfaces the loud 503.
func newHostedGenesisMicroVMDispatcher(ctx context.Context, cfg config.Config, ssmGetParameter func(ctx context.Context, name string) (string, error), opts hostedGenesisMicroVMDispatcherOptions) hostedgenesis.MicroVMDispatcher {
	microvmCfg := cfg.HostedGenesisMicroVM
	if !microvmCfg.Complete() {
		if microvmCfg.Enabled {
			// Enabled but incomplete is a misconfiguration the operator must see.
			log.Printf("controlplane: hosted genesis microvm dispatcher disabled (incomplete config) endpoint_set=%t auth_token_ssm_param_set=%t image_ref_set=%t network_connector_set=%t",
				microvmCfg.ControllerEndpoint != "", microvmCfg.AuthTokenSSMParam != "", microvmCfg.ImageRef != "", microvmCfg.NetworkConnectorRef != "")
		}
		return nil
	}

	authToken := strings.TrimSpace(opts.authToken)
	if authToken == "" {
		token, err := resolveMicroVMAuthToken(ctx, ssmGetParameter, microvmCfg.AuthTokenSSMParam, opts)
		if err != nil {
			log.Printf("controlplane: hosted genesis microvm dispatcher unavailable (auth token fetch failed) err=%v", err)
			return nil
		}
		authToken = strings.TrimSpace(token)
	}
	if authToken == "" {
		log.Printf("controlplane: hosted genesis microvm dispatcher unavailable (auth token is empty)")
		return nil
	}

	httpClient := opts.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: hostedGenesisMicroVMHTTPTimeout}
	}

	dispatcher, err := hostedgenesis.NewHTTPControllerDispatcher(hostedgenesis.HTTPControllerDispatcherConfig{
		Endpoint:             microvmCfg.ControllerEndpoint,
		AuthToken:            authToken,
		ImageRef:             microvmCfg.ImageRef,
		NetworkConnectorRef:  microvmCfg.NetworkConnectorRef,
		IngressConnectorRefs: append([]string(nil), microvmCfg.IngressConnectorRefs...),
		EgressConnectorRefs:  append([]string(nil), microvmCfg.EgressConnectorRefs...),
		MaxDurationSeconds:   microvmCfg.MaximumDurationSeconds,
		IdlePolicy:           microVMIdlePolicyFromConfig(microvmCfg.IdlePolicy),
		HTTPClient:           httpClient,
	})
	if err != nil {
		log.Printf("controlplane: hosted genesis microvm dispatcher unavailable (http dispatcher construction failed) err=%v", err)
		return nil
	}
	log.Printf("controlplane: hosted genesis microvm dispatcher wired stage=%s endpoint=%s image_ref=%s max_duration_seconds=%d idle_max_seconds=%d idle_suspended_seconds=%d idle_auto_resume=%t",
		strings.TrimSpace(cfg.Stage), microvmCfg.ControllerEndpoint, microvmCfg.ImageRef, microvmCfg.MaximumDurationSeconds,
		microvmCfg.IdlePolicy.MaxIdleDurationSeconds, microvmCfg.IdlePolicy.SuspendedDurationSeconds, microvmCfg.IdlePolicy.AutoResumeEnabled)
	return dispatcher
}

func microVMIdlePolicyFromConfig(policy config.HostedGenesisMicroVMIdlePolicyConfig) *runtimemicrovm.ProviderIdlePolicy {
	if !policy.Complete() {
		return nil
	}
	return &runtimemicrovm.ProviderIdlePolicy{
		AutoResumeEnabled:        policy.AutoResumeEnabled,
		MaxIdleDurationSeconds:   policy.MaxIdleDurationSeconds,
		SuspendedDurationSeconds: policy.SuspendedDurationSeconds,
	}
}

// resolveMicroVMAuthToken fetches the authorizer bearer token from SSM. The
// token is a credential: it is returned to the caller for presentation in the
// Authorization header and never logged. opts.ssmGetParameter takes precedence
// over the store getter (test injection); otherwise the store's SSM getter is
// used. An empty SSM parameter name is impossible here — Complete() already
// required it.
func resolveMicroVMAuthToken(ctx context.Context, ssmGetParameter func(ctx context.Context, name string) (string, error), paramName string, opts hostedGenesisMicroVMDispatcherOptions) (string, error) {
	if opts.ssmGetParameter != nil {
		return opts.ssmGetParameter(ctx, paramName)
	}
	if ssmGetParameter == nil {
		return "", errMicroVMDispatcherStoreUnavailable
	}
	return ssmGetParameter(ctx, paramName)
}

// errMicroVMDispatcherStoreUnavailable is the fail-closed error when the
// auth-token SSM fetch cannot proceed because no SSM getter is wired.
var errMicroVMDispatcherStoreUnavailable = errMicroVM("controlplane: hosted genesis microvm dispatcher ssm getter unavailable (cannot fetch auth token)")

type errMicroVM string

func (e errMicroVM) Error() string { return string(e) }
