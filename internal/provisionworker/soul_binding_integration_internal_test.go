package provisionworker

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/stretchr/testify/require"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func envVarValue(env []cbtypes.EnvironmentVariable, name string) (string, bool) {
	for _, v := range env {
		if strings.TrimSpace(aws.ToString(v.Name)) == name {
			return aws.ToString(v.Value), true
		}
	}
	return "", false
}

func TestSoulBindingIntegrationSecretName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "dev/theory/soul-binding-integration", soulBindingIntegrationSecretName("lab", "Theory"))
	require.Equal(t, "dev/theory/soul-binding-integration", soulBindingIntegrationSecretName("development", "Theory"))
	require.Equal(t, "live/theory/soul-binding-integration", soulBindingIntegrationSecretName("prod", "theory"))
	require.Equal(t, "live/theory/soul-binding-integration", soulBindingIntegrationSecretName("production", "theory"))
	require.Equal(t, "live/theory/soul-binding-integration", soulBindingIntegrationSecretName("live", "theory"))
	require.Empty(t, soulBindingIntegrationSecretName("lab", "  "))
}

func TestResolveProvisionDeployRunnerInstanceInputs_CarriesSoulBindingSecretArn(t *testing.T) {
	t.Parallel()

	const soulARN = "arn:aws:secretsmanager:us-east-1:922120356241:secret:live/theory/soul-binding-integration"
	db := ttmocks.NewMockExtendedDB()
	mockProvisionRunnerInstanceLookup(t, db, models.Instance{
		Slug:                            "theory",
		HostedAccountID:                 "922120356241",
		HostedRegion:                    "us-east-1",
		HostedBaseDomain:                "theory.example.com",
		LesserHostBaseURL:               "https://lesser.host",
		LesserHostInstanceKeySecretARN:  "arn:aws:secretsmanager:us-east-1:922120356241:secret:live/theory/instance-key",
		SoulBindingIntegrationSecretARN: soulARN,
	})

	s := &Server{cfg: config.Config{Stage: "live", ManagedInstanceRoleName: "role"}, store: store.New(db)}
	job := &models.ProvisionJob{
		ID:              "job1",
		InstanceSlug:    "theory",
		AccountID:       "922120356241",
		AccountRoleName: "role",
		Region:          "us-east-1",
		BaseDomain:      "theory.example.com",
		LesserVersion:   "v1.2.3",
	}

	inputs, err := s.resolveProvisionDeployRunnerInstanceInputs(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, soulARN, inputs.soulBindingSecretArn)

	env := appendProvisionDeployRunnerInstanceEnv(nil, inputs)
	got, ok := envVarValue(env, "SOUL_BINDING_INTEGRATION_KEY_ARN")
	require.True(t, ok)
	require.Equal(t, soulARN, got)
}

func TestResolveProvisionDeployRunnerInstanceInputs_UsesCanonicalDevRefWhenMetadataEmpty(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	mockProvisionRunnerInstanceLookup(t, db, models.Instance{
		Slug:              "theory",
		HostedAccountID:   "922120356241",
		HostedRegion:      "us-east-1",
		HostedBaseDomain:  "theory.example.com",
		LesserHostBaseURL: "https://lesser.host",
	})
	s := &Server{cfg: config.Config{Stage: "lab", ManagedInstanceRoleName: "role"}, store: store.New(db)}
	job := &models.ProvisionJob{
		ID:              "job1",
		InstanceSlug:    "theory",
		AccountID:       "922120356241",
		AccountRoleName: "role",
		Region:          "us-east-1",
		BaseDomain:      "theory.example.com",
		LesserVersion:   "v1.2.3",
	}

	inputs, err := s.resolveProvisionDeployRunnerInstanceInputs(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, "dev/theory/soul-binding-integration", inputs.soulBindingSecretArn)

	env := appendProvisionDeployRunnerInstanceEnv(nil, inputs)
	got, ok := envVarValue(env, "SOUL_BINDING_INTEGRATION_KEY_ARN")
	require.True(t, ok)
	require.Equal(t, "dev/theory/soul-binding-integration", got)
}

func TestBuildUpdateDeployRunnerEnv_CarriesSoulBindingSecretArn(t *testing.T) {
	t.Parallel()

	const soulARN = "arn:aws:secretsmanager:us-east-1:123456789012:secret:dev/slug/soul-binding-integration"
	s := &Server{cfg: config.Config{Stage: "lab", ArtifactBucketName: "bucket"}}
	job := &models.UpdateJob{ID: "u1", InstanceSlug: "slug"}
	env := s.buildUpdateDeployRunnerEnv(job, updateDeployRunnerInputs{stage: "dev", soulBindingSecretArn: soulARN})
	got, ok := envVarValue(env, "SOUL_BINDING_INTEGRATION_KEY_ARN")
	require.True(t, ok)
	require.Equal(t, soulARN, got)
}

func TestResolveUpdateDeployRunnerInputs_SoulBindingCarryAndFallback(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "lab", ManagedInstanceRoleName: "role"}}
	job := &models.UpdateJob{
		ID:                             "u1",
		InstanceSlug:                   "slug",
		AccountID:                      "123456789012",
		AccountRoleName:                "role",
		Region:                         "us-east-1",
		BaseDomain:                     "slug.example.com",
		LesserVersion:                  "v1.2.3",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
	}
	inst := &models.Instance{
		Slug:             "slug",
		Owner:            "wallet-abc",
		HostedAccountID:  "123456789012",
		HostedRegion:     "us-east-1",
		HostedBaseDomain: "slug.example.com",
	}

	// Empty Host metadata derives only the normalized target-stage name.
	inputs, err := s.resolveUpdateDeployRunnerInputs(job, inst)
	require.NoError(t, err)
	require.Equal(t, "dev", inputs.stage)
	require.Equal(t, "dev/slug/soul-binding-integration", inputs.soulBindingSecretArn)

	// Instance carries the ensured ARN: the job fallback resolves to it.
	const soulARN = "arn:aws:secretsmanager:us-east-1:123456789012:secret:dev/slug/soul-binding-integration-Ab12Cd"
	inst.SoulBindingIntegrationSecretARN = soulARN
	inputs, err = s.resolveUpdateDeployRunnerInputs(job, inst)
	require.NoError(t, err)
	require.Equal(t, soulARN, inputs.soulBindingSecretArn)

	// Canonical job state wins when already carried across phases/retries.
	job.SoulBindingIntegrationSecretARN = "arn:aws:secretsmanager:us-east-1:123456789012:secret:dev/slug/soul-binding-integration-Zz90Yx"
	inputs, err = s.resolveUpdateDeployRunnerInputs(job, inst)
	require.NoError(t, err)
	require.Equal(t, job.SoulBindingIntegrationSecretARN, inputs.soulBindingSecretArn)

	// A wrong control-plane-stage prefix is rejected, never treated as a fallback.
	job.SoulBindingIntegrationSecretARN = "lab/slug/soul-binding-integration"
	_, err = s.resolveUpdateDeployRunnerInputs(job, inst)
	require.ErrorContains(t, err, "canonical target stage and slug")
}

func TestResolveProvisionDeployRunnerInstanceInputs_RejectsMismatchedSoulBindingARN(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	mockProvisionRunnerInstanceLookup(t, db, models.Instance{
		Slug:                            "theory",
		HostedAccountID:                 "922120356241",
		HostedRegion:                    "us-east-1",
		HostedBaseDomain:                "theory.example.com",
		LesserHostBaseURL:               "https://lesser.host",
		SoulBindingIntegrationSecretARN: "arn:aws:secretsmanager:us-east-1:922120356241:secret:live/other/soul-binding-integration",
	})
	s := &Server{cfg: config.Config{Stage: "live", ManagedInstanceRoleName: "role"}, store: store.New(db)}
	job := &models.ProvisionJob{
		ID: "job1", InstanceSlug: "theory", AccountID: "922120356241", AccountRoleName: "role",
		Region: "us-east-1", BaseDomain: "theory.example.com", LesserVersion: "v1.2.3",
	}

	_, err := s.resolveProvisionDeployRunnerInstanceInputs(context.Background(), job)
	require.ErrorContains(t, err, "canonical target stage and slug")
}

func validSoulBindingIntegrationReceipt(accountID, region, slug, stage string) *soulBindingIntegrationReceipt {
	return &soulBindingIntegrationReceipt{
		Version:      1,
		Source:       managedInstanceKeyReceiptSourceDeployRunner,
		SecretARN:    provisionTestSoulBindingSecretARN(accountID, region, slug, stage),
		KeyID:        provisionTestSoulBindingKeyID,
		InstanceSlug: slug,
		Stage:        stage,
		VerifiedAt:   "2026-06-27T00:00:00Z",
	}
}

func TestApplyProvisionSoulBindingIntegrationReceipt_Validation(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "live"}}
	job := &models.ProvisionJob{ID: "job1", InstanceSlug: "theory", AccountID: "922120356241", Region: "us-east-1"}

	arn, err := s.applyProvisionSoulBindingIntegrationReceipt(job, validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "live"))
	require.NoError(t, err)
	require.Equal(t, provisionTestSoulBindingSecretARN("922120356241", "us-east-1", "theory", "live"), arn)

	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, nil)
	require.ErrorContains(t, err, "soul binding integration proof missing")

	badSlug := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "other", "live")
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, badSlug)
	require.ErrorContains(t, err, "slug does not match")

	badStage := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "dev")
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, badStage)
	require.ErrorContains(t, err, "stage does not match target deployment stage")

	badAccount := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "live")
	badAccount.SecretARN = provisionTestSoulBindingSecretARN("111111111111", "us-east-1", "theory", "live")
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, badAccount)
	require.ErrorContains(t, err, "account does not match")

	badRegion := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "live")
	badRegion.SecretARN = provisionTestSoulBindingSecretARN("922120356241", "us-west-2", "theory", "live")
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, badRegion)
	require.ErrorContains(t, err, "region does not match")

	badSource := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "live")
	badSource.Source = "operator-manual"
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, badSource)
	require.ErrorContains(t, err, "source is invalid")

	badKeyID := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "live")
	badKeyID.KeyID = "not-hex"
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, badKeyID)
	require.ErrorContains(t, err, "invalid key id")

	badVersion := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "live")
	badVersion.Version = 2
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, badVersion)
	require.ErrorContains(t, err, "unsupported soul binding integration receipt version")
}

func TestApplyProvisionSoulBindingIntegrationReceipt_LabRequiresCanonicalDevIdentity(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "lab"}}
	job := &models.ProvisionJob{ID: "job1", InstanceSlug: "theory", AccountID: "922120356241", Region: "us-east-1", Stage: "dev"}

	receipt := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "dev")
	arn, err := s.applyProvisionSoulBindingIntegrationReceipt(job, receipt)
	require.NoError(t, err)
	require.Equal(t, provisionTestSoulBindingSecretARN("922120356241", "us-east-1", "theory", "dev"), arn)

	wrongStage := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "lab")
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, wrongStage)
	require.ErrorContains(t, err, "stage does not match")

	wrongName := validSoulBindingIntegrationReceipt("922120356241", "us-east-1", "theory", "dev")
	wrongName.SecretARN = provisionTestSoulBindingSecretARN("922120356241", "us-east-1", "theory", "live")
	_, err = s.applyProvisionSoulBindingIntegrationReceipt(job, wrongName)
	require.ErrorContains(t, err, "canonical target stage and slug")
}

func TestApplyUpdateSoulBindingIntegrationReceiptJSON(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "live"}}
	job := &models.UpdateJob{ID: "u1", InstanceSlug: "theory", AccountID: "922120356241", Region: "us-east-1"}

	// Every managed phase must carry proof; there is no legacy/fallback lane.
	require.ErrorContains(t, s.applyUpdateSoulBindingIntegrationReceiptJSON(job, `{"version":1}`), "proof missing")
	require.Empty(t, job.SoulBindingIntegrationSecretARN)

	receiptJSON := provisionReceiptWithManagedInstanceKey(
		"922120356241", "us-east-1", "theory", "live",
		"arn:aws:secretsmanager:us-east-1:922120356241:secret:live/theory/instance-key",
	)
	require.NoError(t, s.applyUpdateSoulBindingIntegrationReceiptJSON(job, receiptJSON))
	require.Equal(t, provisionTestSoulBindingSecretARN("922120356241", "us-east-1", "theory", "live"), job.SoulBindingIntegrationSecretARN)

	// A proof bound to another tenant account fails closed.
	crossAccount := provisionReceiptWithManagedInstanceKey(
		"111111111111", "us-east-1", "theory", "live",
		"arn:aws:secretsmanager:us-east-1:111111111111:secret:live/theory/instance-key",
	)
	require.ErrorContains(t, s.applyUpdateSoulBindingIntegrationReceiptJSON(job, crossAccount), "account does not match")
}
