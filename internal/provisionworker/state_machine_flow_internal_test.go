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
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type fakeOrg struct {
	createOut   *organizations.CreateAccountOutput
	createErr   error
	describeOut *organizations.DescribeCreateAccountStatusOutput
	describeErr error
	listOut     *organizations.ListAccountsOutput
	listErr     error

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

func (f *fakeOrg) ListAccounts(_ context.Context, _ *organizations.ListAccountsInput, _ ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOut != nil {
		return f.listOut, nil
	}
	return &organizations.ListAccountsOutput{}, nil
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

type fakeSecretsManager struct {
	describeOut *secretsmanager.DescribeSecretOutput
	describeErr error

	getOut *secretsmanager.GetSecretValueOutput
	getErr error

	createOut *secretsmanager.CreateSecretOutput
	createErr error

	updateOut *secretsmanager.UpdateSecretOutput
	updateErr error

	tagOut *secretsmanager.TagResourceOutput
	tagErr error

	untagOut *secretsmanager.UntagResourceOutput
	untagErr error
}

func (f *fakeSecretsManager) CreateSecret(_ context.Context, _ *secretsmanager.CreateSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createOut, nil
}

func (f *fakeSecretsManager) DescribeSecret(_ context.Context, _ *secretsmanager.DescribeSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return f.describeOut, nil
}

func (f *fakeSecretsManager) GetSecretValue(_ context.Context, _ *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getOut, nil
}

func (f *fakeSecretsManager) UpdateSecret(_ context.Context, _ *secretsmanager.UpdateSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.UpdateSecretOutput, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.updateOut != nil {
		return f.updateOut, nil
	}
	return &secretsmanager.UpdateSecretOutput{}, nil
}

func (f *fakeSecretsManager) TagResource(_ context.Context, _ *secretsmanager.TagResourceInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.TagResourceOutput, error) {
	if f.tagErr != nil {
		return nil, f.tagErr
	}
	if f.tagOut != nil {
		return f.tagOut, nil
	}
	return &secretsmanager.TagResourceOutput{}, nil
}

func (f *fakeSecretsManager) UntagResource(_ context.Context, _ *secretsmanager.UntagResourceInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.UntagResourceOutput, error) {
	if f.untagErr != nil {
		return nil, f.untagErr
	}
	if f.untagOut != nil {
		return f.untagOut, nil
	}
	return &secretsmanager.UntagResourceOutput{}, nil
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
	db.On("WithContext", mock.Anything).Return(db).Maybe()

	qInst := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()

	instanceLoads := 0
	translationEnabled := true
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		instanceLoads++
		if instanceLoads == 1 {
			*dest = models.Instance{Slug: "demo"}
			return
		}
		*dest = models.Instance{
			Slug:                           "demo",
			LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
			LesserHostBaseURL:              "https://lab.lesser.host",
			LesserHostAttestationsURL:      "https://lab.lesser.host",
			TranslationEnabled:             &translationEnabled,
		}
	}).Maybe()

	qKey := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()

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

	sm := &fakeSecretsManager{
		describeErr: &smtypes.ResourceNotFoundException{},
		createOut:   &secretsmanager.CreateSecretOutput{ARN: aws.String("arn:aws:secretsmanager:us-east-1:123456789012:secret:test")},
	}

	s := &Server{
		cfg: config.Config{
			Stage:                             "lab",
			WebAuthnRPID:                      "lesser.host",
			ManagedProvisioningEnabled:        true,
			ManagedDefaultRegion:              "us-east-1",
			ManagedParentHostedZoneID:         "ZPARENT",
			ManagedParentDomain:               "example.com",
			ManagedInstanceRoleName:           "role",
			ManagedAccountEmailTemplate:       "ops+{slug}@example.com",
			ManagedAccountNamePrefix:          "lesser-",
			ManagedTargetOrganizationalUnitID: "ou-target",
			ManagedProvisionRunnerProjectName: "proj",
			ManagedLesserGitHubOwner:          "o",
			ManagedLesserGitHubRepo:           "r",
			ArtifactBucketName:                "bucket",
			ProvisionQueueURL:                 "https://sqs.us-east-1.amazonaws.com/123/q",
			ManagedOrgVendingRoleARN:          "",
		},
		store: st,
		org:   org,
		r53:   r53,
		sts:   stsClient,
		cb:    cb,
		s3:    s3Client,
		smFactory: func(context.Context, string, string, string, string, string) (secretsManagerAPI, error) {
			return sm, nil
		},
	}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:                 "j1",
		InstanceSlug:       "demo",
		Status:             models.ProvisionJobStatusRunning,
		Step:               provisionStepQueued,
		MaxAttempts:        3,
		CreatedAt:          now.Add(-1 * time.Minute),
		UpdatedAt:          now.Add(-1 * time.Minute),
		ExpiresAt:          now.Add(1 * time.Hour),
		Stage:              "lab",
		LesserVersion:      "v",
		BodyEnabled:        true,
		AdminUsername:      "demo",
		AdminWalletType:    "ethereum",
		AdminWalletAddr:    "0x0000000000000000000000000000000000000003",
		AdminWalletChainID: 1,
	}

	// Initialize missing fields for downstream steps.
	s.initializeManagedProvisionJob(job)

	_, _, err := s.advanceProvisionQueued(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionStepAccountCreate, job.Step)

	delay, _, err := s.advanceProvisionAccountCreate(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepAccountCreatePoll, job.Step)
	require.Equal(t, "req1", job.AccountRequestID)

	delay, _, err = s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepAccountMove, job.Step)
	require.NotEmpty(t, strings.TrimSpace(job.AccountID))

	// Move to target OU then advance to assume role.
	delay, _, err = s.advanceProvisionAccountMove(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepAssumeRole, job.Step)

	// Assume role advances to child zone.
	delay, _, err = s.advanceProvisionAssumeRole(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepChildZone, job.Step)

	// Pre-populate child zone info so Route53 calls are skipped.
	job.ChildHostedZoneID = "ZCHILD"
	job.ChildNameServers = []string{"ns-1", "ns-2", "ns-1"}
	delay, _, err = s.advanceProvisionChildZone(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepParentDelegation, job.Step)
	require.Equal(t, "ZCHILD", job.ChildHostedZoneID)

	delay, _, err = s.advanceProvisionParentDelegation(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepInstanceConfig, job.Step)

	delay, _, err = s.advanceProvisionInstanceConfig(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepDeployStart, job.Step)

	delay, _, err = s.advanceProvisionDeployStart(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepDeployWait, job.Step)
	require.Equal(t, "run1", job.RunID)

	delay, _, err = s.advanceProvisionDeployWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepReceiptIngest, job.Step)

	delay, done, err := s.advanceProvisionReceiptIngest(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.False(t, done)
	require.Equal(t, provisionStepBodyDeployStart, job.Step)

	delay, _, err = s.advanceProvisionBodyDeployStart(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepBodyDeployWait, job.Step)
	require.Equal(t, "run1", job.RunID)

	delay, _, err = s.advanceProvisionBodyDeployWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepDeployMcpStart, job.Step)
	require.Equal(t, now, job.BodyProvisionedAt)

	delay, _, err = s.advanceProvisionDeployMcpStart(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepDeployMcpWait, job.Step)

	delay, done, err = s.advanceProvisionDeployMcpWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.True(t, done)
	require.Equal(t, provisionStepDone, job.Step)
	require.Equal(t, models.ProvisionJobStatusOK, job.Status)
	require.Equal(t, now, job.McpWiredAt)

	// upsertParentNSDelegation handles trim + dedupe.
	require.NoError(t, s.upsertParentNSDelegation(context.Background(), "ZPARENT", "demo.example.com", []string{" ns-1 ", "", "ns-1"}))

	require.NotNil(t, r53.lastChange)
	require.NotNil(t, r53.lastChange.ChangeBatch)
	require.Len(t, r53.lastChange.ChangeBatch.Changes, 1)
	require.NotNil(t, r53.lastChange.ChangeBatch.Changes[0].ResourceRecordSet)
	require.Equal(t, r53types.RRTypeNs, r53.lastChange.ChangeBatch.Changes[0].ResourceRecordSet.Type)
}

func TestProvisionStateMachine_SoulEnabled_SuccessPathAcrossSteps(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()

	qInst := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()

	instanceLoads := 0
	translationEnabled := true
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		instanceLoads++
		if instanceLoads == 1 {
			*dest = models.Instance{Slug: "demo"}
			return
		}
		*dest = models.Instance{
			Slug:                           "demo",
			LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
			LesserHostBaseURL:              "https://lab.lesser.host",
			LesserHostAttestationsURL:      "https://lab.lesser.host",
			TranslationEnabled:             &translationEnabled,
		}
	}).Maybe()

	qKey := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()

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

	lesserReceipt := `{"app":"x","base_domain":"d","account_id":"123456789012","region":"us-east-1","hosted_zone":{"id":"/hostedzone/Z1","name":"d."}}`
	soulReceipt := `{"version":1,"app":"lesser-soul","instance_domain":"dev.demo.example.com","soul_version":"main"}`
	s3Client := &fakeS3{byKey: map[string]*s3.GetObjectOutput{
		"managed/provisioning/demo/j1/state.json":      {Body: io.NopCloser(strings.NewReader(lesserReceipt))},
		"managed/provisioning/demo/j1/soul-state.json": {Body: io.NopCloser(strings.NewReader(soulReceipt))},
	}}

	sm := &fakeSecretsManager{
		describeErr: &smtypes.ResourceNotFoundException{},
		createOut:   &secretsmanager.CreateSecretOutput{ARN: aws.String("arn:aws:secretsmanager:us-east-1:123456789012:secret:test")},
	}

	s := &Server{
		cfg: config.Config{
			Stage:                             "lab",
			WebAuthnRPID:                      "lesser.host",
			ManagedProvisioningEnabled:        true,
			ManagedDefaultRegion:              "us-east-1",
			ManagedParentHostedZoneID:         "ZPARENT",
			ManagedParentDomain:               "example.com",
			ManagedInstanceRoleName:           "role",
			ManagedAccountEmailTemplate:       "ops+{slug}@example.com",
			ManagedAccountNamePrefix:          "lesser-",
			ManagedTargetOrganizationalUnitID: "ou-target",
			ManagedProvisionRunnerProjectName: "proj",
			ManagedLesserGitHubOwner:          "o",
			ManagedLesserGitHubRepo:           "r",
			ArtifactBucketName:                "bucket",
			ProvisionQueueURL:                 "https://sqs.us-east-1.amazonaws.com/123/q",
			ManagedOrgVendingRoleARN:          "",
		},
		store: st,
		org:   org,
		r53:   r53,
		sts:   stsClient,
		cb:    cb,
		s3:    s3Client,
		smFactory: func(context.Context, string, string, string, string, string) (secretsManagerAPI, error) {
			return sm, nil
		},
	}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:                 "j1",
		InstanceSlug:       "demo",
		Status:             models.ProvisionJobStatusRunning,
		Step:               provisionStepQueued,
		MaxAttempts:        3,
		CreatedAt:          now.Add(-1 * time.Minute),
		UpdatedAt:          now.Add(-1 * time.Minute),
		ExpiresAt:          now.Add(1 * time.Hour),
		SoulEnabled:        true,
		BodyEnabled:        true,
		Stage:              "lab",
		LesserVersion:      "v",
		AdminUsername:      "demo",
		AdminWalletType:    "ethereum",
		AdminWalletAddr:    "0x0000000000000000000000000000000000000003",
		AdminWalletChainID: 1,
	}

	// Initialize missing fields for downstream steps.
	s.initializeManagedProvisionJob(job)

	_, _, err := s.advanceProvisionQueued(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionStepAccountCreate, job.Step)

	delay, _, err := s.advanceProvisionAccountCreate(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepAccountCreatePoll, job.Step)
	require.Equal(t, "req1", job.AccountRequestID)

	delay, _, err = s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepAccountMove, job.Step)
	require.NotEmpty(t, strings.TrimSpace(job.AccountID))

	// Move to target OU then advance to assume role.
	delay, _, err = s.advanceProvisionAccountMove(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepAssumeRole, job.Step)

	// Assume role advances to child zone.
	delay, _, err = s.advanceProvisionAssumeRole(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepChildZone, job.Step)

	// Pre-populate child zone info so Route53 calls are skipped.
	job.ChildHostedZoneID = "ZCHILD"
	job.ChildNameServers = []string{"ns-1", "ns-2", "ns-1"}
	delay, _, err = s.advanceProvisionChildZone(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepParentDelegation, job.Step)
	require.Equal(t, "ZCHILD", job.ChildHostedZoneID)

	delay, _, err = s.advanceProvisionParentDelegation(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepInstanceConfig, job.Step)

	delay, _, err = s.advanceProvisionInstanceConfig(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepDeployStart, job.Step)

	delay, _, err = s.advanceProvisionDeployStart(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepDeployWait, job.Step)
	require.Equal(t, "run1", job.RunID)

	delay, _, err = s.advanceProvisionDeployWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepReceiptIngest, job.Step)

	delay, done, err := s.advanceProvisionReceiptIngest(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.False(t, done)
	require.Equal(t, provisionStepBodyDeployStart, job.Step)

	delay, _, err = s.advanceProvisionBodyDeployStart(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepBodyDeployWait, job.Step)

	delay, _, err = s.advanceProvisionBodyDeployWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepDeployMcpStart, job.Step)
	require.Equal(t, now, job.BodyProvisionedAt)

	delay, _, err = s.advanceProvisionDeployMcpStart(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepDeployMcpWait, job.Step)

	delay, done, err = s.advanceProvisionDeployMcpWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.False(t, done)
	require.Equal(t, provisionStepSoulDeployStart, job.Step)
	require.Equal(t, now, job.McpWiredAt)

	delay, _, err = s.advanceProvisionSoulDeployStart(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepSoulDeployWait, job.Step)

	delay, _, err = s.advanceProvisionSoulDeployWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepSoulInitStart, job.Step)

	delay, _, err = s.advanceProvisionSoulInitStart(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, provisionStepSoulInitWait, job.Step)

	delay, _, err = s.advanceProvisionSoulInitWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, provisionStepSoulReceiptIngest, job.Step)

	delay, done, err = s.advanceProvisionSoulReceiptIngest(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.True(t, done)
	require.Equal(t, provisionStepDone, job.Step)
	require.Equal(t, models.ProvisionJobStatusOK, job.Status)
	require.Equal(t, now, job.SoulProvisionedAt)
	require.Equal(t, soulReceipt, job.SoulReceiptJSON)

	// upsertParentNSDelegation handles trim + dedupe.
	require.NoError(t, s.upsertParentNSDelegation(context.Background(), "ZPARENT", "demo.example.com", []string{" ns-1 ", "", "ns-1"}))

	require.NotNil(t, r53.lastChange)
	require.NotNil(t, r53.lastChange.ChangeBatch)
	require.Len(t, r53.lastChange.ChangeBatch.Changes, 1)
	require.NotNil(t, r53.lastChange.ChangeBatch.Changes[0].ResourceRecordSet)
	require.Equal(t, r53types.RRTypeNs, r53.lastChange.ChangeBatch.Changes[0].ResourceRecordSet.Type)
}
