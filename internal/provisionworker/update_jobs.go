package provisionworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	updateStepQueued         = "queued"
	updateStepInstanceConfig = "instance.config"
	updateStepDeployStart    = "deploy.start"
	updateStepDeployWait     = "deploy.wait"
	updateStepReceiptIngest  = "receipt.ingest"
	updateStepVerify         = "verify"
	updateStepDone           = "done"
	updateStepFailed         = "failed"

	updateMaxTransitionsPerRun = 6
)

func updateJobProcessable(job *models.UpdateJob) bool {
	if job == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(job.Status))
	return status == models.UpdateJobStatusQueued || status == models.UpdateJobStatusRunning
}

func (s *Server) processUpdateJob(ctx context.Context, requestID string, jobID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}

	job, err := s.loadUpdateJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job == nil || !updateJobProcessable(job) {
		return nil
	}

	now := time.Now().UTC()

	if !s.cfg.ManagedProvisioningEnabled {
		return s.failUpdateJob(ctx, job, requestID, now, "disabled", "managed provisioning is disabled (set MANAGED_PROVISIONING_ENABLED=true)")
	}

	if missing := s.missingManagedUpdateConfig(); len(missing) > 0 {
		return s.failUpdateJob(ctx, job, requestID, now, "missing_config", "missing required config: "+strings.Join(missing, ", "))
	}

	return s.runManagedUpdateStateMachine(ctx, job, requestID, now)
}

func (s *Server) loadUpdateJob(ctx context.Context, jobID string) (*models.UpdateJob, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, nil
	}

	job, err := s.store.GetUpdateJob(ctx, jobID)
	if theoryErrors.IsNotFound(err) || job == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Server) missingManagedUpdateConfig() []string {
	if s == nil {
		return nil
	}
	var missing []string
	if strings.TrimSpace(s.cfg.ManagedInstanceRoleName) == "" {
		missing = append(missing, "MANAGED_INSTANCE_ROLE_NAME")
	}
	if strings.TrimSpace(s.cfg.ManagedProvisionRunnerProjectName) == "" {
		missing = append(missing, "MANAGED_PROVISION_RUNNER_PROJECT_NAME")
	}
	if strings.TrimSpace(s.cfg.ArtifactBucketName) == "" {
		missing = append(missing, "ARTIFACT_BUCKET_NAME")
	}
	return missing
}

func (s *Server) failUpdateJob(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time, code string, msg string) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return nil
	}

	job.Status = models.UpdateJobStatusError
	job.Step = updateStepFailed
	job.ErrorCode = strings.TrimSpace(code)
	job.ErrorMessage = strings.TrimSpace(msg)
	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	_ = job.UpdateKeys()

	updateInst := &models.Instance{Slug: strings.TrimSpace(job.InstanceSlug)}
	_ = updateInst.UpdateKeys()

	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Put(job)
		tx.UpdateWithBuilder(updateInst, func(ub core.UpdateBuilder) error {
			ub.Set("UpdateStatus", models.UpdateJobStatusError)
			ub.Set("UpdateJobID", strings.TrimSpace(job.ID))
			return nil
		}, tabletheory.IfExists())
		return nil
	})
}

func (s *Server) persistUpdateJobAndInstance(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time, instanceUpdate func(core.UpdateBuilder) error) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return fmt.Errorf("job is nil")
	}

	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	_ = job.UpdateKeys()

	updateInst := &models.Instance{Slug: strings.TrimSpace(job.InstanceSlug)}
	_ = updateInst.UpdateKeys()

	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Put(job)
		if instanceUpdate != nil {
			tx.UpdateWithBuilder(updateInst, instanceUpdate, tabletheory.IfExists())
		}
		return nil
	})
}

func (s *Server) requeueUpdateJob(ctx context.Context, jobID string, delay time.Duration) error {
	if s == nil || s.sqs == nil {
		return fmt.Errorf("sqs client not initialized")
	}
	url := strings.TrimSpace(s.cfg.ProvisionQueueURL)
	if url == "" {
		return fmt.Errorf("provision queue url is not configured")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil
	}

	body, err := json.Marshal(provisioning.JobMessage{Kind: "update_job", JobID: jobID})
	if err != nil {
		return err
	}

	delaySeconds := int32(delay.Round(time.Second).Seconds())
	if delaySeconds < 0 {
		delaySeconds = 0
	}
	if delaySeconds > 900 {
		delaySeconds = 900
	}

	_, err = s.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:     aws.String(url),
		MessageBody:  aws.String(string(body)),
		DelaySeconds: delaySeconds,
	})
	return err
}

func (s *Server) runManagedUpdateStateMachine(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return nil
	}

	if !job.ExpiresAt.IsZero() && job.ExpiresAt.Before(now) {
		return s.failUpdateJob(ctx, job, requestID, now, "expired", "update job has expired")
	}

	s.initializeManagedUpdateJob(job)
	if err := s.startManagedUpdateJobIfQueued(ctx, job, requestID, now); err != nil {
		return err
	}
	return s.advanceManagedUpdateLoop(ctx, job, requestID, now)
}

func (s *Server) initializeManagedUpdateJob(job *models.UpdateJob) {
	if s == nil || job == nil {
		return
	}

	if strings.TrimSpace(job.Step) == "" {
		job.Step = updateStepQueued
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 10
	}
	if strings.TrimSpace(job.AccountRoleName) == "" {
		job.AccountRoleName = strings.TrimSpace(s.cfg.ManagedInstanceRoleName)
	}
}

func (s *Server) startManagedUpdateJobIfQueued(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) error {
	if s == nil || job == nil {
		return nil
	}

	status := strings.ToLower(strings.TrimSpace(job.Status))
	if status != models.UpdateJobStatusQueued {
		return nil
	}

	job.Status = models.UpdateJobStatusRunning
	job.Note = "starting update"
	if strings.TrimSpace(job.Step) == "" {
		job.Step = updateStepQueued
	}
	return s.persistUpdateJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
		ub.Set("UpdateStatus", models.UpdateJobStatusRunning)
		ub.Set("UpdateJobID", strings.TrimSpace(job.ID))
		return nil
	})
}

func (s *Server) advanceManagedUpdateLoop(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return nil
	}

	delay := time.Duration(0)
	done := false

	for transitions := 0; transitions < updateMaxTransitionsPerRun; transitions++ {
		if !updateJobProcessable(job) {
			return nil
		}

		switch strings.TrimSpace(job.Step) {
		case updateStepQueued:
			job.Step = updateStepInstanceConfig
			job.Note = "ensuring instance configuration"
			if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
				return err
			}
			continue

		case updateStepInstanceConfig:
			delay, done, _ = s.advanceUpdateInstanceConfig(ctx, job, requestID, now)

		case updateStepDeployStart:
			delay, done, _ = s.advanceUpdateDeployStart(ctx, job, requestID, now)

		case updateStepDeployWait:
			delay, done, _ = s.advanceUpdateDeployWait(ctx, job, requestID, now)

		case updateStepReceiptIngest:
			delay, done, _ = s.advanceUpdateReceiptIngest(ctx, job, requestID, now)

		case updateStepVerify:
			delay, done, _ = s.advanceUpdateVerify(ctx, job, requestID, now)

		case updateStepDone:
			done = true
			delay = 0

		default:
			return s.failUpdateJob(ctx, job, requestID, now, "invalid_step", "unknown update job step: "+strings.TrimSpace(job.Step))
		}

		if done {
			return nil
		}
		if delay > 0 {
			break
		}
	}

	if delay > 0 {
		return s.requeueUpdateJob(ctx, strings.TrimSpace(job.ID), delay)
	}
	return nil
}

func (s *Server) retryUpdateJobOrFail(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	code string,
	msg string,
	baseDelay time.Duration,
	maxDelay time.Duration,
) (time.Duration, bool, error) {
	job.Attempts++
	if job.Attempts >= job.MaxAttempts {
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, strings.TrimSpace(code), strings.TrimSpace(msg))
	}
	_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
	return jitteredBackoff(job.Attempts, baseDelay, maxDelay), false, nil
}

func (s *Server) advanceUpdateInstanceConfig(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return 0, false, fmt.Errorf("store not initialized")
	}
	if job == nil {
		return 0, true, nil
	}

	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return s.retryUpdateJobOrFail(ctx, job, requestID, now, "instance_load_failed", "failed to load instance: "+err.Error(), provisionDefaultShortRetryDelay, 2*time.Minute)
	}
	if inst == nil {
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, "instance_not_found", "instance record not found")
	}

	accountID := strings.TrimSpace(inst.HostedAccountID)
	region := strings.TrimSpace(inst.HostedRegion)
	baseDomain := strings.TrimSpace(inst.HostedBaseDomain)
	roleName := strings.TrimSpace(job.AccountRoleName)
	if roleName == "" {
		roleName = strings.TrimSpace(s.cfg.ManagedInstanceRoleName)
	}
	if accountID == "" || region == "" || baseDomain == "" || roleName == "" {
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, "missing_instance_metadata", "managed instance account metadata is missing")
	}

	publicBaseURL := strings.TrimSpace(job.LesserHostBaseURL)
	if publicBaseURL == "" {
		publicBaseURL = strings.TrimSpace(s.publicBaseURL())
	}
	attestationsURL := strings.TrimSpace(job.LesserHostAttestationsURL)
	if attestationsURL == "" {
		attestationsURL = publicBaseURL
	}

	// Ensure the instance key secret exists in the instance account (and the InstanceKey record exists in lesser-host state).
	pseudo := &models.ProvisionJob{
		ID:              strings.TrimSpace(job.ID),
		InstanceSlug:    strings.TrimSpace(job.InstanceSlug),
		AccountID:       accountID,
		AccountRoleName: roleName,
		Region:          region,
	}
	secretArn, err := s.ensureManagedInstanceKeySecret(ctx, pseudo, inst)
	if err != nil {
		return s.retryUpdateJobOrFail(ctx, job, requestID, now, "instance_key_secret_failed", "failed to ensure instance key secret: "+err.Error(), provisionDefaultShortRetryDelay, 5*time.Minute)
	}

	job.AccountID = accountID
	job.Region = region
	job.BaseDomain = baseDomain
	job.LesserHostBaseURL = publicBaseURL
	job.LesserHostAttestationsURL = attestationsURL
	job.LesserHostInstanceKeySecretARN = strings.TrimSpace(secretArn)
	job.Step = updateStepDeployStart
	job.Note = "starting update deploy runner"

	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
		if publicBaseURL != "" {
			ub.Set("LesserHostBaseURL", publicBaseURL)
			ub.Set("LesserHostAttestationsURL", attestationsURL)
		}
		if strings.TrimSpace(secretArn) != "" {
			ub.Set("LesserHostInstanceKeySecretARN", strings.TrimSpace(secretArn))
		}
		ub.Set("TranslationEnabled", job.TranslationEnabled)
		return nil
	}); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) updateReceiptS3Key(job *models.UpdateJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/updates/%s/%s/state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}

func (s *Server) updateBootstrapS3Key(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/bootstrap.json", slug)
}

func walletAddressFromUsername(username string) string {
	username = strings.ToLower(strings.TrimSpace(username))
	if !strings.HasPrefix(username, "wallet-") {
		return ""
	}
	hexPart := strings.TrimSpace(strings.TrimPrefix(username, "wallet-"))
	if hexPart == "" {
		return ""
	}
	return "0x" + hexPart
}

func (s *Server) startUpdateDeployRunner(ctx context.Context, job *models.UpdateJob, inst *models.Instance) (string, error) {
	if s == nil || s.cb == nil {
		return "", fmt.Errorf("codebuild client not initialized")
	}
	if job == nil {
		return "", fmt.Errorf("job is nil")
	}
	if inst == nil {
		return "", fmt.Errorf("instance is nil")
	}

	projectName, err := s.provisionRunnerProjectName()
	if err != nil {
		return "", err
	}

	accountID := strings.TrimSpace(job.AccountID)
	roleName := strings.TrimSpace(job.AccountRoleName)
	region := strings.TrimSpace(job.Region)
	baseDomain := strings.TrimSpace(job.BaseDomain)
	lesserVersion := strings.TrimSpace(job.LesserVersion)
	instanceKeySecretArn := strings.TrimSpace(job.LesserHostInstanceKeySecretARN)
	if accountID == "" || roleName == "" || region == "" || baseDomain == "" || lesserVersion == "" {
		return "", fmt.Errorf("missing required update job deploy inputs")
	}
	if instanceKeySecretArn == "" {
		return "", fmt.Errorf("instance key secret arn is missing")
	}

	adminWallet := walletAddressFromUsername(strings.TrimSpace(inst.Owner))
	if adminWallet == "" {
		return "", fmt.Errorf("instance owner is not a wallet username")
	}

	stage := normalizeManagedLesserStage(strings.TrimSpace(s.cfg.Stage))
	receiptKey := s.updateReceiptS3Key(job)
	bootstrapKey := s.updateBootstrapS3Key(strings.TrimSpace(job.InstanceSlug))

	lesserHostURL := strings.TrimSpace(job.LesserHostBaseURL)
	if lesserHostURL == "" {
		lesserHostURL = strings.TrimSpace(s.publicBaseURL())
	}
	lesserHostAttestationsURL := strings.TrimSpace(job.LesserHostAttestationsURL)
	if lesserHostAttestationsURL == "" {
		lesserHostAttestationsURL = lesserHostURL
	}
	if lesserHostURL == "" {
		return "", fmt.Errorf("lesser host base url is missing")
	}

	env := []cbtypes.EnvironmentVariable{
		{Name: aws.String("JOB_ID"), Value: aws.String(strings.TrimSpace(job.ID))},
		{Name: aws.String("APP_SLUG"), Value: aws.String(strings.TrimSpace(job.InstanceSlug))},
		{Name: aws.String("STAGE"), Value: aws.String(stage)},
		{Name: aws.String("ADMIN_USERNAME"), Value: aws.String(strings.TrimSpace(job.InstanceSlug))},
		{Name: aws.String("ADMIN_WALLET_ADDRESS"), Value: aws.String(adminWallet)},
		{Name: aws.String("BASE_DOMAIN"), Value: aws.String(baseDomain)},
		{Name: aws.String("TARGET_ACCOUNT_ID"), Value: aws.String(accountID)},
		{Name: aws.String("TARGET_ROLE_NAME"), Value: aws.String(roleName)},
		{Name: aws.String("TARGET_REGION"), Value: aws.String(region)},
		{Name: aws.String("LESSER_VERSION"), Value: aws.String(lesserVersion)},
		{Name: aws.String("ARTIFACT_BUCKET"), Value: aws.String(strings.TrimSpace(s.cfg.ArtifactBucketName))},
		{Name: aws.String("RECEIPT_S3_KEY"), Value: aws.String(receiptKey)},
		{Name: aws.String("BOOTSTRAP_S3_KEY"), Value: aws.String(bootstrapKey)},
		{Name: aws.String("GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubOwner))},
		{Name: aws.String("GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubRepo))},

		{Name: aws.String("LESSER_HOST_URL"), Value: aws.String(lesserHostURL)},
		{Name: aws.String("LESSER_HOST_ATTESTATIONS_URL"), Value: aws.String(lesserHostAttestationsURL)},
		{Name: aws.String("LESSER_HOST_INSTANCE_KEY_ARN"), Value: aws.String(instanceKeySecretArn)},
		{Name: aws.String("TRANSLATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.TranslationEnabled))},
	}

	if strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN) != "" {
		env = append(env, cbtypes.EnvironmentVariable{
			Name:  aws.String("MANAGED_ORG_VENDING_ROLE_ARN"),
			Value: aws.String(strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN)),
		})
	}

	out, err := s.cb.StartBuild(ctx, &codebuild.StartBuildInput{
		ProjectName:                  aws.String(projectName),
		EnvironmentVariablesOverride: env,
	})
	if err != nil {
		return "", err
	}
	return codebuildBuildID(out)
}

func (s *Server) advanceUpdateDeployStart(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	if strings.TrimSpace(job.RunID) != "" {
		job.Step = updateStepDeployWait
		job.Note = "deploy runner already started"
		if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return provisionDefaultPollDelay, false, nil
	}

	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return s.retryUpdateJobOrFail(ctx, job, requestID, now, "instance_load_failed", "failed to load instance: "+err.Error(), provisionDefaultShortRetryDelay, 2*time.Minute)
	}
	if inst == nil {
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, "instance_not_found", "instance record not found")
	}
	if strings.TrimSpace(job.LesserHostInstanceKeySecretARN) == "" && strings.TrimSpace(inst.LesserHostInstanceKeySecretARN) == "" {
		job.Step = updateStepInstanceConfig
		job.Note = "ensuring instance configuration"
		if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}
	if strings.TrimSpace(job.LesserHostInstanceKeySecretARN) == "" {
		job.LesserHostInstanceKeySecretARN = strings.TrimSpace(inst.LesserHostInstanceKeySecretARN)
	}

	runID, err := s.startUpdateDeployRunner(ctx, job, inst)
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, "deploy_start_failed", "failed to start deploy runner: "+err.Error())
		}
		job.Note = "failed to start deploy runner; retrying: " + compactErr(err)
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 10*time.Minute), false, nil
	}

	job.RunID = strings.TrimSpace(runID)
	job.Step = updateStepDeployWait
	job.Note = "deploy runner in progress"
	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceUpdateDeployWait(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, "deploy_timeout", "deploy runner timed out")
	}

	status, deepLink, err := s.getDeployRunnerStatus(ctx, strings.TrimSpace(job.RunID))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, "deploy_status_failed", "failed to poll deploy runner: "+err.Error())
		}
		job.Note = "failed to poll deploy runner; retrying: " + compactErr(err)
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultPollDelay, 10*time.Minute), false, nil
	}

	if deepLink != "" && strings.TrimSpace(job.RunURL) == "" {
		job.RunURL = deepLink
	}

	switch status {
	case codebuildStatusSucceeded:
		job.Step = updateStepReceiptIngest
		job.Note = "ingesting deployment receipt"
		if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil

	case codebuildStatusInProgress:
		job.Note = "deploy runner in progress"
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil

	case codebuildStatusFailed, codebuildStatusFault, codebuildStatusStopped, codebuildStatusTimedOut:
		msg := "deploy runner failed"
		if deepLink != "" {
			job.RunURL = deepLink
			msg = msg + " (CodeBuild: " + deepLink + ")"
		}
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, "deploy_failed", msg)

	default:
		job.Note = "deploy runner status: " + status
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil
	}
}

func (s *Server) advanceUpdateReceiptIngest(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	receiptKey := s.updateReceiptS3Key(job)
	receiptJSON, _, err := s.loadReceiptFromS3(ctx, strings.TrimSpace(s.cfg.ArtifactBucketName), receiptKey)
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, "receipt_load_failed", "failed to load receipt: "+err.Error())
		}
		job.Note = "failed to load receipt; retrying: " + compactErr(err)
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 5*time.Minute), false, nil
	}

	job.ReceiptJSON = strings.TrimSpace(receiptJSON)
	job.Step = updateStepVerify
	job.Note = "verifying deployment"
	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

type instanceV2Response struct {
	Configuration struct {
		Translation struct {
			Enabled bool `json:"enabled"`
		} `json:"translation"`
	} `json:"configuration"`
}

func fetchInstanceTranslationEnabled(ctx context.Context, baseDomain string) (bool, error) {
	baseDomain = strings.TrimSpace(baseDomain)
	if baseDomain == "" {
		return false, fmt.Errorf("base domain is required")
	}

	host := strings.TrimSpace(baseDomain)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")
	u := fmt.Sprintf("https://%s/api/v2/instance", host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return false, fmt.Errorf("instance config request failed (HTTP %d)", resp.StatusCode)
	}

	var parsed instanceV2Response
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return false, err
	}
	return parsed.Configuration.Translation.Enabled, nil
}

func updateTrustVerifyJobID(jobID string) string {
	sum := sha256.Sum256([]byte("trust-verify:" + strings.TrimSpace(jobID)))
	return hex.EncodeToString(sum[:])
}

func (s *Server) resolveInstanceKeyPlaintext(ctx context.Context, job *models.UpdateJob) (string, error) {
	if s == nil {
		return "", fmt.Errorf("server is nil")
	}
	if job == nil {
		return "", fmt.Errorf("job is nil")
	}

	secretArn := strings.TrimSpace(job.LesserHostInstanceKeySecretARN)
	if secretArn == "" {
		return "", fmt.Errorf("instance key secret arn is missing")
	}

	accountID := strings.TrimSpace(job.AccountID)
	roleName := strings.TrimSpace(job.AccountRoleName)
	region := strings.TrimSpace(job.Region)
	slug := strings.TrimSpace(job.InstanceSlug)
	jobID := strings.TrimSpace(job.ID)
	if accountID == "" || roleName == "" || region == "" || slug == "" || jobID == "" {
		return "", fmt.Errorf("missing managed instance metadata for secret read")
	}

	sm, err := s.childSecretsManagerClient(ctx, accountID, roleName, region, slug, jobID)
	if err != nil {
		return "", err
	}

	out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretArn)})
	if err != nil {
		return "", err
	}

	raw := strings.TrimSpace(aws.ToString(out.SecretString))
	if raw == "" && len(out.SecretBinary) > 0 {
		raw = strings.TrimSpace(string(out.SecretBinary))
	}
	if raw == "" {
		return "", fmt.Errorf("secret value is empty")
	}
	return raw, nil
}

func verifyTrustEndpoint(ctx context.Context, baseURL string, instanceKey string, jobID string) (bool, string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceKey = strings.TrimSpace(instanceKey)
	if baseURL == "" {
		return false, "lesser host base url is missing"
	}
	if instanceKey == "" {
		return false, "instance key is missing"
	}

	healthID := updateTrustVerifyJobID(jobID)
	u := baseURL + "/api/v1/ai/jobs/" + healthID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+instanceKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return true, ""
	default:
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return true, ""
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return false, fmt.Sprintf("unauthorized (HTTP %d)", resp.StatusCode)
		}
		return false, fmt.Sprintf("unexpected status (HTTP %d)", resp.StatusCode)
	}
}

func (s *Server) advanceUpdateVerify(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return 0, false, fmt.Errorf("store not initialized")
	}
	if job == nil {
		return 0, true, nil
	}

	transOK := false
	transErr := ""
	verifyDomain := strings.TrimSpace(job.BaseDomain)
	managedStage := normalizeManagedLesserStage(strings.TrimSpace(s.cfg.Stage))
	if managedStage != managedStageLive && verifyDomain != "" {
		verifyDomain = managedStage + "." + verifyDomain
	}
	if got, err := fetchInstanceTranslationEnabled(ctx, verifyDomain); err != nil {
		transErr = err.Error()
	} else if got != job.TranslationEnabled {
		transErr = fmt.Sprintf("expected %t, got %t", job.TranslationEnabled, got)
	} else {
		transOK = true
	}

	trustOK := false
	trustErr := ""
	key, err := s.resolveInstanceKeyPlaintext(ctx, job)
	if err != nil {
		trustErr = err.Error()
	} else {
		baseURL := strings.TrimSpace(job.LesserHostBaseURL)
		if baseURL == "" {
			baseURL = strings.TrimSpace(s.publicBaseURL())
		}
		trustOK, trustErr = verifyTrustEndpoint(ctx, baseURL, key, strings.TrimSpace(job.ID))
	}

	job.VerifyTranslationOK = &transOK
	job.VerifyTranslationErr = strings.TrimSpace(transErr)
	job.VerifyTrustOK = &trustOK
	job.VerifyTrustErr = strings.TrimSpace(trustErr)

	job.Step = updateStepDone
	job.Status = models.UpdateJobStatusOK
	job.Note = "updated"
	job.ErrorCode = ""
	job.ErrorMessage = ""

	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
		ub.Set("UpdateStatus", models.UpdateJobStatusOK)
		ub.Set("UpdateJobID", strings.TrimSpace(job.ID))
		if strings.TrimSpace(job.LesserVersion) != "" {
			ub.Set("LesserVersion", strings.TrimSpace(job.LesserVersion))
		}
		ub.Set("TranslationEnabled", job.TranslationEnabled)
		if strings.TrimSpace(job.LesserHostBaseURL) != "" {
			ub.Set("LesserHostBaseURL", strings.TrimSpace(job.LesserHostBaseURL))
		}
		if strings.TrimSpace(job.LesserHostAttestationsURL) != "" {
			ub.Set("LesserHostAttestationsURL", strings.TrimSpace(job.LesserHostAttestationsURL))
		}
		if strings.TrimSpace(job.LesserHostInstanceKeySecretARN) != "" {
			ub.Set("LesserHostInstanceKeySecretARN", strings.TrimSpace(job.LesserHostInstanceKeySecretARN))
		}
		return nil
	}); err != nil {
		return 0, false, err
	}

	return 0, true, nil
}
