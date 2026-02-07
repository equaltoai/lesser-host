package provisionworker

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type fakeOrg struct {
	createOut   *organizations.CreateAccountOutput
	createErr   error
	describeOut *organizations.DescribeCreateAccountStatusOutput
	describeErr error

	parentsOut *organizations.ListParentsOutput
	parentsErr error

	moveErr error
}

func (f *fakeOrg) CreateAccount(_ context.Context, _ *organizations.CreateAccountInput, _ ...func(*organizations.Options)) (*organizations.CreateAccountOutput, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createOut, nil
}

func (f *fakeOrg) DescribeCreateAccountStatus(_ context.Context, _ *organizations.DescribeCreateAccountStatusInput, _ ...func(*organizations.Options)) (*organizations.DescribeCreateAccountStatusOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return f.describeOut, nil
}

func (f *fakeOrg) ListParents(_ context.Context, _ *organizations.ListParentsInput, _ ...func(*organizations.Options)) (*organizations.ListParentsOutput, error) {
	if f.parentsErr != nil {
		return nil, f.parentsErr
	}
	return f.parentsOut, nil
}

func (f *fakeOrg) MoveAccount(_ context.Context, _ *organizations.MoveAccountInput, _ ...func(*organizations.Options)) (*organizations.MoveAccountOutput, error) {
	if f.moveErr != nil {
		return nil, f.moveErr
	}
	return &organizations.MoveAccountOutput{}, nil
}

type fakeRoute53 struct {
	changeErr error

	lastChange *route53.ChangeResourceRecordSetsInput
}

func (f *fakeRoute53) ChangeResourceRecordSets(_ context.Context, in *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	f.lastChange = in
	if f.changeErr != nil {
		return nil, f.changeErr
	}
	return &route53.ChangeResourceRecordSetsOutput{}, nil
}

func (f *fakeRoute53) CreateHostedZone(_ context.Context, _ *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
	return &route53.CreateHostedZoneOutput{}, nil
}

func (f *fakeRoute53) GetHostedZone(_ context.Context, _ *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	return &route53.GetHostedZoneOutput{}, nil
}

func (f *fakeRoute53) ListHostedZonesByName(_ context.Context, _ *route53.ListHostedZonesByNameInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error) {
	return &route53.ListHostedZonesByNameOutput{}, nil
}

type fakeCodebuild struct {
	startOut *codebuild.StartBuildOutput
	startErr error

	batchOut *codebuild.BatchGetBuildsOutput
	batchErr error
}

func (f *fakeCodebuild) StartBuild(_ context.Context, _ *codebuild.StartBuildInput, _ ...func(*codebuild.Options)) (*codebuild.StartBuildOutput, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.startOut, nil
}

func (f *fakeCodebuild) BatchGetBuilds(_ context.Context, _ *codebuild.BatchGetBuildsInput, _ ...func(*codebuild.Options)) (*codebuild.BatchGetBuildsOutput, error) {
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	return f.batchOut, nil
}

func TestInitializeManagedProvisionJob_SetsDefaults(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	s := &Server{cfg: config.Config{
		ManagedDefaultRegion:       "us-east-1",
		ManagedParentHostedZoneID:  "ZPARENT",
		ManagedInstanceRoleName:    "role",
		ManagedParentDomain:        "example.com",
		ManagedProvisioningEnabled: true,
	}, store: st}

	job := &models.ProvisionJob{InstanceSlug: "slug"}
	s.initializeManagedProvisionJob(job)
	if job.Step != provisionStepQueued || job.Region != "us-east-1" || job.ParentHostedZoneID != "ZPARENT" || job.AccountRoleName != "role" {
		t.Fatalf("unexpected job defaults: %#v", job)
	}
	if job.BaseDomain != "slug.example.com" {
		t.Fatalf("unexpected base domain: %q", job.BaseDomain)
	}

	if got := managedBaseDomain(" demo ", ""); got != "demo.greater.website" {
		t.Fatalf("unexpected managedBaseDomain fallback: %q", got)
	}
}

func TestProvisionStateMachine_SuccessPathAcrossSteps(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	org := &fakeOrg{
		createOut: &organizations.CreateAccountOutput{CreateAccountStatus: &orgtypes.CreateAccountStatus{Id: aws.String("req1")}},
		describeOut: &organizations.DescribeCreateAccountStatusOutput{
			CreateAccountStatus: &orgtypes.CreateAccountStatus{
				State:     orgtypes.CreateAccountStateSucceeded,
				AccountId: aws.String("123456789012"),
			},
		},
		parentsOut: &organizations.ListParentsOutput{
			Parents: []orgtypes.Parent{{Id: aws.String("ou-source")}},
		},
	}
	r53 := &fakeRoute53{}

	stsClient := &fakeSTS{out: &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{
		AccessKeyId:     aws.String("a"),
		SecretAccessKey: aws.String("b"),
		SessionToken:    aws.String("c"),
	}}}

	cb := &fakeCodebuild{
		startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}},
		batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{
			BuildStatus: cbtypes.StatusTypeSucceeded,
			Logs:        &cbtypes.LogsLocation{DeepLink: aws.String(" link ")},
		}}},
	}

	s3Client := &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(`{"app":"x","base_domain":"d","account_id":"123456789012","region":"us-east-1","hosted_zone":{"id":"/hostedzone/Z1","name":"d."}}`))}}

	s := &Server{
		cfg: config.Config{
			ManagedProvisioningEnabled:          true,
			ManagedDefaultRegion:                "us-east-1",
			ManagedParentHostedZoneID:           "ZPARENT",
			ManagedParentDomain:                 "example.com",
			ManagedInstanceRoleName:             "role",
			ManagedAccountEmailTemplate:         "ops+{slug}@example.com",
			ManagedAccountNamePrefix:            "lesser-",
			ManagedTargetOrganizationalUnitID:   "ou-target",
			ManagedProvisionRunnerProjectName:   "proj",
			ManagedLesserGitHubOwner:            "o",
			ManagedLesserGitHubRepo:             "r",
			ArtifactBucketName:                  "bucket",
			ProvisionQueueURL:                   "https://sqs.us-east-1.amazonaws.com/123/q",
			ManagedOrgVendingRoleARN:            "",
		},
		store: st,
		org:   org,
		r53:   r53,
		sts:   stsClient,
		cb:    cb,
		s3:    s3Client,
	}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug:  "demo",
		Status:        models.ProvisionJobStatusRunning,
		Step:          provisionStepQueued,
		MaxAttempts:   3,
		CreatedAt:     now.Add(-1 * time.Minute),
		UpdatedAt:     now.Add(-1 * time.Minute),
		ExpiresAt:     now.Add(1 * time.Hour),
		LesserVersion: "v",
	}

	// Initialize missing fields for downstream steps.
	s.initializeManagedProvisionJob(job)

	if _, _, err := s.advanceProvisionQueued(context.Background(), job, "req", now); err != nil {
		t.Fatalf("advanceProvisionQueued: %v", err)
	}
	if job.Step != provisionStepAccountCreate {
		t.Fatalf("expected account.create, got %q", job.Step)
	}

	delay, _, err := s.advanceProvisionAccountCreate(context.Background(), job, "req", now)
	if err != nil || delay != provisionDefaultPollDelay || job.Step != provisionStepAccountCreatePoll || job.AccountRequestID != "req1" {
		t.Fatalf("unexpected account create: delay=%v step=%q req=%q err=%v", delay, job.Step, job.AccountRequestID, err)
	}

	delay, _, err = s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
	if err != nil || delay != 0 || job.Step != provisionStepAccountMove || strings.TrimSpace(job.AccountID) == "" {
		t.Fatalf("unexpected account poll: delay=%v step=%q acc=%q err=%v", delay, job.Step, job.AccountID, err)
	}

	// Move to target OU then advance to assume role.
	delay, _, err = s.advanceProvisionAccountMove(context.Background(), job, "req", now)
	if err != nil || delay != 0 || job.Step != provisionStepAssumeRole {
		t.Fatalf("unexpected account move: delay=%v step=%q err=%v", delay, job.Step, err)
	}

	// Assume role advances to child zone.
	delay, _, err = s.advanceProvisionAssumeRole(context.Background(), job, "req", now)
	if err != nil || delay != 0 || job.Step != provisionStepChildZone {
		t.Fatalf("unexpected assume role: delay=%v step=%q err=%v", delay, job.Step, err)
	}

	// Pre-populate child zone info so Route53 calls are skipped.
	job.ChildHostedZoneID = "ZCHILD"
	job.ChildNameServers = []string{"ns-1", "ns-2", "ns-1"}
	delay, _, err = s.advanceProvisionChildZone(context.Background(), job, "req", now)
	if err != nil || delay != 0 || job.Step != provisionStepParentDelegation || job.ChildHostedZoneID != "ZCHILD" {
		t.Fatalf("unexpected child zone: delay=%v step=%q zone=%q err=%v", delay, job.Step, job.ChildHostedZoneID, err)
	}

	delay, _, err = s.advanceProvisionParentDelegation(context.Background(), job, "req", now)
	if err != nil || delay != 0 || job.Step != provisionStepDeployStart {
		t.Fatalf("unexpected parent delegation: delay=%v step=%q err=%v", delay, job.Step, err)
	}

	delay, _, err = s.advanceProvisionDeployStart(context.Background(), job, "req", now)
	if err != nil || delay != provisionDefaultPollDelay || job.Step != provisionStepDeployWait || job.RunID != "run1" {
		t.Fatalf("unexpected deploy start: delay=%v step=%q run=%q err=%v", delay, job.Step, job.RunID, err)
	}

	delay, _, err = s.advanceProvisionDeployWait(context.Background(), job, "req", now)
	if err != nil || delay != 0 || job.Step != provisionStepReceiptIngest {
		t.Fatalf("unexpected deploy wait: delay=%v step=%q err=%v", delay, job.Step, err)
	}

	delay, done, err := s.advanceProvisionReceiptIngest(context.Background(), job, "req", now)
	if err != nil || delay != 0 || !done || job.Step != provisionStepDone || job.Status != models.ProvisionJobStatusOK {
		t.Fatalf("unexpected receipt ingest: delay=%v done=%v step=%q status=%q err=%v", delay, done, job.Step, job.Status, err)
	}

	// upsertParentNSDelegation handles trim + dedupe.
	if err := s.upsertParentNSDelegation(context.Background(), "ZPARENT", "demo.example.com", []string{" ns-1 ", "", "ns-1"}); err != nil {
		t.Fatalf("upsertParentNSDelegation: %v", err)
	}

	if r53.lastChange == nil || r53.lastChange.ChangeBatch == nil || len(r53.lastChange.ChangeBatch.Changes) != 1 {
		t.Fatalf("expected route53 change, got %#v", r53.lastChange)
	}
	if r53.lastChange.ChangeBatch.Changes[0].ResourceRecordSet == nil || r53.lastChange.ChangeBatch.Changes[0].ResourceRecordSet.Type != r53types.RRTypeNs {
		t.Fatalf("unexpected record type: %#v", r53.lastChange)
	}
}
