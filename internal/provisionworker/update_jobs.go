package provisionworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	updateStepQueued            = "queued"
	updateStepInstanceConfig    = "instance.config"
	updateStepDeployStart       = "deploy.start"
	updateStepDeployClaimed     = "deploy.start.claimed"
	updateStepDeployWait        = "deploy.wait"
	updateStepReceiptIngest     = "receipt.ingest"
	updateStepBodyDeployStart   = "body.deploy.start"
	updateStepBodyDeployClaimed = "body.deploy.start.claimed"
	updateStepBodyDeployWait    = "body.deploy.wait"
	updateStepDeployMcpStart    = "deploy.mcp.start"
	updateStepDeployMcpClaimed  = "deploy.mcp.start.claimed"
	updateStepDeployMcpWait     = "deploy.mcp.wait" // #nosec G101 -- step identifier, not a credential
	updateStepVerify            = "verify"
	updateStepDone              = "done"
	updateStepFailed            = "failed"

	updateMaxTransitionsPerRun   = 6
	updateRunnerStartClaimMaxAge = 2 * time.Minute
)

type updateStepHandler func(*Server, context.Context, *models.UpdateJob, string, time.Time) (time.Duration, bool, error)

var managedUpdateStepHandlers = map[string]updateStepHandler{
	updateStepQueued:            (*Server).advanceUpdateQueued,
	updateStepInstanceConfig:    (*Server).advanceUpdateInstanceConfig,
	updateStepDeployStart:       (*Server).advanceUpdateDeployStart,
	updateStepDeployClaimed:     (*Server).advanceUpdateDeployClaimed,
	updateStepDeployWait:        (*Server).advanceUpdateDeployWait,
	updateStepReceiptIngest:     (*Server).advanceUpdateReceiptIngest,
	updateStepBodyDeployStart:   (*Server).advanceUpdateBodyDeployStart,
	updateStepBodyDeployClaimed: (*Server).advanceUpdateBodyDeployClaimed,
	updateStepBodyDeployWait:    (*Server).advanceUpdateBodyDeployWait,
	updateStepDeployMcpStart:    (*Server).advanceUpdateDeployMcpStart,
	updateStepDeployMcpClaimed:  (*Server).advanceUpdateDeployMcpClaimed,
	updateStepDeployMcpWait:     (*Server).advanceUpdateDeployMcpWait,
	updateStepVerify:            (*Server).advanceUpdateVerify,
	updateStepDone:              (*Server).advanceUpdateDone,
}

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

	return s.persistModelAndInstance(ctx, job, strings.TrimSpace(job.InstanceSlug), instanceUpdate)
}

func (s *Server) requeueUpdateJob(ctx context.Context, jobID string, delay time.Duration) error {
	return s.requeueJob(ctx, provisioning.JobMessage{Kind: "update_job", JobID: strings.TrimSpace(jobID)}, delay)
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
	if job.BodyOnly {
		job.Note = "starting lesser-body update"
	} else {
		job.Note = "starting update"
	}
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
	for transitions := 0; transitions < updateMaxTransitionsPerRun; transitions++ {
		if !updateJobProcessable(job) {
			return nil
		}

		step := strings.TrimSpace(job.Step)
		handler, ok := managedUpdateStepHandlers[step]
		if !ok || handler == nil {
			return s.failUpdateJob(ctx, job, requestID, now, "invalid_step", "unknown update job step: "+step)
		}

		stepDelay, done, err := handler(s, ctx, job, requestID, now)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		delay = stepDelay
		if delay > 0 {
			break
		}
	}

	if delay > 0 {
		return s.requeueUpdateJob(ctx, strings.TrimSpace(job.ID), delay)
	}

	// Safety: if we progressed quickly through multiple steps, requeue to continue.
	if updateJobProcessable(job) {
		return s.requeueUpdateJob(ctx, strings.TrimSpace(job.ID), provisionDefaultShortRetryDelay)
	}
	return nil
}

func (s *Server) advanceUpdateQueued(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	job.Step = updateStepInstanceConfig
	job.Note = noteEnsuringInstanceConfiguration
	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceUpdateDone(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	return 0, true, nil
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

type managedUpdateMetadata struct {
	accountID  string
	roleName   string
	region     string
	baseDomain string
}

func (s *Server) resolveManagedUpdateMetadata(job *models.UpdateJob, inst *models.Instance) (managedUpdateMetadata, error) {
	if s == nil || job == nil || inst == nil {
		return managedUpdateMetadata{}, fmt.Errorf("managed instance account metadata is missing")
	}

	md := managedUpdateMetadata{
		accountID:  strings.TrimSpace(inst.HostedAccountID),
		region:     strings.TrimSpace(inst.HostedRegion),
		baseDomain: strings.TrimSpace(inst.HostedBaseDomain),
		roleName:   strings.TrimSpace(job.AccountRoleName),
	}
	if md.roleName == "" {
		md.roleName = strings.TrimSpace(s.cfg.ManagedInstanceRoleName)
	}

	if md.accountID == "" || md.region == "" || md.baseDomain == "" || md.roleName == "" {
		return managedUpdateMetadata{}, fmt.Errorf("managed instance account metadata is missing")
	}
	return md, nil
}

func (s *Server) resolveUpdateHostURLs(job *models.UpdateJob) (string, string) {
	if s == nil || job == nil {
		return "", ""
	}

	publicBaseURL := strings.TrimSpace(job.LesserHostBaseURL)
	if publicBaseURL == "" {
		publicBaseURL = strings.TrimSpace(s.publicBaseURL())
	}
	attestationsURL := strings.TrimSpace(job.LesserHostAttestationsURL)
	if attestationsURL == "" {
		attestationsURL = publicBaseURL
	}

	return publicBaseURL, attestationsURL
}

func shouldRotateUpdateInstanceKey(job *models.UpdateJob) bool {
	if job == nil {
		return false
	}
	if !job.RotateInstanceKey {
		return false
	}
	return strings.TrimSpace(job.RotatedInstanceKeyID) == ""
}

func updateInstanceConfigInstanceUpdate(publicBaseURL, attestationsURL, secretArn string, job *models.UpdateJob) func(core.UpdateBuilder) error {
	return func(ub core.UpdateBuilder) error {
		if strings.TrimSpace(publicBaseURL) != "" {
			ub.Set("LesserHostBaseURL", strings.TrimSpace(publicBaseURL))
			ub.Set("LesserHostAttestationsURL", strings.TrimSpace(attestationsURL))
		}
		if strings.TrimSpace(secretArn) != "" {
			ub.Set("LesserHostInstanceKeySecretARN", strings.TrimSpace(secretArn))
		}
		ub.Set("TranslationEnabled", job.TranslationEnabled)
		ub.Set("TipEnabled", job.TipEnabled)
		ub.Set("TipChainID", job.TipChainID)
		ub.Set("TipContractAddress", strings.TrimSpace(job.TipContractAddress))
		ub.Set("LesserAIEnabled", job.AIEnabled)
		ub.Set("LesserAIModerationEnabled", job.AIModerationEnabled)
		ub.Set("LesserAINsfwDetectionEnabled", job.AINsfwDetectionEnabled)
		ub.Set("LesserAISpamDetectionEnabled", job.AISpamDetectionEnabled)
		ub.Set("LesserAIPiiDetectionEnabled", job.AIPiiDetectionEnabled)
		ub.Set("LesserAIContentDetectionEnabled", job.AIContentDetectionEnabled)
		return nil
	}
}

func (s *Server) advanceUpdateInstanceConfig(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if err := s.requireStoreDB(); err != nil {
		return 0, false, err
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

	md, mdErr := s.resolveManagedUpdateMetadata(job, inst)
	if mdErr != nil {
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, "missing_instance_metadata", mdErr.Error())
	}

	publicBaseURL, attestationsURL := s.resolveUpdateHostURLs(job)

	// Ensure the instance key secret exists in the instance account (and the InstanceKey record exists in lesser-host state).
	pseudo := &models.ProvisionJob{
		ID:              strings.TrimSpace(job.ID),
		InstanceSlug:    strings.TrimSpace(job.InstanceSlug),
		AccountID:       md.accountID,
		AccountRoleName: md.roleName,
		Region:          md.region,
	}
	secretArn, err := s.ensureManagedInstanceKeySecret(ctx, pseudo, inst)
	if err != nil {
		return s.retryUpdateJobOrFail(ctx, job, requestID, now, "instance_key_secret_failed", "failed to ensure instance key secret: "+err.Error(), provisionDefaultShortRetryDelay, 5*time.Minute)
	}

	if shouldRotateUpdateInstanceKey(job) {
		keyID, err := s.rotateManagedInstanceKeySecret(ctx, pseudo, secretArn)
		if err != nil {
			return s.retryUpdateJobOrFail(ctx, job, requestID, now, "instance_key_rotation_failed", "failed to rotate instance key: "+err.Error(), provisionDefaultShortRetryDelay, 5*time.Minute)
		}
		job.RotatedInstanceKeyID = strings.TrimSpace(keyID)
	}

	job.AccountID = md.accountID
	job.Region = md.region
	job.BaseDomain = md.baseDomain
	job.LesserHostBaseURL = publicBaseURL
	job.LesserHostAttestationsURL = attestationsURL
	job.LesserHostInstanceKeySecretARN = strings.TrimSpace(secretArn)
	if effectiveBodyEnabled(inst.BodyEnabled) && strings.TrimSpace(job.LesserBodyVersion) == "" {
		job.LesserBodyVersion = strings.TrimSpace(s.cfg.ManagedLesserBodyDefaultVersion)
	}
	if !effectiveBodyEnabled(inst.BodyEnabled) {
		job.LesserBodyVersion = ""
	}
	job.TipEnabled = effectiveTipEnabled(inst.TipEnabled)
	job.TipChainID = inst.TipChainID
	job.TipContractAddress = strings.TrimSpace(inst.TipContractAddress)
	job.AIEnabled = effectiveLesserAIEnabled(inst.LesserAIEnabled)
	job.AIModerationEnabled = effectiveLesserAIModerationEnabled(inst.LesserAIModerationEnabled)
	job.AINsfwDetectionEnabled = effectiveLesserAINsfwDetectionEnabled(inst.LesserAINsfwDetectionEnabled)
	job.AISpamDetectionEnabled = effectiveLesserAISpamDetectionEnabled(inst.LesserAISpamDetectionEnabled)
	job.AIPiiDetectionEnabled = effectiveLesserAIPiiDetectionEnabled(inst.LesserAIPiiDetectionEnabled)
	job.AIContentDetectionEnabled = effectiveLesserAIContentDetectionEnabled(inst.LesserAIContentDetectionEnabled)
	if job.BodyOnly {
		job.Step = updateStepBodyDeployStart
		job.Note = noteStartingLesserBodyDeployRunner
	} else {
		job.Step = updateStepDeployStart
		job.Note = "starting update deploy runner"
	}

	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, updateInstanceConfigInstanceUpdate(publicBaseURL, attestationsURL, secretArn, job)); err != nil {
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

func (s *Server) updateBodyReceiptS3Key(job *models.UpdateJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/updates/%s/%s/body-state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}

func (s *Server) updateMcpReceiptS3Key(job *models.UpdateJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/updates/%s/%s/mcp-state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
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

type updateDeployRunnerInputs struct {
	accountID                 string
	roleName                  string
	region                    string
	baseDomain                string
	lesserVersion             string
	instanceKeySecretArn      string
	adminWallet               string
	stage                     string
	receiptKey                string
	bootstrapKey              string
	lesserHostURL             string
	lesserHostAttestationsURL string
}

func (s *Server) resolveUpdateDeployRunnerInputs(job *models.UpdateJob, inst *models.Instance) (updateDeployRunnerInputs, error) {
	if s == nil {
		return updateDeployRunnerInputs{}, fmt.Errorf("server is nil")
	}
	if job == nil {
		return updateDeployRunnerInputs{}, fmt.Errorf("job is nil")
	}
	if inst == nil {
		return updateDeployRunnerInputs{}, fmt.Errorf("instance is nil")
	}

	inputs := updateDeployRunnerInputs{
		accountID:            strings.TrimSpace(job.AccountID),
		roleName:             strings.TrimSpace(job.AccountRoleName),
		region:               strings.TrimSpace(job.Region),
		baseDomain:           strings.TrimSpace(job.BaseDomain),
		lesserVersion:        strings.TrimSpace(job.LesserVersion),
		instanceKeySecretArn: strings.TrimSpace(job.LesserHostInstanceKeySecretARN),
	}
	if inputs.accountID == "" || inputs.roleName == "" || inputs.region == "" || inputs.baseDomain == "" || inputs.lesserVersion == "" {
		return updateDeployRunnerInputs{}, fmt.Errorf("missing required update job deploy inputs")
	}
	if inputs.instanceKeySecretArn == "" {
		return updateDeployRunnerInputs{}, fmt.Errorf("instance key secret arn is missing")
	}

	inputs.adminWallet = walletAddressFromUsername(strings.TrimSpace(inst.Owner))
	if inputs.adminWallet == "" {
		return updateDeployRunnerInputs{}, fmt.Errorf("instance owner is not a wallet username")
	}

	inputs.stage = normalizeManagedLesserStage(strings.TrimSpace(s.cfg.Stage))
	inputs.receiptKey = s.updateReceiptS3Key(job)
	inputs.bootstrapKey = s.updateBootstrapS3Key(strings.TrimSpace(job.InstanceSlug))

	inputs.lesserHostURL = strings.TrimSpace(job.LesserHostBaseURL)
	if inputs.lesserHostURL == "" {
		inputs.lesserHostURL = strings.TrimSpace(s.publicBaseURL())
	}
	inputs.lesserHostAttestationsURL = strings.TrimSpace(job.LesserHostAttestationsURL)
	if inputs.lesserHostAttestationsURL == "" {
		inputs.lesserHostAttestationsURL = inputs.lesserHostURL
	}
	if inputs.lesserHostURL == "" {
		return updateDeployRunnerInputs{}, fmt.Errorf("lesser host base url is missing")
	}

	return inputs, nil
}

func (s *Server) buildUpdateDeployRunnerEnv(job *models.UpdateJob, inputs updateDeployRunnerInputs) []cbtypes.EnvironmentVariable {
	if s == nil || job == nil {
		return nil
	}

	tipEnabled := job.TipEnabled
	lesserBodyVersion := strings.TrimSpace(job.LesserBodyVersion)
	if lesserBodyVersion == "" {
		lesserBodyVersion = strings.TrimSpace(s.cfg.ManagedLesserBodyDefaultVersion)
	}
	env := []cbtypes.EnvironmentVariable{
		{Name: aws.String("JOB_ID"), Value: aws.String(strings.TrimSpace(job.ID))},
		{Name: aws.String("APP_SLUG"), Value: aws.String(strings.TrimSpace(job.InstanceSlug))},
		{Name: aws.String("STAGE"), Value: aws.String(inputs.stage)},
		{Name: aws.String("ADMIN_USERNAME"), Value: aws.String(strings.TrimSpace(job.InstanceSlug))},
		{Name: aws.String("ADMIN_WALLET_ADDRESS"), Value: aws.String(inputs.adminWallet)},
		{Name: aws.String("BASE_DOMAIN"), Value: aws.String(inputs.baseDomain)},
		{Name: aws.String("TARGET_ACCOUNT_ID"), Value: aws.String(inputs.accountID)},
		{Name: aws.String("TARGET_ROLE_NAME"), Value: aws.String(inputs.roleName)},
		{Name: aws.String("TARGET_REGION"), Value: aws.String(inputs.region)},
		{Name: aws.String("LESSER_VERSION"), Value: aws.String(inputs.lesserVersion)},
		{Name: aws.String("ARTIFACT_BUCKET"), Value: aws.String(strings.TrimSpace(s.cfg.ArtifactBucketName))},
		{Name: aws.String("RECEIPT_S3_KEY"), Value: aws.String(inputs.receiptKey)},
		{Name: aws.String("BOOTSTRAP_S3_KEY"), Value: aws.String(inputs.bootstrapKey)},
		{Name: aws.String("GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubOwner))},
		{Name: aws.String("GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubRepo))},
		{Name: aws.String("LESSER_BODY_GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserBodyGitHubOwner))},
		{Name: aws.String("LESSER_BODY_GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserBodyGitHubRepo))},

		{Name: aws.String("LESSER_HOST_URL"), Value: aws.String(inputs.lesserHostURL)},
		{Name: aws.String("LESSER_HOST_ATTESTATIONS_URL"), Value: aws.String(inputs.lesserHostAttestationsURL)},
		{Name: aws.String("LESSER_HOST_INSTANCE_KEY_ARN"), Value: aws.String(inputs.instanceKeySecretArn)},
		{Name: aws.String("TRANSLATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.TranslationEnabled))},
		{Name: aws.String("TIP_ENABLED"), Value: aws.String(fmt.Sprintf("%t", tipEnabled))},
		{Name: aws.String("AI_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AIEnabled))},
		{Name: aws.String("AI_MODERATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AIModerationEnabled))},
		{Name: aws.String("AI_NSFW_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AINsfwDetectionEnabled))},
		{Name: aws.String("AI_SPAM_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AISpamDetectionEnabled))},
		{Name: aws.String("AI_PII_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AIPiiDetectionEnabled))},
		{Name: aws.String("AI_CONTENT_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AIContentDetectionEnabled))},
	}
	if lesserBodyVersion != "" {
		env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("LESSER_BODY_VERSION"), Value: aws.String(lesserBodyVersion)})
	}
	if tipEnabled {
		env = append(env,
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CHAIN_ID"), Value: aws.String(fmt.Sprintf("%d", job.TipChainID))},
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CONTRACT_ADDRESS"), Value: aws.String(strings.TrimSpace(job.TipContractAddress))},
		)
	}

	if strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN) != "" {
		env = append(env, cbtypes.EnvironmentVariable{
			Name:  aws.String("MANAGED_ORG_VENDING_ROLE_ARN"),
			Value: aws.String(strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN)),
		})
	}

	return env
}

func (s *Server) startUpdateDeployRunnerWithMode(ctx context.Context, job *models.UpdateJob, inst *models.Instance, mode string, receiptKey string) (string, error) {
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

	inputs, err := s.resolveUpdateDeployRunnerInputs(job, inst)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(receiptKey) != "" {
		inputs.receiptKey = strings.TrimSpace(receiptKey)
	}
	env := s.buildUpdateDeployRunnerEnv(job, inputs)

	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "lesser"
	}
	env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("RUN_MODE"), Value: aws.String(mode)})

	idempotencyToken := codebuildIdempotencyToken(
		projectName,
		inputs.stage,
		strings.TrimSpace(job.InstanceSlug),
		strings.TrimSpace(job.ID),
		mode,
		strings.TrimSpace(inputs.receiptKey),
	)
	startIn := &codebuild.StartBuildInput{
		ProjectName:                  aws.String(projectName),
		EnvironmentVariablesOverride: env,
	}
	if idempotencyToken != "" {
		startIn.IdempotencyToken = aws.String(idempotencyToken)
	}

	out, err := s.cb.StartBuild(ctx, startIn)
	if err != nil {
		return "", err
	}
	return codebuildBuildID(out)
}

func (s *Server) startUpdateDeployRunner(ctx context.Context, job *models.UpdateJob, inst *models.Instance) (string, error) {
	return s.startUpdateDeployRunnerWithMode(ctx, job, inst, "lesser", "")
}

func updateJobKey(jobID string) *models.UpdateJob {
	jobKey := &models.UpdateJob{ID: strings.TrimSpace(jobID)}
	_ = jobKey.UpdateKeys()
	return jobKey
}

func updateRunnerRunIDUnsetCondition() core.TransactCondition {
	return tabletheory.ConditionExpression(
		"attribute_not_exists(runId) OR runId = :empty",
		map[string]any{":empty": ""},
	)
}

func updateRunnerClaimExpired(job *models.UpdateJob, now time.Time) bool {
	if job == nil {
		return false
	}
	claimedAt := job.UpdatedAt
	if claimedAt.IsZero() {
		claimedAt = job.CreatedAt
	}
	if claimedAt.IsZero() {
		return true
	}
	return now.Sub(claimedAt) > updateRunnerStartClaimMaxAge
}

func (s *Server) claimUpdateRunnerStart(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	expectedStep string,
	claimedStep string,
	claimedNote string,
) (bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return false, fmt.Errorf("store not initialized")
	}
	if job == nil {
		return false, fmt.Errorf("job is nil")
	}

	err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(updateJobKey(job.ID), func(ub core.UpdateBuilder) error {
			ub.Set("Step", strings.TrimSpace(claimedStep))
			ub.Set("Note", strings.TrimSpace(claimedNote))
			ub.Set("RequestID", strings.TrimSpace(requestID))
			ub.Set("UpdatedAt", now)
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.UpdateJobStatusRunning),
			tabletheory.Condition("Step", "=", strings.TrimSpace(expectedStep)),
			updateRunnerRunIDUnsetCondition(),
		)
		return nil
	})
	if theoryErrors.IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	job.Step = strings.TrimSpace(claimedStep)
	job.Note = strings.TrimSpace(claimedNote)
	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	return true, nil
}

func (s *Server) releaseClaimedUpdateRunnerStartForRetry(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	claimedStep string,
	retryStep string,
	retryNote string,
	attempts int64,
) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return fmt.Errorf("job is nil")
	}

	err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(updateJobKey(job.ID), func(ub core.UpdateBuilder) error {
			ub.Set("Step", strings.TrimSpace(retryStep))
			ub.Set("Note", strings.TrimSpace(retryNote))
			ub.Set("Attempts", attempts)
			ub.Set("RequestID", strings.TrimSpace(requestID))
			ub.Set("UpdatedAt", now)
			ub.Remove("RunID")
			ub.Remove("RunURL")
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.UpdateJobStatusRunning),
			tabletheory.Condition("Step", "=", strings.TrimSpace(claimedStep)),
		)
		return nil
	})
	if err != nil {
		return err
	}

	job.Step = strings.TrimSpace(retryStep)
	job.Note = strings.TrimSpace(retryNote)
	job.Attempts = attempts
	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	job.RunID = ""
	job.RunURL = ""
	return nil
}

func (s *Server) completeClaimedUpdateRunnerStart(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	claimedStep string,
	waitStep string,
	runID string,
	inProgressNote string,
) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return fmt.Errorf("job is nil")
	}

	runID = strings.TrimSpace(runID)
	err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(updateJobKey(job.ID), func(ub core.UpdateBuilder) error {
			ub.Set("Step", strings.TrimSpace(waitStep))
			ub.Set("RunID", runID)
			ub.Set("Note", strings.TrimSpace(inProgressNote))
			ub.Set("RequestID", strings.TrimSpace(requestID))
			ub.Set("UpdatedAt", now)
			ub.Remove("RunURL")
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.UpdateJobStatusRunning),
			tabletheory.Condition("Step", "=", strings.TrimSpace(claimedStep)),
		)
		return nil
	})
	if err != nil {
		return err
	}

	job.Step = strings.TrimSpace(waitStep)
	job.RunID = runID
	job.RunURL = ""
	job.Note = strings.TrimSpace(inProgressNote)
	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	return nil
}

func (s *Server) failClaimedUpdateJob(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	claimedStep string,
	code string,
	msg string,
) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return nil
	}

	updateInst := &models.Instance{Slug: strings.TrimSpace(job.InstanceSlug)}
	_ = updateInst.UpdateKeys()

	err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(updateJobKey(job.ID), func(ub core.UpdateBuilder) error {
			ub.Set("Status", models.UpdateJobStatusError)
			ub.Set("Step", updateStepFailed)
			ub.Set("ErrorCode", strings.TrimSpace(code))
			ub.Set("ErrorMessage", strings.TrimSpace(msg))
			ub.Set("RequestID", strings.TrimSpace(requestID))
			ub.Set("UpdatedAt", now)
			ub.Remove("RunID")
			ub.Remove("RunURL")
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.UpdateJobStatusRunning),
			tabletheory.Condition("Step", "=", strings.TrimSpace(claimedStep)),
		)
		tx.UpdateWithBuilder(updateInst, func(ub core.UpdateBuilder) error {
			ub.Set("UpdateStatus", models.UpdateJobStatusError)
			ub.Set("UpdateJobID", strings.TrimSpace(job.ID))
			return nil
		}, tabletheory.IfExists())
		return nil
	})
	if err != nil {
		return err
	}

	job.Status = models.UpdateJobStatusError
	job.Step = updateStepFailed
	job.ErrorCode = strings.TrimSpace(code)
	job.ErrorMessage = strings.TrimSpace(msg)
	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	job.RunID = ""
	job.RunURL = ""
	return nil
}

type updateRunnerStartSpec struct {
	expectedStep     string
	claimedStep      string
	waitStep         string
	claimedNote      string
	inProgressNote   string
	runnerLabel      string
	startFailureCode string
	startRunner      func(context.Context, *models.UpdateJob, *models.Instance) (string, error)
}

func (s *Server) retryClaimedUpdateRunnerStart(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	spec updateRunnerStartSpec,
	startErr error,
) (time.Duration, bool, error) {
	nextAttempts := job.Attempts + 1
	if nextAttempts >= job.MaxAttempts {
		failErr := s.failClaimedUpdateJob(
			ctx,
			job,
			requestID,
			now,
			spec.claimedStep,
			spec.startFailureCode,
			"failed to start "+spec.runnerLabel+": "+startErr.Error(),
		)
		if theoryErrors.IsConditionFailed(failErr) {
			return provisionDefaultShortRetryDelay, false, nil
		}
		return 0, false, failErr
	}

	retryNote := "failed to start " + spec.runnerLabel + "; retrying: " + compactErr(startErr)
	releaseErr := s.releaseClaimedUpdateRunnerStartForRetry(
		ctx,
		job,
		requestID,
		now,
		spec.claimedStep,
		spec.expectedStep,
		retryNote,
		nextAttempts,
	)
	if theoryErrors.IsConditionFailed(releaseErr) {
		return provisionDefaultShortRetryDelay, false, nil
	}
	if releaseErr != nil {
		return 0, false, releaseErr
	}
	return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 10*time.Minute), false, nil
}

func (s *Server) advanceUpdateRunnerAlreadyStarted(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	waitStep string,
	note string,
) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	job.Step = strings.TrimSpace(waitStep)
	job.Note = strings.TrimSpace(note)
	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceUpdateRunnerStartWithInstance(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	inst *models.Instance,
	spec updateRunnerStartSpec,
) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	claimed, err := s.claimUpdateRunnerStart(ctx, job, requestID, now, spec.expectedStep, spec.claimedStep, spec.claimedNote)
	if err != nil {
		return 0, false, err
	}
	if !claimed {
		return provisionDefaultShortRetryDelay, false, nil
	}

	runID, err := spec.startRunner(ctx, job, inst)
	if err != nil {
		return s.retryClaimedUpdateRunnerStart(ctx, job, requestID, now, spec, err)
	}

	if err := s.completeClaimedUpdateRunnerStart(ctx, job, requestID, now, spec.claimedStep, spec.waitStep, runID, spec.inProgressNote); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

type updateRunnerClaimSpec struct {
	claimedStep  string
	staleCode    string
	staleMessage string
}

func (s *Server) advanceUpdateRunnerClaimed(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	spec updateRunnerClaimSpec,
) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}
	if !updateRunnerClaimExpired(job, now) {
		return provisionDefaultShortRetryDelay, false, nil
	}

	err := s.failClaimedUpdateJob(ctx, job, requestID, now, spec.claimedStep, spec.staleCode, spec.staleMessage)
	if theoryErrors.IsConditionFailed(err) {
		return provisionDefaultShortRetryDelay, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceUpdateDeployStart(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}
	if strings.TrimSpace(job.RunID) != "" {
		return s.advanceUpdateRunnerAlreadyStarted(ctx, job, requestID, now, updateStepDeployWait, "deploy runner already started")
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
		job.Note = noteEnsuringInstanceConfiguration
		persistErr := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		if persistErr != nil {
			return 0, false, persistErr
		}
		return 0, false, nil
	}
	if strings.TrimSpace(job.LesserHostInstanceKeySecretARN) == "" {
		job.LesserHostInstanceKeySecretARN = strings.TrimSpace(inst.LesserHostInstanceKeySecretARN)
	}

	return s.advanceUpdateRunnerStartWithInstance(ctx, job, requestID, now, inst, updateRunnerStartSpec{
		expectedStep:     updateStepDeployStart,
		claimedStep:      updateStepDeployClaimed,
		waitStep:         updateStepDeployWait,
		claimedNote:      noteStartingDeployRunner,
		inProgressNote:   noteDeployRunnerInProgress,
		runnerLabel:      "deploy runner",
		startFailureCode: "deploy_start_failed",
		startRunner:      s.startUpdateDeployRunner,
	})
}

func (s *Server) advanceUpdateDeployClaimed(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	return s.advanceUpdateRunnerClaimed(ctx, job, requestID, now, updateRunnerClaimSpec{
		claimedStep:  updateStepDeployClaimed,
		staleCode:    "deploy_start_claim_stale",
		staleMessage: "deploy runner start claim expired before a run ID was recorded; refusing to launch a duplicate deploy runner automatically",
	})
}

func (s *Server) advanceUpdateDeployWait(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
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
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, "deploy_timeout", "deploy runner timed out")
		}
		job.Note = noteDeployRunnerInProgress
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
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, "deploy_timeout", "deploy runner timed out")
		}
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
	job.RunID = ""

	if strings.TrimSpace(job.LesserBodyVersion) != "" {
		job.Step = updateStepBodyDeployStart
		job.Note = noteStartingLesserBodyDeployRunner
	} else {
		job.Step = updateStepVerify
		job.Note = noteVerifyingDeployment
	}
	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceUpdateBodyDeployStart(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	if strings.TrimSpace(job.LesserBodyVersion) == "" {
		job.Step = updateStepVerify
		job.Note = noteVerifyingDeployment
		job.RunID = ""
		if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}

	if strings.TrimSpace(job.RunID) != "" {
		return s.advanceUpdateRunnerAlreadyStarted(ctx, job, requestID, now, updateStepBodyDeployWait, "lesser-body deploy runner already started")
	}

	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return s.retryUpdateJobOrFail(ctx, job, requestID, now, "instance_load_failed", "failed to load instance: "+err.Error(), provisionDefaultShortRetryDelay, 2*time.Minute)
	}
	if inst == nil {
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, "instance_not_found", "instance record not found")
	}

	return s.advanceUpdateRunnerStartWithInstance(ctx, job, requestID, now, inst, updateRunnerStartSpec{
		expectedStep:     updateStepBodyDeployStart,
		claimedStep:      updateStepBodyDeployClaimed,
		waitStep:         updateStepBodyDeployWait,
		claimedNote:      noteStartingLesserBodyDeployRunner,
		inProgressNote:   noteLesserBodyDeployRunnerInProgress,
		runnerLabel:      "lesser-body deploy runner",
		startFailureCode: "body_deploy_start_failed",
		startRunner: func(ctx context.Context, job *models.UpdateJob, inst *models.Instance) (string, error) {
			return s.startUpdateDeployRunnerWithMode(ctx, job, inst, "lesser-body", s.updateBodyReceiptS3Key(job))
		},
	})
}

func (s *Server) advanceUpdateBodyDeployClaimed(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	return s.advanceUpdateRunnerClaimed(ctx, job, requestID, now, updateRunnerClaimSpec{
		claimedStep:  updateStepBodyDeployClaimed,
		staleCode:    "body_deploy_start_claim_stale",
		staleMessage: "lesser-body deploy runner start claim expired before a run ID was recorded; refusing to launch a duplicate lesser-body deploy runner automatically",
	})
}

type updateRunnerWaitSpec struct {
	pollFailureCode    string
	pollFailureMessage string
	successStep        string
	successNote        string
	inProgressNote     string
	timeoutCode        string
	timeoutMessage     string
	failedCode         string
	failedMessage      string
	statusPrefix       string
}

func (s *Server) advanceUpdateRunnerWait(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	spec updateRunnerWaitSpec,
) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	status, deepLink, err := s.getDeployRunnerStatus(ctx, strings.TrimSpace(job.RunID))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, spec.pollFailureCode, spec.pollFailureMessage+err.Error())
		}
		job.Note = spec.pollFailureMessage + compactErr(err)
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultPollDelay, 10*time.Minute), false, nil
	}

	if deepLink != "" && strings.TrimSpace(job.RunURL) == "" {
		job.RunURL = deepLink
	}

	switch status {
	case codebuildStatusSucceeded:
		job.RunID = ""
		job.Step = spec.successStep
		job.Note = spec.successNote
		if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil

	case codebuildStatusInProgress:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, spec.timeoutCode, spec.timeoutMessage)
		}
		job.Note = spec.inProgressNote
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil

	case codebuildStatusFailed, codebuildStatusFault, codebuildStatusStopped, codebuildStatusTimedOut:
		msg := spec.failedMessage
		if deepLink != "" {
			job.RunURL = deepLink
			msg += " (CodeBuild: " + deepLink + ")"
		}
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, spec.failedCode, msg)

	default:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, spec.timeoutCode, spec.timeoutMessage)
		}
		job.Note = spec.statusPrefix + status
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil
	}
}

func (s *Server) advanceUpdateBodyDeployWait(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	return s.advanceUpdateRunnerWait(ctx, job, requestID, now, updateRunnerWaitSpec{
		pollFailureCode:    "body_deploy_status_failed",
		pollFailureMessage: "failed to poll lesser-body deploy runner: ",
		successStep:        updateStepDeployMcpStart,
		successNote:        noteStartingMcpWiringDeployRunner,
		inProgressNote:     noteLesserBodyDeployRunnerInProgress,
		timeoutCode:        "body_deploy_timeout",
		timeoutMessage:     "lesser-body deploy runner timed out",
		failedCode:         "body_deploy_failed",
		failedMessage:      "lesser-body deploy runner failed",
		statusPrefix:       "lesser-body deploy runner status: ",
	})
}

func (s *Server) advanceUpdateDeployMcpStart(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}
	if strings.TrimSpace(job.RunID) != "" {
		return s.advanceUpdateRunnerAlreadyStarted(ctx, job, requestID, now, updateStepDeployMcpWait, "MCP wiring deploy runner already started")
	}

	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return s.retryUpdateJobOrFail(ctx, job, requestID, now, "instance_load_failed", "failed to load instance: "+err.Error(), provisionDefaultShortRetryDelay, 2*time.Minute)
	}
	if inst == nil {
		return 0, false, s.failUpdateJob(ctx, job, requestID, now, "instance_not_found", "instance record not found")
	}

	return s.advanceUpdateRunnerStartWithInstance(ctx, job, requestID, now, inst, updateRunnerStartSpec{
		expectedStep:     updateStepDeployMcpStart,
		claimedStep:      updateStepDeployMcpClaimed,
		waitStep:         updateStepDeployMcpWait,
		claimedNote:      noteStartingMcpWiringDeployRunner,
		inProgressNote:   noteMCPDeployRunnerInProgress,
		runnerLabel:      "MCP wiring deploy runner",
		startFailureCode: "mcp_deploy_start_failed",
		startRunner: func(ctx context.Context, job *models.UpdateJob, inst *models.Instance) (string, error) {
			return s.startUpdateDeployRunnerWithMode(ctx, job, inst, "lesser-mcp", s.updateMcpReceiptS3Key(job))
		},
	})
}

func (s *Server) advanceUpdateDeployMcpClaimed(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	return s.advanceUpdateRunnerClaimed(ctx, job, requestID, now, updateRunnerClaimSpec{
		claimedStep:  updateStepDeployMcpClaimed,
		staleCode:    "mcp_deploy_start_claim_stale",
		staleMessage: "MCP wiring deploy runner start claim expired before a run ID was recorded; refusing to launch a duplicate MCP wiring deploy runner automatically",
	})
}

func (s *Server) advanceUpdateDeployMcpWait(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	return s.advanceUpdateRunnerWait(ctx, job, requestID, now, updateRunnerWaitSpec{
		pollFailureCode:    "mcp_deploy_status_failed",
		pollFailureMessage: "failed to poll MCP wiring deploy runner: ",
		successStep:        updateStepVerify,
		successNote:        noteVerifyingDeployment,
		inProgressNote:     noteMCPDeployRunnerInProgress,
		timeoutCode:        "mcp_deploy_timeout",
		timeoutMessage:     "MCP wiring deploy runner timed out",
		failedCode:         "mcp_deploy_failed",
		failedMessage:      "MCP wiring deploy runner failed",
		statusPrefix:       "MCP wiring deploy runner status: ",
	})
}

type instanceV2Response struct {
	Configuration struct {
		Translation struct {
			Enabled bool `json:"enabled"`
		} `json:"translation"`
		Trust struct {
			Enabled bool   `json:"enabled"`
			BaseURL string `json:"base_url"`
		} `json:"trust"`
		Tips struct {
			Enabled         bool   `json:"enabled"`
			ChainID         int64  `json:"chain_id"`
			ContractAddress string `json:"contract_address"`
		} `json:"tips"`
	} `json:"configuration"`
}

func normalizeVerifyHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("base domain is required")
	}

	// Accept either a bare host (`example.com`, `127.0.0.1:443`) or a full URL.
	if strings.Contains(raw, "://") {
		if parsed, err := url.Parse(raw); err == nil && strings.TrimSpace(parsed.Host) != "" {
			return strings.TrimSpace(parsed.Host), nil
		}
	}

	host := strings.TrimPrefix(raw, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("base domain is required")
	}
	return host, nil
}

func fetchInstanceConfigV2(ctx context.Context, client *http.Client, baseDomain string) (instanceV2Response, error) {
	var parsed instanceV2Response

	host, err := normalizeVerifyHost(baseDomain)
	if err != nil {
		return parsed, err
	}

	u := fmt.Sprintf("https://%s/api/v2/instance", host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return parsed, err
	}
	req.Header.Set("Accept", "application/json")

	if client == nil {
		client = ssrfProtectedHTTPClient(nil)
	}
	resp, err := client.Do(req) //nolint:gosec // SSRF mitigated by ssrfProtectedHTTPClient (verify path) or caller-provided transport in tests.
	if err != nil {
		return parsed, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parsed, fmt.Errorf("instance config request failed (HTTP %d)", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return parsed, err
	}
	return parsed, nil
}

func requireInstanceEndpoint2xx(ctx context.Context, client *http.Client, baseDomain string, path string) error {
	host, err := normalizeVerifyHost(baseDomain)
	if err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	u := fmt.Sprintf("https://%s%s", host, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	if client == nil {
		client = ssrfProtectedHTTPClient(nil)
	}
	resp, err := client.Do(req) //nolint:gosec // SSRF mitigated by ssrfProtectedHTTPClient (verify path) or caller-provided transport in tests.
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("endpoint request failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}

func updateVerifyHealthJobID(jobID string) string {
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
	plaintext, err := unwrapSecretsManagerSecretString(raw)
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

func verifyAIEndpoint(ctx context.Context, client *http.Client, baseURL string, instanceKey string, jobID string) (bool, string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceKey = strings.TrimSpace(instanceKey)
	if baseURL == "" {
		return false, "lesser host base url is missing"
	}
	if instanceKey == "" {
		return false, "instance key is missing"
	}

	healthID := updateVerifyHealthJobID(jobID)
	u := baseURL + "/api/v1/ai/jobs/" + healthID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+instanceKey)

	if client == nil {
		client = ssrfProtectedHTTPClient(nil)
	}
	resp, err := client.Do(req) //nolint:gosec // SSRF mitigated by ssrfProtectedHTTPClient (verify path) or caller-provided transport in tests.
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

func normalizeVerifyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, "/")
	return raw
}

func updateVerifyDomain(baseDomain, stage string) string {
	verifyDomain := strings.TrimSpace(baseDomain)
	managedStage := normalizeManagedLesserStage(strings.TrimSpace(stage))
	if managedStage != managedStageLive && verifyDomain != "" {
		verifyDomain = managedStage + "." + verifyDomain
	}
	return verifyDomain
}

func verifyUpdateTranslation(ctx context.Context, client *http.Client, verifyDomain string, cfg instanceV2Response, cfgErr error, expectedEnabled bool) (bool, string) {
	if cfgErr != nil {
		return false, strings.TrimSpace(cfgErr.Error())
	}
	if cfg.Configuration.Translation.Enabled != expectedEnabled {
		return false, fmt.Sprintf("expected %t, got %t", expectedEnabled, cfg.Configuration.Translation.Enabled)
	}
	if !expectedEnabled {
		return true, ""
	}
	if err := requireInstanceEndpoint2xx(ctx, client, verifyDomain, "/api/v1/instance/translation_languages"); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func resolveExpectedTrustBaseURL(job *models.UpdateJob, fallback string) string {
	if job == nil {
		return strings.TrimSpace(fallback)
	}
	expectedBaseURL := strings.TrimSpace(job.LesserHostAttestationsURL)
	if expectedBaseURL == "" {
		expectedBaseURL = strings.TrimSpace(job.LesserHostBaseURL)
	}
	if expectedBaseURL == "" {
		expectedBaseURL = strings.TrimSpace(fallback)
	}
	return expectedBaseURL
}

func verifyUpdateTrust(ctx context.Context, client *http.Client, verifyDomain string, cfg instanceV2Response, cfgErr error, expectedBaseURL string) (bool, string) {
	if cfgErr != nil {
		return false, strings.TrimSpace(cfgErr.Error())
	}
	if !cfg.Configuration.Trust.Enabled {
		return false, "disabled"
	}
	gotBaseURL := normalizeVerifyURL(cfg.Configuration.Trust.BaseURL)
	wantBaseURL := normalizeVerifyURL(expectedBaseURL)
	if gotBaseURL != wantBaseURL {
		return false, fmt.Sprintf("expected base_url %q, got %q", wantBaseURL, gotBaseURL)
	}
	if err := requireInstanceEndpoint2xx(ctx, client, verifyDomain, "/api/v1/trust/jwks.json"); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func verifyUpdateTips(cfg instanceV2Response, cfgErr error, expectedEnabled bool, expectedChainID int64, expectedContractAddress string) (bool, string) {
	if cfgErr != nil {
		return false, strings.TrimSpace(cfgErr.Error())
	}
	if cfg.Configuration.Tips.Enabled != expectedEnabled {
		return false, fmt.Sprintf("expected %t, got %t", expectedEnabled, cfg.Configuration.Tips.Enabled)
	}
	if !expectedEnabled {
		return true, ""
	}
	if cfg.Configuration.Tips.ChainID != expectedChainID {
		return false, fmt.Sprintf("expected chain_id %d, got %d", expectedChainID, cfg.Configuration.Tips.ChainID)
	}
	got := strings.TrimSpace(cfg.Configuration.Tips.ContractAddress)
	want := strings.TrimSpace(expectedContractAddress)
	if !strings.EqualFold(got, want) {
		return false, fmt.Sprintf("expected contract_address %q, got %q", want, got)
	}
	return true, ""
}

func (s *Server) verifyUpdateAI(ctx context.Context, client *http.Client, job *models.UpdateJob) (bool, string) {
	if s == nil || job == nil {
		return false, "internal error"
	}
	if !job.AIEnabled {
		return true, ""
	}

	key, err := s.resolveInstanceKeyPlaintext(ctx, job)
	if err != nil {
		return false, err.Error()
	}

	baseURL := strings.TrimSpace(job.LesserHostBaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(s.publicBaseURL())
	}
	return verifyAIEndpoint(ctx, client, baseURL, key, strings.TrimSpace(job.ID))
}

func updateVerifyInstanceUpdate(job *models.UpdateJob) func(core.UpdateBuilder) error {
	return func(ub core.UpdateBuilder) error {
		if job == nil {
			return nil
		}
		ub.Set("UpdateStatus", models.UpdateJobStatusOK)
		ub.Set("UpdateJobID", strings.TrimSpace(job.ID))
		if strings.TrimSpace(job.LesserVersion) != "" {
			ub.Set("LesserVersion", strings.TrimSpace(job.LesserVersion))
		}
		ub.Set("TranslationEnabled", job.TranslationEnabled)
		ub.Set("TipEnabled", job.TipEnabled)
		ub.Set("TipChainID", job.TipChainID)
		ub.Set("TipContractAddress", strings.TrimSpace(job.TipContractAddress))
		ub.Set("LesserAIEnabled", job.AIEnabled)
		ub.Set("LesserAIModerationEnabled", job.AIModerationEnabled)
		ub.Set("LesserAINsfwDetectionEnabled", job.AINsfwDetectionEnabled)
		ub.Set("LesserAISpamDetectionEnabled", job.AISpamDetectionEnabled)
		ub.Set("LesserAIPiiDetectionEnabled", job.AIPiiDetectionEnabled)
		ub.Set("LesserAIContentDetectionEnabled", job.AIContentDetectionEnabled)
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
	}
}

func (s *Server) advanceUpdateVerify(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if err := s.requireStoreDB(); err != nil {
		return 0, false, err
	}
	if job == nil {
		return 0, true, nil
	}

	verifyDomain := updateVerifyDomain(job.BaseDomain, s.cfg.Stage)
	client := ssrfProtectedHTTPClient(s.httpClient)

	cfg, cfgErr := fetchInstanceConfigV2(ctx, client, verifyDomain)
	transOK, transErr := verifyUpdateTranslation(ctx, client, verifyDomain, cfg, cfgErr, job.TranslationEnabled)
	expectedTrustBaseURL := resolveExpectedTrustBaseURL(job, s.publicBaseURL())
	trustOK, trustErr := verifyUpdateTrust(ctx, client, verifyDomain, cfg, cfgErr, expectedTrustBaseURL)
	tipsOK, tipsErr := verifyUpdateTips(cfg, cfgErr, job.TipEnabled, job.TipChainID, job.TipContractAddress)
	aiOK, aiErr := s.verifyUpdateAI(ctx, client, job)

	job.VerifyTranslationOK = &transOK
	job.VerifyTranslationErr = strings.TrimSpace(transErr)
	job.VerifyTrustOK = &trustOK
	job.VerifyTrustErr = strings.TrimSpace(trustErr)
	job.VerifyTipsOK = &tipsOK
	job.VerifyTipsErr = strings.TrimSpace(tipsErr)
	job.VerifyAIOK = &aiOK
	job.VerifyAIErr = strings.TrimSpace(aiErr)

	job.Step = updateStepDone
	job.Status = models.UpdateJobStatusOK
	job.Note = "updated"
	job.ErrorCode = ""
	job.ErrorMessage = ""

	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, updateVerifyInstanceUpdate(job)); err != nil {
		return 0, false, err
	}

	return 0, true, nil
}
