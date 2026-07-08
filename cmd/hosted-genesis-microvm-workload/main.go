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
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
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

// microVMSTSAPI is the minimal STS surface used to exchange the platform-provided
// MicroVM guest credentials for Host's explicit MicroVM execution role. AWS
// GetMicrovm reports ExecutionRoleArn, but lab evidence showed the in-guest
// default credential chain was still backed by the image BuildRoleArn; the
// workload therefore assumes the configured execution role itself before any
// TableTheory/SSM client is initialized. Temporary credentials stay process-
// local, are never logged, and are used only by the default AWS SDK chain.
type microVMSTSAPI interface {
	AssumeRole(context.Context, *sts.AssumeRoleInput, ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

func configureMicroVMExecutionCredentials(ctx context.Context) error {
	roleArn := strings.TrimSpace(os.Getenv("HOSTED_GENESIS_MICROVM_EXECUTION_ROLE_ARN"))
	if roleArn == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	return assumeMicroVMExecutionRole(ctx, sts.NewFromConfig(cfg), roleArn)
}

func assumeMicroVMExecutionRole(ctx context.Context, api microVMSTSAPI, roleArn string) error {
	roleArn = strings.TrimSpace(roleArn)
	if roleArn == "" {
		return nil
	}
	if api == nil {
		return fmt.Errorf("sts client is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := api.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String("hosted-genesis-microvm-workload"),
		DurationSeconds: aws.Int32(3600),
	})
	if err != nil {
		return err
	}
	creds := stsCredentials(out)
	accessKeyID := aws.ToString(creds.AccessKeyId)
	secretAccessKey := aws.ToString(creds.SecretAccessKey)
	sessionToken := aws.ToString(creds.SessionToken)
	if accessKeyID == "" || secretAccessKey == "" || sessionToken == "" {
		return fmt.Errorf("assume role returned incomplete credentials")
	}
	// These are temporary in-process AWS credentials, not Host/customer secrets.
	// Do not log them; setting env lets TableTheory and internal/secrets use the
	// standard AWS SDK default chain without local framework patches.
	if err := os.Setenv("AWS_ACCESS_KEY_ID", accessKeyID); err != nil {
		return err
	}
	if err := os.Setenv("AWS_SECRET_ACCESS_KEY", secretAccessKey); err != nil {
		return err
	}
	if err := os.Setenv("AWS_SESSION_TOKEN", sessionToken); err != nil {
		return err
	}
	return nil
}

func stsCredentials(out *sts.AssumeRoleOutput) ststypes.Credentials {
	if out == nil || out.Credentials == nil {
		return ststypes.Credentials{}
	}
	return *out.Credentials
}

func run() error {
	if err := configureMicroVMExecutionCredentials(context.Background()); err != nil {
		return errors.Join(errors.New("configure execution credentials"), err)
	}

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

	// Bind explicitly before serving so a successful bind is confirmed with a
	// "listening" log line and a bind failure fails fast (clear error + non-zero
	// exit) instead of surfacing as a ~120s build timeout. The prior
	// "serving M16 lifecycle hooks" log was emitted BEFORE ListenAndServe, so it
	// did not prove the app was actually listening on :8080.
	if err := server.serveWithListener(httpSrv, addr); err != nil {
		return err
	}
	return nil
}
