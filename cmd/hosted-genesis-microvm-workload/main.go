// Command hosted-genesis-microvm-workload is the in-VM AppTheory Lambda MicroVM
// image workload for hosted genesis conversations. It is the consumer-owned
// workload the MicroVM image runs instead of an external codeArtifactUri: the
// CDK image consumes this repo-built artifact.
//
// The workload serves the AppTheory M16 MicroVM image lifecycle hooks on port
// 8080 (matching the AppTheoryMicrovmImage hooks.port CDK configuration). Each
// hook drives the AppTheory runtime/microvm lifecycle vocabulary with the real
// M16 contract (validate/run/ready/suspend/resume/terminate/failure). The run
// hook executes the assistant turn AND declaration extraction through the
// existing internal/ai/llm clients — which carry an explicit per-provider HTTP
// timeout configured at startup — and durably records completion to
// HostedGenesisSession truth through the existing store layer, reusing
// persistHostedGenesisAcceptedAssistantTurn semantics via the completion writer.
//
// Fail-closed posture: there is no degraded/non-MicroVM fallback. Missing
// session/conversation/registration, missing provider keys, empty assistant
// responses, declaration-extraction failures, and idempotency conflicts all
// surface as typed completion failures or non-2xx hook responses — never as a
// silent HTTP 200 or a swallowed error.
//
// This is H1.1 of the MicroVM-only hosted genesis program (parent
// lesser-host#867). Dispatch wiring (H1.2), reconstruction-hook reachability
// (H1.3), recovery (H1.4), and full CDK de-lab-gating + lab E2E (H1.5) are
// separate steps; this workload is the repo-built artifact those steps will run.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
	"github.com/equaltoai/lesser-host/internal/observability"
	"github.com/equaltoai/lesser-host/internal/store"
)

const serviceName = "hosted-genesis-microvm-workload"

// observabilityApp is constructed at package load so the workload wires the same
// AppTheory observability hooks every cmd entrypoint must wire (gov-infra
// COM-6). The workload is a long-running HTTP server rather than an AppTheory
// request app, so the app is not used as a request router; its observability
// hooks establish the structured-logging posture for the process.
var observabilityApp = apptheory.New(apptheory.WithObservability(observability.New(serviceName)))

func main() {
	if observabilityApp == nil {
		// observability.New never returns nil, but fail loudly if it ever does.
		panic(serviceName + ": observability not initialized")
	}
	if err := run(); err != nil {
		slog.Error(serviceName+": fatal", slog.String("error", err.Error())) //nolint:gosec // G706: err is a structured slog attribute (JSON-encoded), not a log format string.
		os.Exit(1)
	}
}

func run() error {
	// Install the explicit-timeout provider HTTP client before any provider call
	// so every llm.StreamMintConversation* / MintConversationDeclarations* call
	// fails at the configured HTTP deadline, not the Lambda/MicroVM envelope
	// (kills G8).
	llm.ConfigureDefaultProviderHTTPClient()

	stateDB, err := store.LambdaInit()
	if err != nil {
		return errors.Join(errors.New("store init"), err)
	}
	st := store.New(stateDB)
	writer := completion.NewCompletionWriter(st, nil)
	runner := &turnRunner{store: st, writer: writer}

	server, err := newHookServer(runner, hostedgenesis.MicroVMNamespace)
	if err != nil {
		return err
	}

	addr := ":" + hookPort
	httpSrv := server.httpServer(addr)

	// Graceful shutdown on SIGTERM/SIGINT so the image's terminate hook can
	// drain in-flight turn execution without corrupting a half-written session.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		slog.Info(serviceName + ": shutdown signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	slog.Info(serviceName+": serving M16 lifecycle hooks", slog.String("addr", addr))
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
