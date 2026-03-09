package provisionworker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	testManagedUpdateTrustBaseURL = "https://example.test"
	testManagedUpdateReceiptJSON  = `{"app":"x","base_domain":"d"}`
)

func TestRunManagedUpdateStateMachine_HappyPath(t *testing.T) {
	t.Parallel()

	trustBaseURL := ""
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		baseURL := trustBaseURL
		if strings.TrimSpace(baseURL) == "" {
			baseURL = testManagedUpdateTrustBaseURL
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"configuration": map[string]any{
				"translation": map[string]any{"enabled": true},
				"trust": map[string]any{
					"enabled":  true,
					"base_url": baseURL,
				},
				"tips": map[string]any{
					"enabled":          true,
					"chain_id":         8453,
					"contract_address": "0xabc",
				},
			},
		})
	})
	handler.HandleFunc("/api/v1/instance/translation_languages", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/api/v1/trust/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/api/v1/ai/jobs/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	trustBaseURL = ts.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qKey := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()

	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()

	translationEnabled := true
	tipEnabled := true
	aiEnabled := true
	aiModerationEnabled := true
	aiNsfwEnabled := true
	aiSpamEnabled := true
	aiPiiEnabled := false
	aiContentEnabled := false

	instValue := models.Instance{
		Slug:                            "slug",
		Owner:                           "wallet-deadbeef",
		HostedAccountID:                 "123",
		HostedRegion:                    "us-east-1",
		HostedBaseDomain:                ts.URL,
		LesserHostInstanceKeySecretARN:  "",
		TranslationEnabled:              &translationEnabled,
		TipEnabled:                      &tipEnabled,
		TipChainID:                      8453,
		TipContractAddress:              "0xabc",
		LesserAIEnabled:                 &aiEnabled,
		LesserAIModerationEnabled:       &aiModerationEnabled,
		LesserAINsfwDetectionEnabled:    &aiNsfwEnabled,
		LesserAISpamDetectionEnabled:    &aiSpamEnabled,
		LesserAIPiiDetectionEnabled:     &aiPiiEnabled,
		LesserAIContentDetectionEnabled: &aiContentEnabled,
	}

	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = instValue
	}).Maybe()

	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()

	cb := &fakeCodebuild{
		startOut: &codebuild.StartBuildOutput{
			Build: &cbtypes.Build{Id: aws.String("build1")},
		},
		batchOut: &codebuild.BatchGetBuildsOutput{
			Builds: []cbtypes.Build{
				{
					BuildStatus: cbtypes.StatusTypeSucceeded,
					Logs:        &cbtypes.LogsLocation{DeepLink: aws.String("https://logs.example")},
				},
			},
		},
	}

	s3Client := &fakeS3{
		out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(testManagedUpdateReceiptJSON))},
	}

	cfg := config.Config{
		Stage:                             "live",
		ManagedProvisioningEnabled:        true,
		ManagedInstanceRoleName:           "role",
		ManagedProvisionRunnerProjectName: "project",
		ArtifactBucketName:                "artifact-bucket",
		ProvisionQueueURL:                 "https://example.com/queue",
		ManagedOrgVendingRoleARN:          "arn:aws:iam::123:role/vending",
	}

	st := store.New(db)
	sqsClient := &fakeSQS{}
	srv := NewServer(cfg, st, nil, nil, nil, sqsClient, cb, s3Client)
	srv.httpClient = ts.Client()

	fsm := &fakeSecretsManager{
		describeErr: &smtypes.ResourceNotFoundException{},
		createOut: &secretsmanager.CreateSecretOutput{
			ARN: aws.String("arn:aws:secretsmanager:us-east-1:000000000000:secret:lesser-host/live/instances/slug/instance-key"),
		},
		getOut: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{"secret":"lhk_test"}`),
		},
	}
	srv.smFactory = func(_ context.Context, _ string, _ string, _ string, _ string, _ string) (secretsManagerAPI, error) {
		return fsm, nil
	}

	job := &models.UpdateJob{
		ID:                        "job1",
		InstanceSlug:              "slug",
		Status:                    models.UpdateJobStatusQueued,
		LesserVersion:             "v1.2.3",
		RotateInstanceKey:         true,
		LesserHostBaseURL:         ts.URL,
		LesserHostAttestationsURL: ts.URL,
		TranslationEnabled:        true,
		MaxAttempts:               3,
	}

	now := time.Unix(100, 0).UTC()
	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now))

	require.Equal(t, updateStepDeployWait, job.Step)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.NotEmpty(t, job.RunID)
	require.Len(t, sqsClient.inputs, 1)

	// Second run simulates polling the queued message.
	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now.Add(1*time.Minute)))

	require.Equal(t, updateStepDone, job.Step)
	require.Equal(t, models.UpdateJobStatusOK, job.Status)
	require.NotEmpty(t, job.RunURL)
	require.NotEmpty(t, job.LesserHostInstanceKeySecretARN)
	require.NotEmpty(t, job.ReceiptJSON)
	require.True(t, job.VerifyTranslationOK != nil && *job.VerifyTranslationOK, "translation verify failed: %s", job.VerifyTranslationErr)
	require.True(t, job.VerifyTrustOK != nil && *job.VerifyTrustOK, "trust verify failed: %s", job.VerifyTrustErr)
	require.True(t, job.VerifyTipsOK != nil && *job.VerifyTipsOK, "tips verify failed: %s", job.VerifyTipsErr)
	require.True(t, job.VerifyAIOK != nil && *job.VerifyAIOK, "ai verify failed: %s", job.VerifyAIErr)
}

func TestRunManagedUpdateStateMachine_DeploysLesserBodyWithDefaultVersion(t *testing.T) {
	t.Parallel()

	trustBaseURL := ""
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		baseURL := trustBaseURL
		if strings.TrimSpace(baseURL) == "" {
			baseURL = testManagedUpdateTrustBaseURL
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"configuration": map[string]any{
				"translation": map[string]any{"enabled": true},
				"trust": map[string]any{
					"enabled":  true,
					"base_url": baseURL,
				},
				"tips": map[string]any{
					"enabled":          true,
					"chain_id":         8453,
					"contract_address": "0xabc",
				},
			},
		})
	})
	handler.HandleFunc("/api/v1/instance/translation_languages", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/api/v1/trust/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/api/v1/ai/jobs/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	trustBaseURL = ts.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qKey := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()

	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()

	translationEnabled := true
	tipEnabled := true
	aiEnabled := true
	aiModerationEnabled := true
	aiNsfwEnabled := true
	aiSpamEnabled := true
	aiPiiEnabled := false
	aiContentEnabled := false

	instValue := models.Instance{
		Slug:                            "slug",
		Owner:                           "wallet-deadbeef",
		HostedAccountID:                 "123",
		HostedRegion:                    "us-east-1",
		HostedBaseDomain:                ts.URL,
		LesserHostInstanceKeySecretARN:  "",
		TranslationEnabled:              &translationEnabled,
		TipEnabled:                      &tipEnabled,
		TipChainID:                      8453,
		TipContractAddress:              "0xabc",
		LesserAIEnabled:                 &aiEnabled,
		LesserAIModerationEnabled:       &aiModerationEnabled,
		LesserAINsfwDetectionEnabled:    &aiNsfwEnabled,
		LesserAISpamDetectionEnabled:    &aiSpamEnabled,
		LesserAIPiiDetectionEnabled:     &aiPiiEnabled,
		LesserAIContentDetectionEnabled: &aiContentEnabled,
		BodyEnabled:                     nil, // nil defaults to enabled
	}

	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = instValue
	}).Maybe()

	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()

	cb := &fakeCodebuild{
		startOut: &codebuild.StartBuildOutput{
			Build: &cbtypes.Build{Id: aws.String("build1")},
		},
		batchOut: &codebuild.BatchGetBuildsOutput{
			Builds: []cbtypes.Build{
				{
					BuildStatus: cbtypes.StatusTypeSucceeded,
					Logs:        &cbtypes.LogsLocation{DeepLink: aws.String("https://logs.example")},
				},
			},
		},
	}

	s3Client := &fakeS3{
		out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(testManagedUpdateReceiptJSON))},
	}

	cfg := config.Config{
		Stage:                             "live",
		ManagedProvisioningEnabled:        true,
		ManagedInstanceRoleName:           "role",
		ManagedProvisionRunnerProjectName: "project",
		ArtifactBucketName:                "artifact-bucket",
		ProvisionQueueURL:                 "https://example.com/queue",
		ManagedOrgVendingRoleARN:          "arn:aws:iam::123:role/vending",
		ManagedLesserBodyDefaultVersion:   "v.0.1.3",
		ManagedLesserBodyGitHubOwner:      "equaltoai",
		ManagedLesserBodyGitHubRepo:       "lesser-body",
	}

	st := store.New(db)
	sqsClient := &fakeSQS{}
	srv := NewServer(cfg, st, nil, nil, nil, sqsClient, cb, s3Client)
	srv.httpClient = ts.Client()

	fsm := &fakeSecretsManager{
		describeErr: &smtypes.ResourceNotFoundException{},
		createOut: &secretsmanager.CreateSecretOutput{
			ARN: aws.String("arn:aws:secretsmanager:us-east-1:000000000000:secret:lesser-host/live/instances/slug/instance-key"),
		},
		getOut: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{"secret":"lhk_test"}`),
		},
	}
	srv.smFactory = func(_ context.Context, _ string, _ string, _ string, _ string, _ string) (secretsManagerAPI, error) {
		return fsm, nil
	}

	job := &models.UpdateJob{
		ID:                        "job1",
		InstanceSlug:              "slug",
		Status:                    models.UpdateJobStatusQueued,
		LesserVersion:             "v1.2.3",
		RotateInstanceKey:         true,
		LesserHostBaseURL:         ts.URL,
		LesserHostAttestationsURL: ts.URL,
		TranslationEnabled:        true,
		MaxAttempts:               3,
	}

	now := time.Unix(100, 0).UTC()

	envValue := func(in *codebuild.StartBuildInput, name string) string {
		if in == nil {
			return ""
		}
		for _, v := range in.EnvironmentVariablesOverride {
			if aws.ToString(v.Name) == name {
				return aws.ToString(v.Value)
			}
		}
		return ""
	}

	// First run starts the main Lesser deploy.
	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now))
	require.Equal(t, updateStepDeployWait, job.Step)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.NotEmpty(t, job.RunID)
	require.Len(t, sqsClient.inputs, 1)

	// Second run ingests receipt and starts lesser-body deploy.
	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now.Add(1*time.Minute)))
	require.Equal(t, updateStepBodyDeployWait, job.Step)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.Equal(t, "v.0.1.3", job.LesserBodyVersion)
	require.NotEmpty(t, job.RunID)
	require.Len(t, sqsClient.inputs, 2)

	// Third run completes lesser-body deploy and starts MCP wiring deploy.
	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now.Add(2*time.Minute)))
	require.Equal(t, updateStepDeployMcpWait, job.Step)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.NotEmpty(t, job.RunID)
	require.Len(t, sqsClient.inputs, 3)

	// Fourth run completes MCP wiring and verifies deployment.
	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now.Add(3*time.Minute)))
	require.Equal(t, updateStepDone, job.Step)
	require.Equal(t, models.UpdateJobStatusOK, job.Status)
	require.NotEmpty(t, job.RunURL)

	require.Len(t, cb.startInputs, 3)
	require.Equal(t, "lesser", envValue(cb.startInputs[0], "RUN_MODE"))
	require.Equal(t, "lesser-body", envValue(cb.startInputs[1], "RUN_MODE"))
	require.Equal(t, "lesser-mcp", envValue(cb.startInputs[2], "RUN_MODE"))
	require.Equal(t, "v.0.1.3", envValue(cb.startInputs[1], "LESSER_BODY_VERSION"))
	require.Equal(t, "v.0.1.3", envValue(cb.startInputs[2], "LESSER_BODY_VERSION"))
}

func TestRunManagedUpdateStateMachine_BodyOnlySkipsLesserDeploy(t *testing.T) {
	t.Parallel()

	trustBaseURL := ""
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		baseURL := trustBaseURL
		if strings.TrimSpace(baseURL) == "" {
			baseURL = testManagedUpdateTrustBaseURL
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"configuration": map[string]any{
				"translation": map[string]any{"enabled": true},
				"trust": map[string]any{
					"enabled":  true,
					"base_url": baseURL,
				},
				"tips": map[string]any{
					"enabled":          true,
					"chain_id":         8453,
					"contract_address": "0xabc",
				},
			},
		})
	})
	handler.HandleFunc("/api/v1/instance/translation_languages", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/api/v1/trust/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/api/v1/ai/jobs/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	trustBaseURL = ts.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qKey := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()

	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()

	translationEnabled := true
	tipEnabled := true
	aiEnabled := true
	aiModerationEnabled := true
	aiNsfwEnabled := true
	aiSpamEnabled := true
	aiPiiEnabled := false
	aiContentEnabled := false

	instValue := models.Instance{
		Slug:                            "slug",
		Owner:                           "wallet-deadbeef",
		HostedAccountID:                 "123",
		HostedRegion:                    "us-east-1",
		HostedBaseDomain:                ts.URL,
		LesserHostInstanceKeySecretARN:  "",
		LesserVersion:                   "v1.2.3",
		TranslationEnabled:              &translationEnabled,
		TipEnabled:                      &tipEnabled,
		TipChainID:                      8453,
		TipContractAddress:              "0xabc",
		LesserAIEnabled:                 &aiEnabled,
		LesserAIModerationEnabled:       &aiModerationEnabled,
		LesserAINsfwDetectionEnabled:    &aiNsfwEnabled,
		LesserAISpamDetectionEnabled:    &aiSpamEnabled,
		LesserAIPiiDetectionEnabled:     &aiPiiEnabled,
		LesserAIContentDetectionEnabled: &aiContentEnabled,
		BodyEnabled:                     nil,
	}

	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = instValue
	}).Maybe()

	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()

	cb := &fakeCodebuild{
		startOut: &codebuild.StartBuildOutput{
			Build: &cbtypes.Build{Id: aws.String("build1")},
		},
		batchOut: &codebuild.BatchGetBuildsOutput{
			Builds: []cbtypes.Build{
				{
					BuildStatus: cbtypes.StatusTypeSucceeded,
					Logs:        &cbtypes.LogsLocation{DeepLink: aws.String("https://logs.example")},
				},
			},
		},
	}

	s3Client := &fakeS3{
		out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(testManagedUpdateReceiptJSON))},
	}

	cfg := config.Config{
		Stage:                             "live",
		ManagedProvisioningEnabled:        true,
		ManagedInstanceRoleName:           "role",
		ManagedProvisionRunnerProjectName: "project",
		ArtifactBucketName:                "artifact-bucket",
		ProvisionQueueURL:                 "https://example.com/queue",
		ManagedOrgVendingRoleARN:          "arn:aws:iam::123:role/vending",
		ManagedLesserBodyDefaultVersion:   "v.0.1.3",
		ManagedLesserBodyGitHubOwner:      "equaltoai",
		ManagedLesserBodyGitHubRepo:       "lesser-body",
	}

	st := store.New(db)
	sqsClient := &fakeSQS{}
	srv := NewServer(cfg, st, nil, nil, nil, sqsClient, cb, s3Client)
	srv.httpClient = ts.Client()

	fsm := &fakeSecretsManager{
		describeErr: &smtypes.ResourceNotFoundException{},
		createOut: &secretsmanager.CreateSecretOutput{
			ARN: aws.String("arn:aws:secretsmanager:us-east-1:000000000000:secret:lesser-host/live/instances/slug/instance-key"),
		},
		getOut: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{"secret":"lhk_test"}`),
		},
	}
	srv.smFactory = func(_ context.Context, _ string, _ string, _ string, _ string, _ string) (secretsManagerAPI, error) {
		return fsm, nil
	}

	job := &models.UpdateJob{
		ID:                        "job1",
		InstanceSlug:              "slug",
		Status:                    models.UpdateJobStatusQueued,
		LesserVersion:             "v1.2.3",
		LesserBodyVersion:         "v.0.1.3",
		BodyOnly:                  true,
		LesserHostBaseURL:         ts.URL,
		LesserHostAttestationsURL: ts.URL,
		TranslationEnabled:        true,
		MaxAttempts:               3,
	}

	now := time.Unix(100, 0).UTC()

	envValue := func(in *codebuild.StartBuildInput, name string) string {
		if in == nil {
			return ""
		}
		for _, v := range in.EnvironmentVariablesOverride {
			if aws.ToString(v.Name) == name {
				return aws.ToString(v.Value)
			}
		}
		return ""
	}

	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now))
	require.Equal(t, updateStepBodyDeployWait, job.Step)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.NotEmpty(t, job.RunID)
	require.Len(t, sqsClient.inputs, 1)

	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now.Add(1*time.Minute)))
	require.Equal(t, updateStepDeployMcpWait, job.Step)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.NotEmpty(t, job.RunID)
	require.Len(t, sqsClient.inputs, 2)

	require.NoError(t, srv.runManagedUpdateStateMachine(ctx, job, "req", now.Add(2*time.Minute)))
	require.Equal(t, updateStepDone, job.Step)
	require.Equal(t, models.UpdateJobStatusOK, job.Status)
	require.NotEmpty(t, job.RunURL)

	require.Len(t, cb.startInputs, 2)
	require.Equal(t, "lesser-body", envValue(cb.startInputs[0], "RUN_MODE"))
	require.Equal(t, "lesser-mcp", envValue(cb.startInputs[1], "RUN_MODE"))
	require.Equal(t, "v.0.1.3", envValue(cb.startInputs[0], "LESSER_BODY_VERSION"))
	require.Equal(t, "v.0.1.3", envValue(cb.startInputs[1], "LESSER_BODY_VERSION"))
}

func TestUpdateJob_ProcessableAndMissingConfig(t *testing.T) {
	t.Parallel()

	require.False(t, updateJobProcessable(nil))
	require.True(t, updateJobProcessable(&models.UpdateJob{Status: " queued "}))

	s := &Server{cfg: config.Config{}}
	require.NotEmpty(t, s.missingManagedUpdateConfig())
}

func TestResolveUpdateHostURLs_Defaults(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "lab", WebAuthnRPID: "example.com"}}
	publicBaseURL, attestationsURL := s.resolveUpdateHostURLs(&models.UpdateJob{})
	require.Contains(t, publicBaseURL, "https://")
	require.Equal(t, publicBaseURL, attestationsURL)
}

func TestResolveInstanceKeyPlaintext_ErrorsWithoutSecretARN(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, err := s.resolveInstanceKeyPlaintext(context.Background(), &models.UpdateJob{})
	require.Error(t, err)
}

func TestUpdateDeployRunnerStatus_ErrorsWithoutCodebuild(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.getDeployRunnerStatus(context.Background(), "run")
	require.Error(t, err)

	s = &Server{cb: &fakeCodebuild{batchErr: errors.New("nope")}}
	_, _, err = s.getDeployRunnerStatus(context.Background(), "run")
	require.Error(t, err)

	s = &Server{cb: &fakeCodebuild{batchOut: &codebuild.BatchGetBuildsOutput{Builds: nil}}}
	_, _, err = s.getDeployRunnerStatus(context.Background(), "run")
	require.Error(t, err)
}

func TestEnsureInstanceKeyRecord_ConditionFailedIsOk(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(theoryErrors.ErrConditionFailed).Maybe()

	srv := &Server{store: store.New(db)}
	require.NoError(t, srv.ensureInstanceKeyRecord(context.Background(), "slug", "kid"))
}
