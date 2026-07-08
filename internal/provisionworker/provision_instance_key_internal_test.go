package provisionworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const provisionTestManagedInstanceKeyID = "38ed91d202121369e6ad8f501c2839590ba5427b51cf16422f446d99f031601b"

func provisionReceiptWithManagedInstanceKey(accountID, region, slug, stage, secretARN string) string {
	return fmt.Sprintf(
		`{"app":"x","base_domain":"d","account_id":%q,"region":%q,"hosted_zone":{"id":"/hostedzone/Z1","name":"d."},"managed_instance_key":{"version":1,"source":"deploy-runner-managed-profile","secret_arn":%q,"key_id":%q,"instance_slug":%q,"stage":%q,"verified_at":"2026-06-27T00:00:00Z"}}`,
		accountID,
		region,
		secretARN,
		provisionTestManagedInstanceKeyID,
		slug,
		stage,
	)
}

func mockProvisionRunnerInstanceLookup(t *testing.T, db *ttmocks.MockExtendedDB, inst models.Instance) {
	t.Helper()

	qInst := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = inst
	}).Maybe()
}

func TestStartDeployRunnerWithMode_DoesNotPreflightTargetAccountAccess(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	mockProvisionRunnerInstanceLookup(t, db, models.Instance{
		Slug:              "theory",
		HostedAccountID:   "922120356241",
		HostedRegion:      "us-east-1",
		HostedBaseDomain:  "theory.greater.website",
		LesserHostBaseURL: "https://lesser.host",
	})

	cb := &fakeCodebuild{startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}}}
	stsClient := &fakeSTS{err: errors.New("AccessDenied")}
	srv := &Server{
		cfg: config.Config{
			Stage:                             "live",
			ManagedInstanceRoleName:           "OrganizationAccountAccessRole",
			ManagedProvisionRunnerProjectName: "runner-project",
			ManagedProvisionRunnerRoleARN:     "arn:aws:iam::693925625407:role/runner",
			ArtifactBucketName:                "artifact-bucket",
			ManagedLesserGitHubOwner:          "equaltoai",
			ManagedLesserGitHubRepo:           "lesser",
		},
		store: store.New(db),
		cb:    cb,
		sts:   stsClient,
		iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) {
			t.Fatalf("startDeployRunnerWithMode must not preflight target-account IAM")
			return nil, errors.New("unexpected target IAM preflight")
		},
		smFactory: func(context.Context, string, string, string, string, string) (secretsManagerAPI, error) {
			t.Fatalf("startDeployRunnerWithMode must not preflight target-account Secrets Manager")
			return nil, errors.New("unexpected target Secrets Manager preflight")
		},
	}
	job := &models.ProvisionJob{
		ID:              "job1",
		InstanceSlug:    "theory",
		AdminUsername:   "theory",
		AdminWalletAddr: "0x0000000000000000000000000000000000000003",
		AccountID:       "922120356241",
		AccountRoleName: "OrganizationAccountAccessRole",
		Region:          "us-east-1",
		BaseDomain:      "theory.greater.website",
		LesserVersion:   "v1.2.3",
	}

	runID, err := srv.startDeployRunnerWithMode(context.Background(), job, deployRunnerModeLesser, srv.receiptS3Key(job))
	require.NoError(t, err)
	require.Equal(t, "run1", runID)
	require.Empty(t, stsClient.lastArn)
	require.Len(t, cb.startInputs, 1)

	env := envOverrideMap(cb.startInputs[0].EnvironmentVariablesOverride)
	require.Equal(t, "theory", env["APP_SLUG"])
	require.Equal(t, "live", env["STAGE"])
	require.Equal(t, "922120356241", env["TARGET_ACCOUNT_ID"])
	require.Equal(t, "OrganizationAccountAccessRole", env["TARGET_ROLE_NAME"])
	require.Equal(t, "lesser-host/deploy/theory", env["DEPLOY_EXTERNAL_ID"])
	require.Equal(t, "", env["LESSER_HOST_INSTANCE_KEY_ARN"])
	require.Equal(t, "", env["LESSER_HOST_INSTANCE_KEY_SECRET_ID"])
	require.Equal(t, deployRunnerModeLesser, env["RUN_MODE"])
	require.Equal(t, "false", env["BODY_ENABLED"])
}

func TestStartDeployRunnerWithMode_FollowOnModesDoNotPreflightTargetAccountAccess(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{deployRunnerModeLesserBody, deployRunnerModeLesserMCP} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			db := ttmocks.NewMockExtendedDB()
			mockProvisionRunnerInstanceLookup(t, db, models.Instance{
				Slug:              "theory",
				HostedAccountID:   "922120356241",
				HostedRegion:      "us-east-1",
				HostedBaseDomain:  "theory.greater.website",
				LesserHostBaseURL: "https://lesser.host",
			})

			cb := &fakeCodebuild{startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}}}
			stsClient := &fakeSTS{err: errors.New("AccessDenied")}
			srv := &Server{
				cfg: config.Config{
					Stage:                             "live",
					ManagedInstanceRoleName:           "OrganizationAccountAccessRole",
					ManagedProvisionRunnerProjectName: "runner-project",
					ArtifactBucketName:                "artifact-bucket",
					ManagedLesserGitHubOwner:          "equaltoai",
					ManagedLesserGitHubRepo:           "lesser",
				},
				store: store.New(db),
				cb:    cb,
				sts:   stsClient,
				iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) {
					t.Fatalf("follow-on provision runner mode %s must not preflight target-account IAM", mode)
					return nil, errors.New("unexpected target IAM preflight")
				},
				smFactory: func(context.Context, string, string, string, string, string) (secretsManagerAPI, error) {
					t.Fatalf("follow-on provision runner mode %s must not preflight target-account Secrets Manager", mode)
					return nil, errors.New("unexpected target Secrets Manager preflight")
				},
			}
			job := &models.ProvisionJob{
				ID:              "job1",
				InstanceSlug:    "theory",
				AdminUsername:   "theory",
				AdminWalletAddr: "0x0000000000000000000000000000000000000003",
				AccountID:       "922120356241",
				AccountRoleName: "OrganizationAccountAccessRole",
				Region:          "us-east-1",
				BaseDomain:      "theory.greater.website",
				LesserVersion:   "v1.2.3",
			}

			runID, err := srv.startDeployRunnerWithMode(context.Background(), job, mode, "receipt.json")
			require.NoError(t, err)
			require.Equal(t, "run1", runID)
			require.Empty(t, stsClient.lastArn)
			require.Len(t, cb.startInputs, 1)
			require.Equal(t, mode, updateStartBuildEnvValue(cb.startInputs[0], "RUN_MODE"))
		})
	}
}

func TestAdvanceProvisionReceiptIngest_AppliesManagedInstanceKeyProofWithoutTargetRead(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Once()

	stsClient := &fakeSTS{err: errors.New("AccessDenied")}
	secretARN := "arn:aws:secretsmanager:us-east-1:922120356241:secret:live/theory/instance-key"
	srv := &Server{
		cfg:   config.Config{Stage: "live", ArtifactBucketName: "artifact-bucket"},
		store: store.New(db),
		sts:   stsClient,
		s3: &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(
			provisionReceiptWithManagedInstanceKey("922120356241", "us-east-1", "theory", "live", secretARN),
		))}},
		smFactory: func(context.Context, string, string, string, string, string) (secretsManagerAPI, error) {
			t.Fatalf("receipt ingest must not read target-account secret")
			return nil, errors.New("unexpected target Secrets Manager read")
		},
	}
	job := &models.ProvisionJob{
		ID:           "job1",
		InstanceSlug: "theory",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepReceiptIngest,
		AccountID:    "922120356241",
		Region:       "us-east-1",
		MaxAttempts:  3,
		BodyEnabled:  false,
	}

	delay, done, err := srv.advanceProvisionReceiptIngest(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.Zero(t, delay)
	require.True(t, done)
	require.Equal(t, provisionStepDone, job.Step)
	require.Equal(t, models.ProvisionJobStatusOK, job.Status)
	require.Contains(t, job.ReceiptJSON, `"managed_instance_key"`)
	require.Contains(t, job.ReceiptJSON, secretARN)
	require.NotContains(t, strings.ToLower(job.ReceiptJSON), "lhk_")
	require.Empty(t, stsClient.lastArn)
}
