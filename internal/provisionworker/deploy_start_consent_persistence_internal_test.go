package provisionworker

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	deployStartConsentTestMessage   = `{"slug":"slug","expires_at":"2026-06-01T00:00:00Z"}`
	deployStartConsentTestSignature = "0xconsent"
)

func TestAdvanceProvisionDeployStart_ConsentRetryAndReroutePersistsStripPlaintextOnly(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	message := deployStartConsentTestMessage
	signature := deployStartConsentTestSignature
	keyHex, encrypted := encryptedProvisionConsent(t, message, signature)

	t.Run("load_instance_error_retry", func(t *testing.T) {
		st, db := newConsentPersistTestStore()
		mockBranchInstanceLookup(t, db, nil, errors.New("boom"))
		builder := expectProvisionJobPersist(t, db)
		srv := consentPersistDeployStartServer(st, keyHex, nil)
		job := deployStartConsentJob(encrypted, now)
		job.MaxAttempts = 3

		delay, done, err := srv.advanceProvisionDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Positive(t, delay)
		require.Equal(t, int64(1), job.Attempts)

		assertPersistedConsentPlaintextClearedEncryptedPreserved(t, builder, encrypted)
	})

	t.Run("missing_instance_key_secret_starts_runner", func(t *testing.T) {
		st, db := newConsentPersistTestStore()
		mockBranchInstanceLookup(t, db, deployStartConsentInstance(""), nil)
		builder := expectProvisionJobPersist(t, db)
		cb := &fakeCodebuild{startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}}}
		srv := consentPersistDeployStartServer(st, keyHex, cb)
		job := deployStartConsentJob(encrypted, now)

		delay, done, err := srv.advanceProvisionDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultPollDelay, delay)
		require.Equal(t, provisionStepDeployWait, job.Step)
		require.Equal(t, "run1", job.RunID)
		require.Len(t, cb.startInputs, 1)
		assertStartBuildConsentEnv(t, cb.startInputs[0], message, signature)

		assertPersistedConsentArtifactsCleared(t, builder)
	})

	t.Run("deploy_start_error_retry", func(t *testing.T) {
		st, db := newConsentPersistTestStore()
		mockBranchInstanceLookup(t, db, deployStartConsentInstance("arn:aws:secretsmanager:us-east-1:123456789012:secret:test"), nil)
		builder := expectProvisionJobPersist(t, db)
		cb := &fakeCodebuild{startErr: errors.New("codebuild unavailable")}
		srv := consentPersistDeployStartServer(st, keyHex, cb)
		job := deployStartConsentJob(encrypted, now)
		job.MaxAttempts = 3

		delay, done, err := srv.advanceProvisionDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Positive(t, delay)
		require.Equal(t, int64(1), job.Attempts)
		require.Len(t, cb.startInputs, 1)
		assertStartBuildConsentEnv(t, cb.startInputs[0], message, signature)

		assertPersistedConsentPlaintextClearedEncryptedPreserved(t, builder, encrypted)
	})
}

func TestAdvanceProvisionDeployStart_ConsentTerminalFailuresClearAllArtifacts(t *testing.T) {
	now := time.Unix(2_000, 0).UTC()
	message := deployStartConsentTestMessage
	signature := deployStartConsentTestSignature
	keyHex, encrypted := encryptedProvisionConsent(t, message, signature)

	t.Run("instance_not_found", func(t *testing.T) {
		st, db := newConsentPersistTestStore()
		mockBranchInstanceLookup(t, db, nil, theoryErrors.ErrItemNotFound)
		builder := expectProvisionJobPersist(t, db)
		srv := consentPersistDeployStartServer(st, keyHex, nil)
		job := deployStartConsentJob(encrypted, now)

		delay, done, err := srv.advanceProvisionDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.ProvisionJobStatusError, job.Status)
		require.Equal(t, "instance_not_found", job.ErrorCode)

		assertPersistedConsentArtifactsCleared(t, builder)
	})

	t.Run("deploy_start_failed_at_max_attempts", func(t *testing.T) {
		st, db := newConsentPersistTestStore()
		mockBranchInstanceLookup(t, db, deployStartConsentInstance("arn:aws:secretsmanager:us-east-1:123456789012:secret:test"), nil)
		builder := expectProvisionJobPersist(t, db)
		cb := &fakeCodebuild{startErr: errors.New("codebuild unavailable")}
		srv := consentPersistDeployStartServer(st, keyHex, cb)
		job := deployStartConsentJob(encrypted, now)
		job.MaxAttempts = 2
		job.Attempts = 1

		delay, done, err := srv.advanceProvisionDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.ProvisionJobStatusError, job.Status)
		require.Equal(t, "deploy_start_failed", job.ErrorCode)
		require.Len(t, cb.startInputs, 1)
		assertStartBuildConsentEnv(t, cb.startInputs[0], message, signature)

		assertPersistedConsentArtifactsCleared(t, builder)
	})
}

func TestAdvanceProvisionDeployStart_RetryPreservesEncryptedConsentForRedecrypt(t *testing.T) {
	now := time.Unix(3_000, 0).UTC()
	message := deployStartConsentTestMessage
	signature := deployStartConsentTestSignature
	keyHex, encrypted := encryptedProvisionConsent(t, message, signature)
	job := deployStartConsentJob(encrypted, now)
	job.MaxAttempts = 3

	retryStore, retryDB := newConsentPersistTestStore()
	mockBranchInstanceLookup(t, retryDB, nil, errors.New("temporary instance lookup failure"))
	retryBuilder := expectProvisionJobPersist(t, retryDB)
	retrySrv := consentPersistDeployStartServer(retryStore, keyHex, nil)

	delay, done, err := retrySrv.advanceProvisionDeployStart(context.Background(), job, "req-retry", now)
	require.NoError(t, err)
	require.False(t, done)
	require.Positive(t, delay)
	assertPersistedConsentPlaintextClearedEncryptedPreserved(t, retryBuilder, encrypted)
	require.Empty(t, job.ConsentMessage)
	require.Empty(t, job.ConsentSignature)
	require.Equal(t, encrypted, job.ConsentEncrypted)

	successStore, successDB := newConsentPersistTestStore()
	mockBranchInstanceLookup(t, successDB, deployStartConsentInstance("arn:aws:secretsmanager:us-east-1:123456789012:secret:test"), nil)
	successBuilder := expectProvisionJobPersist(t, successDB)
	cb := &fakeCodebuild{startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run-success")}}}
	successSrv := consentPersistDeployStartServer(successStore, keyHex, cb)

	delay, done, err = successSrv.advanceProvisionDeployStart(context.Background(), job, "req-success", now.Add(time.Second))
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Equal(t, "run-success", job.RunID)
	require.Len(t, cb.startInputs, 1)
	assertStartBuildConsentEnv(t, cb.startInputs[0], message, signature)
	assertPersistedConsentArtifactsCleared(t, successBuilder)
}

func encryptedProvisionConsent(t *testing.T, message string, signature string) (string, string) {
	t.Helper()

	key := generateTestKey(t)
	packed, err := PackConsent(message, signature)
	require.NoError(t, err)
	encrypted, err := EncryptConsent(string(packed), key)
	require.NoError(t, err)
	return hex.EncodeToString(key), encrypted
}

func deployStartConsentJob(encrypted string, now time.Time) *models.ProvisionJob {
	return &models.ProvisionJob{
		ID:                 "job-1",
		InstanceSlug:       "slug",
		Status:             models.ProvisionJobStatusRunning,
		Step:               provisionStepDeployStart,
		MaxAttempts:        2,
		AccountID:          "123456789012",
		AccountRoleName:    "lesser-host-instance",
		Region:             "us-east-1",
		BaseDomain:         "slug.example.com",
		Stage:              "lab",
		LesserVersion:      "v1.2.6",
		AdminUsername:      "admin",
		AdminWalletAddr:    "0x0000000000000000000000000000000000000003",
		AdminWalletChainID: 1,
		ConsentEncrypted:   encrypted,
		ConsentExpiresAt:   now.Add(time.Hour),
		CreatedAt:          now.Add(-time.Minute),
		UpdatedAt:          now.Add(-time.Minute),
		ExpiresAt:          now.Add(time.Hour),
	}
}

func deployStartConsentInstance(secretARN string) *models.Instance {
	return &models.Instance{
		Slug:                           "slug",
		HostedAccountID:                "123456789012",
		HostedRegion:                   "us-east-1",
		HostedBaseDomain:               "slug.example.com",
		LesserHostInstanceKeySecretARN: secretARN,
		LesserHostBaseURL:              "https://lab.lesser.host",
		LesserHostAttestationsURL:      "https://lab.lesser.host",
	}
}

func consentPersistDeployStartServer(st *store.Store, keyHex string, cb *fakeCodebuild) *Server {
	return &Server{
		cfg: config.Config{
			Stage:                                   "lab",
			WebAuthnRPID:                            "lesser.host",
			ManagedProvisionConsentEncryptionKeyHex: keyHex,
			ManagedProvisionRunnerProjectName:       "proj",
			ManagedLesserGitHubOwner:                "equaltoai",
			ManagedLesserGitHubRepo:                 "lesser",
			ManagedLesserBodyGitHubOwner:            "equaltoai",
			ManagedLesserBodyGitHubRepo:             "lesser-body",
			ManagedLesserBodyDefaultVersion:         "v0.2.3",
			ManagedInstanceRoleName:                 "lesser-host-instance",
			ArtifactBucketName:                      "bucket",
		},
		store: st,
		cb:    cb,
	}
}

func newConsentPersistTestStore() (*store.Store, *ttmocks.MockExtendedDB) {
	db := ttmocks.NewMockExtendedDBStrict()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	return store.New(db), db
}

func expectProvisionJobPersist(t *testing.T, db *ttmocks.MockExtendedDB) *recordingTransactionBuilder {
	t.Helper()

	builder := &recordingTransactionBuilder{}
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		fn := testutil.RequireMockArg[func(core.TransactionBuilder) error](t, args, 1)
		require.NoError(t, fn(builder))
	})
	return builder
}

func assertPersistedConsentPlaintextClearedEncryptedPreserved(t *testing.T, builder *recordingTransactionBuilder, encrypted string) {
	t.Helper()

	persisted := persistedProvisionJob(t, builder)
	require.Empty(t, persisted.ConsentMessage)
	require.Empty(t, persisted.ConsentSignature)
	require.Equal(t, encrypted, persisted.ConsentEncrypted)
}

func assertPersistedConsentArtifactsCleared(t *testing.T, builder *recordingTransactionBuilder) {
	t.Helper()

	persisted := persistedProvisionJob(t, builder)
	require.Empty(t, persisted.ConsentMessage)
	require.Empty(t, persisted.ConsentSignature)
	require.Empty(t, persisted.ConsentEncrypted)
}

func persistedProvisionJob(t *testing.T, builder *recordingTransactionBuilder) *models.ProvisionJob {
	t.Helper()

	for _, model := range builder.putModels {
		if job, ok := model.(*models.ProvisionJob); ok && job != nil {
			return job
		}
	}
	t.Fatalf("expected a persisted ProvisionJob, got %#v", builder.putModels)
	return nil
}

func assertStartBuildConsentEnv(t *testing.T, in *codebuild.StartBuildInput, message string, signature string) {
	t.Helper()

	env := map[string]string{}
	for _, item := range in.EnvironmentVariablesOverride {
		env[aws.ToString(item.Name)] = aws.ToString(item.Value)
	}
	decoded, err := base64.StdEncoding.DecodeString(env["CONSENT_MESSAGE_B64"])
	require.NoError(t, err)
	require.Equal(t, message, string(decoded))
	require.Equal(t, signature, env["CONSENT_SIGNATURE"])
}
