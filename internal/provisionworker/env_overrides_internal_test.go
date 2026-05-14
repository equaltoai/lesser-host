package provisionworker

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type capturingCodebuild struct {
	lastStart *codebuild.StartBuildInput

	startOut *codebuild.StartBuildOutput
	startErr error
}

func (c *capturingCodebuild) StartBuild(_ context.Context, params *codebuild.StartBuildInput, _ ...func(*codebuild.Options)) (*codebuild.StartBuildOutput, error) {
	c.lastStart = params
	if c.startErr != nil {
		return nil, c.startErr
	}
	return c.startOut, nil
}

func (c *capturingCodebuild) BatchGetBuilds(_ context.Context, _ *codebuild.BatchGetBuildsInput, _ ...func(*codebuild.Options)) (*codebuild.BatchGetBuildsOutput, error) {
	return &codebuild.BatchGetBuildsOutput{}, nil
}

func envOverrideMap(env []cbtypes.EnvironmentVariable) map[string]string {
	out := make(map[string]string, len(env))
	for _, v := range env {
		name := aws.ToString(v.Name)
		if name == "" {
			continue
		}
		out[name] = aws.ToString(v.Value)
	}
	return out
}

func TestStartDeployRunner_AppendsTipAndAIEnv(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst)
	qInst.On("Where", "PK", "=", "INSTANCE#demo").Return(qInst)
	qInst.On("Where", "SK", "=", models.SKMetadata).Return(qInst)
	qInst.On("ConsistentRead").Return(qInst)

	tipEnabled := true
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:                           "demo",
			HostedAccountID:                "123456789012",
			HostedRegion:                   "us-east-1",
			HostedBaseDomain:               "demo.example.com",
			LesserHostBaseURL:              "https://lab.lesser.host",
			LesserHostAttestationsURL:      "https://lab.lesser.host",
			LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",

			TipEnabled:         &tipEnabled,
			TipChainID:         8453,
			TipContractAddress: " 0x1111111111111111111111111111111111111111 ",
		}
	})

	st := store.New(db)

	cb := &capturingCodebuild{
		startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}},
	}

	s := &Server{
		cfg: config.Config{
			Stage:                             "lab",
			WebAuthnRPID:                      "lesser.host",
			ManagedProvisionRunnerProjectName: "proj",
			ManagedLesserGitHubOwner:          "o",
			ManagedLesserGitHubRepo:           "r",
			ArtifactBucketName:                "bucket",
			ManagedInstanceRoleName:           "role",
			ManagedOrgVendingRoleARN:          "arn:aws:iam::123456789012:role/lesser-host-org-vending",
		},
		store: st,
		cb:    cb,
	}

	job := &models.ProvisionJob{
		ID:              "j1",
		InstanceSlug:    "demo",
		AdminUsername:   "demo",
		AdminWalletAddr: "0x123",
		AccountID:       "123456789012",
		AccountRoleName: "role",
		Region:          "us-east-1",
		BaseDomain:      "demo.example.com",
		LesserVersion:   "v1.2.3",
	}

	runID, err := s.startDeployRunner(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, "run1", runID)
	require.NotNil(t, cb.lastStart)
	require.Equal(t,
		codebuildIdempotencyToken("proj", s.deployRunnerStage(job), job.InstanceSlug, job.ID, "lesser", s.receiptS3Key(job)),
		aws.ToString(cb.lastStart.IdempotencyToken),
	)

	env := envOverrideMap(cb.lastStart.EnvironmentVariablesOverride)
	require.NotContains(t, env, "MANAGED_ORG_VENDING_ROLE_ARN")
	require.Equal(t, "true", env["TIP_ENABLED"])
	require.Equal(t, "8453", env["TIP_CHAIN_ID"])
	require.Equal(t, "0x1111111111111111111111111111111111111111", env["TIP_CONTRACT_ADDRESS"])

	require.Equal(t, "true", env["AI_ENABLED"])
	require.Equal(t, "true", env["AI_MODERATION_ENABLED"])
	require.Equal(t, "true", env["AI_NSFW_DETECTION_ENABLED"])
	require.Equal(t, "true", env["AI_SPAM_DETECTION_ENABLED"])
	require.Equal(t, "false", env["AI_PII_DETECTION_ENABLED"])
	require.Equal(t, "false", env["AI_CONTENT_DETECTION_ENABLED"])
}

func TestStartDeployRunner_OmitsTipChainAndContractWhenDisabled(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst)
	qInst.On("Where", "PK", "=", "INSTANCE#demo").Return(qInst)
	qInst.On("Where", "SK", "=", models.SKMetadata).Return(qInst)
	qInst.On("ConsistentRead").Return(qInst)

	tipEnabled := false
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:                           "demo",
			HostedAccountID:                "123456789012",
			HostedRegion:                   "us-east-1",
			HostedBaseDomain:               "demo.example.com",
			LesserHostBaseURL:              "https://lab.lesser.host",
			LesserHostAttestationsURL:      "https://lab.lesser.host",
			LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
			TipEnabled:                     &tipEnabled,
			TipChainID:                     8453,
			TipContractAddress:             "0xabc",
		}
	})

	st := store.New(db)

	cb := &capturingCodebuild{
		startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}},
	}

	s := &Server{
		cfg: config.Config{
			ManagedProvisionRunnerProjectName: "proj",
			ManagedLesserGitHubOwner:          "o",
			ManagedLesserGitHubRepo:           "r",
			ArtifactBucketName:                "bucket",
			ManagedInstanceRoleName:           "role",
		},
		store: st,
		cb:    cb,
	}

	job := &models.ProvisionJob{
		ID:              "j1",
		InstanceSlug:    "demo",
		AdminUsername:   "demo",
		AdminWalletAddr: "0x123",
		AccountID:       "123456789012",
		AccountRoleName: "role",
		Region:          "us-east-1",
		BaseDomain:      "demo.example.com",
		LesserVersion:   "v1.2.3",
	}

	_, err := s.startDeployRunner(context.Background(), job)
	require.NoError(t, err)
	require.NotNil(t, cb.lastStart)
	require.Equal(t,
		codebuildIdempotencyToken("proj", s.deployRunnerStage(job), job.InstanceSlug, job.ID, "lesser", s.receiptS3Key(job)),
		aws.ToString(cb.lastStart.IdempotencyToken),
	)

	env := envOverrideMap(cb.lastStart.EnvironmentVariablesOverride)
	require.Equal(t, "false", env["TIP_ENABLED"])
	_, hasChain := env["TIP_CHAIN_ID"]
	_, hasContract := env["TIP_CONTRACT_ADDRESS"]
	require.False(t, hasChain, "expected TIP_CHAIN_ID omitted when TIP_ENABLED=false")
	require.False(t, hasContract, "expected TIP_CONTRACT_ADDRESS omitted when TIP_ENABLED=false")
}

func TestStartUpdateDeployRunner_AppendsTipAndAIEnv(t *testing.T) {
	t.Parallel()

	cb := &capturingCodebuild{
		startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}},
	}

	s := &Server{
		cfg: config.Config{
			Stage:                             "lab",
			WebAuthnRPID:                      "lesser.host",
			ManagedProvisionRunnerProjectName: "proj",
			ManagedLesserGitHubOwner:          "o",
			ManagedLesserGitHubRepo:           "r",
			ArtifactBucketName:                "bucket",
			ManagedInstanceRoleName:           "role",
			ManagedOrgVendingRoleARN:          "arn:aws:iam::123456789012:role/lesser-host-org-vending",
		},
		cb: cb,
	}

	job := &models.UpdateJob{
		ID:                             "u1",
		InstanceSlug:                   "demo",
		AccountID:                      "123456789012",
		AccountRoleName:                "role",
		Region:                         "us-east-1",
		BaseDomain:                     "demo.example.com",
		LesserVersion:                  "v1.2.3",
		LesserHostBaseURL:              "https://lab.lesser.host",
		LesserHostAttestationsURL:      "https://lab.lesser.host",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		TranslationEnabled:             true,

		TipEnabled:         true,
		TipChainID:         10,
		TipContractAddress: " 0x2222222222222222222222222222222222222222 ",

		AIEnabled:                 false,
		AIModerationEnabled:       true,
		AINsfwDetectionEnabled:    false,
		AISpamDetectionEnabled:    true,
		AIPiiDetectionEnabled:     true,
		AIContentDetectionEnabled: false,
	}
	inst := &models.Instance{
		Slug:             "demo",
		Owner:            "wallet-abc",
		HostedAccountID:  "123456789012",
		HostedRegion:     "us-east-1",
		HostedBaseDomain: "demo.example.com",
	}

	runID, err := s.startUpdateDeployRunner(context.Background(), job, inst)
	require.NoError(t, err)
	require.Equal(t, "run1", runID)
	require.NotNil(t, cb.lastStart)
	require.Equal(t,
		codebuildIdempotencyToken("proj", normalizeManagedLesserStage(s.cfg.Stage), job.InstanceSlug, job.ID, "lesser", s.updateReceiptS3Key(job)),
		aws.ToString(cb.lastStart.IdempotencyToken),
	)

	env := envOverrideMap(cb.lastStart.EnvironmentVariablesOverride)
	require.NotContains(t, env, "MANAGED_ORG_VENDING_ROLE_ARN")
	require.Equal(t, "true", env["TIP_ENABLED"])
	require.Equal(t, "10", env["TIP_CHAIN_ID"])
	require.Equal(t, "0x2222222222222222222222222222222222222222", env["TIP_CONTRACT_ADDRESS"])

	require.Equal(t, "false", env["AI_ENABLED"])
	require.Equal(t, "true", env["AI_MODERATION_ENABLED"])
	require.Equal(t, "false", env["AI_NSFW_DETECTION_ENABLED"])
	require.Equal(t, "true", env["AI_SPAM_DETECTION_ENABLED"])
	require.Equal(t, "true", env["AI_PII_DETECTION_ENABLED"])
	require.Equal(t, "false", env["AI_CONTENT_DETECTION_ENABLED"])
}

func TestStartUpdateDeployRunner_OmitsTipChainAndContractWhenDisabled(t *testing.T) {
	t.Parallel()

	cb := &capturingCodebuild{
		startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}},
	}

	s := &Server{
		cfg: config.Config{
			Stage:                             "lab",
			WebAuthnRPID:                      "lesser.host",
			ManagedProvisionRunnerProjectName: "proj",
			ManagedLesserGitHubOwner:          "o",
			ManagedLesserGitHubRepo:           "r",
			ArtifactBucketName:                "bucket",
			ManagedInstanceRoleName:           "role",
		},
		cb: cb,
	}

	job := &models.UpdateJob{
		ID:                             "u1",
		InstanceSlug:                   "demo",
		AccountID:                      "123456789012",
		AccountRoleName:                "role",
		Region:                         "us-east-1",
		BaseDomain:                     "demo.example.com",
		LesserVersion:                  "v1.2.3",
		LesserHostBaseURL:              "https://lab.lesser.host",
		LesserHostAttestationsURL:      "https://lab.lesser.host",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		TranslationEnabled:             true,
		TipEnabled:                     false,
		TipChainID:                     10,
		TipContractAddress:             "0x2222222222222222222222222222222222222222",
		AIEnabled:                      true,
		AIModerationEnabled:            true,
		AINsfwDetectionEnabled:         true,
		AISpamDetectionEnabled:         true,
		AIPiiDetectionEnabled:          false,
		AIContentDetectionEnabled:      false,
	}
	inst := &models.Instance{
		Slug:             "demo",
		Owner:            "wallet-abc",
		HostedAccountID:  "123456789012",
		HostedRegion:     "us-east-1",
		HostedBaseDomain: "demo.example.com",
	}

	_, err := s.startUpdateDeployRunner(context.Background(), job, inst)
	require.NoError(t, err)
	require.NotNil(t, cb.lastStart)
	require.Equal(t,
		codebuildIdempotencyToken("proj", normalizeManagedLesserStage(s.cfg.Stage), job.InstanceSlug, job.ID, "lesser", s.updateReceiptS3Key(job)),
		aws.ToString(cb.lastStart.IdempotencyToken),
	)

	env := envOverrideMap(cb.lastStart.EnvironmentVariablesOverride)
	require.Equal(t, "false", env["TIP_ENABLED"])
	_, hasChain := env["TIP_CHAIN_ID"]
	_, hasContract := env["TIP_CONTRACT_ADDRESS"]
	require.False(t, hasChain, "expected TIP_CHAIN_ID omitted when TIP_ENABLED=false")
	require.False(t, hasContract, "expected TIP_CONTRACT_ADDRESS omitted when TIP_ENABLED=false")
}
