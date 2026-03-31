package provisionworker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type fakeSQS struct {
	inputs []*sqs.SendMessageInput
	err    error
}

func (f *fakeSQS) SendMessage(_ context.Context, params *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.inputs = append(f.inputs, params)
	if f.err != nil {
		return nil, f.err
	}
	return &sqs.SendMessageOutput{MessageId: aws.String("m1")}, nil
}

type fakeSTS struct {
	out *sts.AssumeRoleOutput
	err error

	lastArn  string
	lastName string
}

func (f *fakeSTS) AssumeRole(_ context.Context, params *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	if params != nil {
		f.lastArn = aws.ToString(params.RoleArn)
		f.lastName = aws.ToString(params.RoleSessionName)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

type fakeS3 struct {
	out *s3.GetObjectOutput
	err error

	byKey map[string]*s3.GetObjectOutput
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.byKey != nil && in != nil && in.Key != nil {
		if out, ok := f.byKey[*in.Key]; ok {
			return out, nil
		}
	}
	return f.out, nil
}

func TestProvisionWorker_HelperFunctions(t *testing.T) {
	t.Parallel()

	if got := expandManagedAccountEmailTemplate("", "demo"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := expandManagedAccountEmailTemplate("ops+{slug}@example.com", " demo "); got != "ops+demo@example.com" {
		t.Fatalf("unexpected template expansion: %q", got)
	}

	if !isRetryableAssumeRoleErr(errors.New("AccessDenied")) {
		t.Fatalf("expected retryable")
	}
	if isRetryableAssumeRoleErr(nil) {
		t.Fatalf("expected false for nil")
	}
}

func TestAssumeInstanceRole_ValidationAndRetryableError(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{}}
	if _, _, err := s.assumeInstanceRole(context.Background(), "1", "role", "s", "j"); err == nil {
		t.Fatalf("expected error without sts client")
	}

	s.sts = &fakeSTS{err: errors.New("AccessDenied: not ready")}
	if _, delay, err := s.assumeInstanceRole(context.Background(), "123", "role", "slug", "job"); !errors.Is(err, errAssumeRoleNotReady) || delay != provisionDefaultPollDelay {
		t.Fatalf("expected assume role not ready, got err=%v delay=%v", err, delay)
	}

	f := &fakeSTS{out: &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{AccessKeyId: aws.String("a"), SecretAccessKey: aws.String("b"), SessionToken: aws.String("c")}}}
	s.sts = f
	out, delay, err := s.assumeInstanceRole(context.Background(), "123", "role", strings.Repeat("x", 80), strings.Repeat("y", 80))
	if err != nil || delay != 0 || out == nil {
		t.Fatalf("unexpected: out=%#v delay=%v err=%v", out, delay, err)
	}
	if !strings.Contains(f.lastArn, "arn:aws:iam::123:role/role") {
		t.Fatalf("unexpected role arn: %q", f.lastArn)
	}
	if len(f.lastName) > 64 {
		t.Fatalf("expected session name truncated, got %d", len(f.lastName))
	}
}

func TestRequeueProvisionJob_ValidatesAndClampsDelay(t *testing.T) {
	t.Parallel()

	if err := (&Server{}).requeueProvisionJob(context.Background(), "j", 0); err == nil {
		t.Fatalf("expected error without sqs client")
	}

	f := &fakeSQS{}
	s := &Server{cfg: config.Config{ProvisionQueueURL: "url"}, sqs: f}
	if err := s.requeueProvisionJob(context.Background(), "", 0); err != nil {
		t.Fatalf("expected nil for empty job id, got %v", err)
	}

	if err := s.requeueProvisionJob(context.Background(), "j1", -10*time.Second); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(f.inputs) != 1 || f.inputs[0] == nil || f.inputs[0].DelaySeconds != 0 {
		t.Fatalf("expected delay clamped to 0, got %#v", f.inputs)
	}

	if err := s.requeueProvisionJob(context.Background(), "j2", 2000*time.Second); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(f.inputs) != 2 || f.inputs[1].DelaySeconds != 900 {
		t.Fatalf("expected delay clamped to 900, got %#v", f.inputs[1])
	}
}

func TestLoadReceiptFromS3_ValidatesAndParses(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if _, _, err := s.loadReceiptFromS3(context.Background(), "b", "k"); err == nil {
		t.Fatalf("expected error without s3 client")
	}

	s = &Server{s3: &fakeS3{err: errors.New("nope")}}
	if _, _, err := s.loadReceiptFromS3(context.Background(), "b", "k"); err == nil {
		t.Fatalf("expected error from GetObject")
	}

	s = &Server{s3: &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(" "))}}}
	if _, _, err := s.loadReceiptFromS3(context.Background(), "b", "k"); err == nil {
		t.Fatalf("expected empty receipt error")
	}

	s = &Server{s3: &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader("{"))}}}
	if raw, rec, err := s.loadReceiptFromS3(context.Background(), "b", "k"); err == nil || raw == "" || rec != nil {
		t.Fatalf("expected json error with raw, got raw=%q rec=%v err=%v", raw, rec, err)
	}

	// Missing required fields.
	s = &Server{s3: &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(`{"app":"x"}`))}}}
	if _, rec, err := s.loadReceiptFromS3(context.Background(), "b", "k"); err == nil || rec == nil {
		t.Fatalf("expected missing fields error")
	}

	s = &Server{s3: &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(`{"app":"x","base_domain":"d","managed_deploy_artifacts":{"mode":"release","release":{"name":"lesser","version":"v1.2.4","git_sha":"abc123"},"deploy_artifact":{"kind":"lambda_bundle","manifest_path":"lesser-lambda-bundle.json"}}}`))}}}
	_, rec, err := s.loadReceiptFromS3(context.Background(), "b", "k")
	if err != nil || rec == nil || rec.App != "x" {
		t.Fatalf("expected success, rec=%#v err=%v", rec, err)
	}
	if rec.ManagedDeployArtifacts == nil || rec.ManagedDeployArtifacts.Release.Version != "v1.2.4" {
		t.Fatalf("expected managed deploy artifacts to parse, got %#v", rec.ManagedDeployArtifacts)
	}
}

func TestLoadBodyAndMCPReceipts_ParseManagedDeployArtifacts(t *testing.T) {
	t.Parallel()

	bodyJSON := `{"version":1,"stage":"dev","base_domain":"demo.example.com","lesser_body_version":"v0.2.2","managed_deploy_artifacts":{"mode":"release","checksums_path":"checksums.txt","release_manifest_path":"lesser-body-release.json","release":{"name":"lesser-body","version":"v0.2.2","git_sha":"bodysha","source_checkout_required":false,"npm_install_required":false},"deploy_artifact":{"kind":"lesser_body_managed_deploy","path":"lesser-body.zip","manifest_path":"lesser-body-deploy.json","script_path":"deploy-lesser-body-from-release.sh","template_path":"lesser-body-managed-dev.template.json"}}}`
	mcpJSON := `{"version":1,"stage":"dev","base_domain":"demo.example.com","lesser_body_version":"v0.2.2","mcp_url":"https://api.dev.example.com/mcp/{actor}","mcp_lambda_arn":"arn:aws:lambda:us-east-1:123:function:mcp","managed_deploy_artifacts":{"mode":"release","checksums_path":"checksums.txt","release_manifest_path":"lesser-release.json","release":{"name":"lesser","version":"v1.2.4","git_sha":"lessersha"},"deploy_artifact":{"kind":"lambda_bundle","path":"lesser-lambda-bundle.tar.gz","manifest_path":"lesser-lambda-bundle.json","files":["bin/api.zip"],"prepared_at":"2026-03-30T01:00:00Z"}}}`

	s := &Server{s3: &fakeS3{byKey: map[string]*s3.GetObjectOutput{
		"body": {Body: io.NopCloser(strings.NewReader(bodyJSON))},
		"mcp":  {Body: io.NopCloser(strings.NewReader(mcpJSON))},
	}}}

	_, bodyReceipt, err := s.loadBodyReceiptFromS3(context.Background(), "b", "body")
	if err != nil || bodyReceipt == nil {
		t.Fatalf("expected lesser-body receipt success, got receipt=%#v err=%v", bodyReceipt, err)
	}
	if bodyReceipt.ManagedDeployArtifacts == nil || bodyReceipt.ManagedDeployArtifacts.DeployArtifact.TemplatePath != "lesser-body-managed-dev.template.json" {
		t.Fatalf("expected parsed lesser-body managed deploy artifacts, got %#v", bodyReceipt.ManagedDeployArtifacts)
	}
	if bodyReceipt.ManagedDeployArtifacts.Release.SourceCheckoutRequired == nil || *bodyReceipt.ManagedDeployArtifacts.Release.SourceCheckoutRequired {
		t.Fatalf("expected lesser-body source_checkout_required=false, got %#v", bodyReceipt.ManagedDeployArtifacts.Release.SourceCheckoutRequired)
	}

	_, mcpReceipt, err := s.loadMCPReceiptFromS3(context.Background(), "b", "mcp")
	if err != nil || mcpReceipt == nil {
		t.Fatalf("expected mcp receipt success, got receipt=%#v err=%v", mcpReceipt, err)
	}
	if mcpReceipt.ManagedDeployArtifacts == nil || len(mcpReceipt.ManagedDeployArtifacts.DeployArtifact.Files) != 1 {
		t.Fatalf("expected parsed mcp managed deploy artifacts, got %#v", mcpReceipt.ManagedDeployArtifacts)
	}
	if mcpReceipt.McpLambdaARN == "" {
		t.Fatalf("expected mcp lambda arn to remain available, got %#v", mcpReceipt)
	}
}

func TestStringHelpersAndBackoff(t *testing.T) {
	t.Parallel()

	if got := ensureTrailingDot("example.com"); got != "example.com." {
		t.Fatalf("unexpected: %q", got)
	}
	if got := normalizeHostedZoneID("/hostedzone/Z1"); got != "Z1" {
		t.Fatalf("unexpected: %q", got)
	}

	ns := normalizeNameServers([]string{" b ", "", "a", "a"})
	if len(ns) != 2 || ns[0] != "a" || ns[1] != "b" {
		t.Fatalf("unexpected name servers: %#v", ns)
	}

	if got := compactErr(errors.New("")); got != "unknown error" {
		t.Fatalf("unexpected: %q", got)
	}
	if got := compactErr(errors.New(strings.Repeat("x", 400))); len(got) <= 350 {
		t.Fatalf("expected truncation, got len=%d", len(got))
	}
	if got := sanitizeOperatorVisibleFailureDetail("COMMAND_EXECUTION_ERROR: Error while executing command: bash ./deploy-lesser-body-from-release.sh --stack-name demo Reason: exit status 1"); got != "command execution failed (exit status 1)" {
		t.Fatalf("unexpected sanitized command failure: %q", got)
	}
	if got := sanitizeOperatorVisibleFailureDetail("BUILD -- failed\nwith /*comment*/ scripts"); strings.Contains(got, "--") || strings.Contains(got, "/*") || strings.Contains(got, "*/") {
		t.Fatalf("expected dangerous patterns sanitized, got %q", got)
	}

	if got := jitteredBackoff(0, 1*time.Second, 10*time.Second); got != 1*time.Second {
		t.Fatalf("unexpected: %v", got)
	}
	if got := jitteredBackoff(10, 1*time.Second, 10*time.Second); got != 10*time.Second {
		t.Fatalf("expected capped, got %v", got)
	}
}

func TestCodebuildStatusHelpers(t *testing.T) {
	t.Parallel()

	build := cbtypes.Build{Logs: &cbtypes.LogsLocation{DeepLink: aws.String("  link ")}}
	if got := codebuildBuildDeepLink(build); got != "link" {
		t.Fatalf("unexpected deep link: %q", got)
	}

	if got := normalizeCodebuildStatus(cbtypes.StatusTypeInProgress); got != codebuildStatusInProgress {
		t.Fatalf("unexpected status: %q", got)
	}
	if got := normalizeCodebuildStatus(cbtypes.StatusType(" ")); got != codebuildStatusUnknown {
		t.Fatalf("unexpected status: %q", got)
	}
}

func TestReceiptAndBootstrapKeys(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if got := s.receiptS3Key(nil); got != "" {
		t.Fatalf("expected empty")
	}

	job := &models.ProvisionJob{ID: "j", InstanceSlug: "slug"}
	if got := s.receiptS3Key(job); !strings.Contains(got, "managed/provisioning/slug/j/state.json") {
		t.Fatalf("unexpected receipt key: %q", got)
	}
	if got := s.bootstrapS3Key(job); !strings.Contains(got, "managed/provisioning/slug/bootstrap.json") {
		t.Fatalf("unexpected bootstrap key: %q", got)
	}
}

func TestProcessProvisionJob_DisabledAndMissingConfig(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()

	var loaded *models.ProvisionJob
	qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{ID: "j1", InstanceSlug: "slug", Status: models.ProvisionJobStatusQueued}
		loaded = dest
	}).Once()

	st := store.New(db)
	srv := &Server{cfg: config.Config{ManagedProvisioningEnabled: false}, store: st}
	if err := srv.processProvisionJob(context.Background(), "req", "j1"); err != nil {
		t.Fatalf("processProvisionJob: %v", err)
	}
	if loaded == nil || strings.TrimSpace(loaded.Status) != models.ProvisionJobStatusError {
		t.Fatalf("expected job failed, got %#v", loaded)
	}

	// Missing config triggers failJob as well.
	qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{ID: "j2", InstanceSlug: "slug", Status: models.ProvisionJobStatusQueued}
		loaded = dest
	}).Once()
	srv.cfg.ManagedProvisioningEnabled = true
	if err := srv.processProvisionJob(context.Background(), "req", "j2"); err != nil {
		t.Fatalf("processProvisionJob: %v", err)
	}
	if loaded == nil || strings.TrimSpace(loaded.ErrorCode) != "missing_config" {
		t.Fatalf("expected missing_config error, got %#v", loaded)
	}
}

func TestHandleProvisionQueueMessage_ValidKindCallsProcess(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()
	qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(theoryErrors.ErrItemNotFound).Once()

	st := store.New(db)
	srv := &Server{cfg: config.Config{}, store: st}

	body := `{"kind":"provision_job","job_id":"j1"}`
	evctx := &apptheory.EventContext{RequestID: "r1"}
	if err := srv.handleProvisionQueueMessage(evctx, events.SQSMessage{Body: body}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
