package hostedgenesis

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

// noRunHookMicroVMRunAPI is the minimal AWS Lambda MicroVM SDK surface Host
// needs to start endpoint-driven hosted-genesis images. The image deliberately
// has no AWS image-level run hook enabled; the controller POSTs the AppTheory
// LifecycleEvent to the workload endpoint after the MicroVM reaches RUNNING.
type noRunHookMicroVMRunAPI interface {
	RunMicrovm(context.Context, *lambdamicrovms.RunMicrovmInput, ...func(*lambdamicrovms.Options)) (*lambdamicrovms.RunMicrovmOutput, error)
}

// noRunHookAWSLambdaMicroVMProvider preserves AppTheory's M16 Provider envelope
// while overriding only Run so Host can start no-hooks MicroVM images without a
// RunHookPayload. All other operations remain delegated to AppTheory's official
// provider, keeping token issuance, list/recovery, and state-changing commands
// inside the framework's safe tenant-bound surface.
type noRunHookAWSLambdaMicroVMProvider struct {
	runAPI   noRunHookMicroVMRunAPI
	delegate runtimemicrovm.Provider
}

var _ runtimemicrovm.Provider = (*noRunHookAWSLambdaMicroVMProvider)(nil)

// NewNoRunHookAWSLambdaMicroVMProvider returns the Host endpoint-start provider
// used by the hosted-genesis MicroVM controller. The raw SDK client is retained
// only inside this provider, and the provider still exposes only AppTheory's
// sanitized runtimemicrovm.Provider interface.
func NewNoRunHookAWSLambdaMicroVMProvider(runAPI noRunHookMicroVMRunAPI, delegate runtimemicrovm.Provider) (runtimemicrovm.Provider, error) {
	if runAPI == nil {
		return nil, errors.New("hosted genesis microvm no-run-hook provider requires run api")
	}
	if delegate == nil {
		return nil, errors.New("hosted genesis microvm no-run-hook provider requires delegate")
	}
	return &noRunHookAWSLambdaMicroVMProvider{runAPI: runAPI, delegate: delegate}, nil
}

// Run maps the AppTheory safe run request to AWS RunMicrovm without
// RunHookPayload. Passing RunHookPayload is invalid for Host's no-hooks image
// and AWS rejects it before a MicroVM is created.
func (p *noRunHookAWSLambdaMicroVMProvider) Run(ctx context.Context, input runtimemicrovm.ProviderRunInput) (runtimemicrovm.ProviderSession, error) {
	input = normalizeNoRunHookProviderRunInput(input)
	if err := runtimemicrovm.ValidateProviderRunInput(input); err != nil {
		return runtimemicrovm.ProviderSession{}, err
	}
	if p == nil || p.runAPI == nil {
		return runtimemicrovm.ProviderSession{}, providerOperationFailed(input.RequestID)
	}
	out, err := p.runAPI.RunMicrovm(ctxOrBackground(ctx), &lambdamicrovms.RunMicrovmInput{
		ImageIdentifier:          optionalString(input.ImageRef),
		ClientToken:              optionalString(input.RequestID),
		EgressNetworkConnectors:  noRunHookEgressConnectors(input),
		ExecutionRoleArn:         optionalString(input.ExecutionRoleArn),
		ImageVersion:             optionalString(input.ImageVersion),
		IngressNetworkConnectors: normalizeStringSlice(input.IngressNetworkConnectorRefs),
		IdlePolicy:               noRunHookIdlePolicy(input.IdlePolicy),
		MaximumDurationInSeconds: optionalInt32(input.MaximumDurationSeconds),
	})
	if err != nil {
		return runtimemicrovm.ProviderSession{}, providerOperationFailed(input.RequestID)
	}
	return noRunHookSessionFromRunOutput(input, out)
}

func (p *noRunHookAWSLambdaMicroVMProvider) Get(ctx context.Context, input runtimemicrovm.ProviderSessionInput) (runtimemicrovm.ProviderSession, error) {
	delegate, err := p.requireDelegate(input.RequestID)
	if err != nil {
		return runtimemicrovm.ProviderSession{}, err
	}
	return delegate.Get(ctx, input)
}

func (p *noRunHookAWSLambdaMicroVMProvider) List(ctx context.Context, input runtimemicrovm.ProviderListInput) (runtimemicrovm.ProviderListOutput, error) {
	delegate, err := p.requireDelegate(input.RequestID)
	if err != nil {
		return runtimemicrovm.ProviderListOutput{}, err
	}
	return delegate.List(ctx, input)
}

func (p *noRunHookAWSLambdaMicroVMProvider) Suspend(ctx context.Context, input runtimemicrovm.ProviderSessionInput) (runtimemicrovm.ProviderSession, error) {
	delegate, err := p.requireDelegate(input.RequestID)
	if err != nil {
		return runtimemicrovm.ProviderSession{}, err
	}
	return delegate.Suspend(ctx, input)
}

func (p *noRunHookAWSLambdaMicroVMProvider) Resume(ctx context.Context, input runtimemicrovm.ProviderSessionInput) (runtimemicrovm.ProviderSession, error) {
	delegate, err := p.requireDelegate(input.RequestID)
	if err != nil {
		return runtimemicrovm.ProviderSession{}, err
	}
	return delegate.Resume(ctx, input)
}

func (p *noRunHookAWSLambdaMicroVMProvider) Terminate(ctx context.Context, input runtimemicrovm.ProviderSessionInput) (runtimemicrovm.ProviderSession, error) {
	delegate, err := p.requireDelegate(input.RequestID)
	if err != nil {
		return runtimemicrovm.ProviderSession{}, err
	}
	return delegate.Terminate(ctx, input)
}

func (p *noRunHookAWSLambdaMicroVMProvider) CreateAuthToken(ctx context.Context, input runtimemicrovm.ProviderTokenInput) (runtimemicrovm.ProviderToken, error) {
	delegate, err := p.requireDelegate(input.RequestID)
	if err != nil {
		return runtimemicrovm.ProviderToken{}, err
	}
	return delegate.CreateAuthToken(ctx, input)
}

func (p *noRunHookAWSLambdaMicroVMProvider) CreateShellToken(ctx context.Context, input runtimemicrovm.ProviderTokenInput) (runtimemicrovm.ProviderToken, error) {
	delegate, err := p.requireDelegate(input.RequestID)
	if err != nil {
		return runtimemicrovm.ProviderToken{}, err
	}
	return delegate.CreateShellToken(ctx, input)
}

func (p *noRunHookAWSLambdaMicroVMProvider) requireDelegate(requestID string) (runtimemicrovm.Provider, error) {
	if p == nil || p.delegate == nil {
		return nil, providerOperationFailed(requestID)
	}
	return p.delegate, nil
}

func normalizeNoRunHookProviderRunInput(input runtimemicrovm.ProviderRunInput) runtimemicrovm.ProviderRunInput {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.Namespace = strings.TrimSpace(input.Namespace)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.ImageRef = strings.TrimSpace(input.ImageRef)
	input.ImageVersion = strings.TrimSpace(input.ImageVersion)
	input.NetworkConnectorRef = strings.TrimSpace(input.NetworkConnectorRef)
	input.IngressNetworkConnectorRefs = normalizeStringSlice(input.IngressNetworkConnectorRefs)
	input.EgressNetworkConnectorRefs = normalizeStringSlice(input.EgressNetworkConnectorRefs)
	input.ExecutionRoleArn = strings.TrimSpace(input.ExecutionRoleArn)
	input.AuthContext.Subject = strings.TrimSpace(input.AuthContext.Subject)
	input.AuthContext.TenantID = strings.TrimSpace(input.AuthContext.TenantID)
	input.AuthContext.Namespace = strings.TrimSpace(input.AuthContext.Namespace)
	return input
}

func noRunHookSessionFromRunOutput(input runtimemicrovm.ProviderRunInput, out *lambdamicrovms.RunMicrovmOutput) (runtimemicrovm.ProviderSession, error) {
	if out == nil {
		return runtimemicrovm.ProviderSession{}, providerOperationFailed(input.RequestID)
	}
	providerState := strings.ToLower(strings.TrimSpace(string(out.State)))
	state, terminal, err := runtimemicrovm.MapProviderState(providerState)
	if err != nil {
		return runtimemicrovm.ProviderSession{}, withSafeRequestID(err, input.RequestID)
	}
	session := runtimemicrovm.ProviderSession{
		TenantID:          input.TenantID,
		Namespace:         input.Namespace,
		SessionID:         input.SessionID,
		ProviderMicroVMID: strings.TrimSpace(aws.ToString(out.MicrovmId)),
		State:             state,
		ProviderState:     providerState,
		ImageRef:          strings.TrimSpace(aws.ToString(out.ImageArn)),
		ImageVersion:      strings.TrimSpace(aws.ToString(out.ImageVersion)),
		StartedAt:         timeFromPtr(out.StartedAt),
		TerminatedAt:      timeFromPtr(out.TerminatedAt),
		Terminal:          terminal,
	}
	if err := runtimemicrovm.ValidateProviderSession(session); err != nil {
		return runtimemicrovm.ProviderSession{}, withSafeRequestID(err, input.RequestID)
	}
	return session, nil
}

func noRunHookEgressConnectors(input runtimemicrovm.ProviderRunInput) []string {
	connectors := append([]string{}, input.EgressNetworkConnectorRefs...)
	if input.NetworkConnectorRef != "" {
		connectors = append(connectors, input.NetworkConnectorRef)
	}
	return normalizeStringSlice(connectors)
}

func noRunHookIdlePolicy(policy *runtimemicrovm.ProviderIdlePolicy) *lambdatypes.IdlePolicy {
	if policy == nil {
		return nil
	}
	return &lambdatypes.IdlePolicy{
		AutoResumeEnabled:        aws.Bool(policy.AutoResumeEnabled),
		MaxIdleDurationSeconds:   aws.Int32(policy.MaxIdleDurationSeconds),
		SuspendedDurationSeconds: aws.Int32(policy.SuspendedDurationSeconds),
	}
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return aws.String(value)
}

func optionalInt32(value int32) *int32 {
	if value <= 0 {
		return nil
	}
	return aws.Int32(value)
}

func timeFromPtr(value *time.Time) time.Time {
	if value == nil || value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func providerOperationFailed(requestID string) runtimemicrovm.SafeError {
	return runtimemicrovm.SafeError{
		Code:      runtimemicrovm.ErrorCodeProviderOperationFailed,
		Message:   "apptheory: microvm provider operation failed",
		RequestID: strings.TrimSpace(requestID),
	}
}

func withSafeRequestID(err error, requestID string) error {
	var safe runtimemicrovm.SafeError
	if errors.As(err, &safe) && safe.RequestID == "" {
		safe.RequestID = strings.TrimSpace(requestID)
		return safe
	}
	return err
}
