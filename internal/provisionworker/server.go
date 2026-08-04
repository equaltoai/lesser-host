package provisionworker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	"github.com/theory-cloud/tabletheory/v3"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type organizationsAPI interface {
	CreateAccount(ctx context.Context, params *organizations.CreateAccountInput, optFns ...func(*organizations.Options)) (*organizations.CreateAccountOutput, error)
	DescribeCreateAccountStatus(ctx context.Context, params *organizations.DescribeCreateAccountStatusInput, optFns ...func(*organizations.Options)) (*organizations.DescribeCreateAccountStatusOutput, error)
	ListAccounts(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error)
	ListParents(ctx context.Context, params *organizations.ListParentsInput, optFns ...func(*organizations.Options)) (*organizations.ListParentsOutput, error)
	MoveAccount(ctx context.Context, params *organizations.MoveAccountInput, optFns ...func(*organizations.Options)) (*organizations.MoveAccountOutput, error)
}

type route53API interface {
	ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
	CreateHostedZone(ctx context.Context, params *route53.CreateHostedZoneInput, optFns ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error)
	GetHostedZone(ctx context.Context, params *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
	ListHostedZonesByName(ctx context.Context, params *route53.ListHostedZonesByNameInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error)
}

type stsAPI interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

type sqsAPI interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type codebuildAPI interface {
	StartBuild(ctx context.Context, params *codebuild.StartBuildInput, optFns ...func(*codebuild.Options)) (*codebuild.StartBuildOutput, error)
	BatchGetBuilds(ctx context.Context, params *codebuild.BatchGetBuildsInput, optFns ...func(*codebuild.Options)) (*codebuild.BatchGetBuildsOutput, error)
}

type s3API interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type secretsManagerAPI interface {
	CreateSecret(ctx context.Context, params *secretsmanager.CreateSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
	DescribeSecret(ctx context.Context, params *secretsmanager.DescribeSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error)
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	UpdateSecret(ctx context.Context, params *secretsmanager.UpdateSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.UpdateSecretOutput, error)
	TagResource(ctx context.Context, params *secretsmanager.TagResourceInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.TagResourceOutput, error)
	UntagResource(ctx context.Context, params *secretsmanager.UntagResourceInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.UntagResourceOutput, error)
}

type secretsManagerClientFactory func(ctx context.Context, accountID string, roleName string, region string, slug string, jobID string) (secretsManagerAPI, error)

// Server processes provisioning jobs from the worker queue.
type Server struct {
	cfg config.Config

	store *store.Store

	httpClient        *http.Client
	releaseHTTPClient *http.Client

	org organizationsAPI
	r53 route53API
	sts stsAPI
	sqs sqsAPI
	cb  codebuildAPI
	s3  s3API

	smFactory  secretsManagerClientFactory
	iamFactory iamClientFactory
}

// NewServer constructs a Server with AWS service clients and a store.
func NewServer(cfg config.Config, st *store.Store, org organizationsAPI, r53 route53API, stsClient stsAPI, sqsClient sqsAPI, cbClient codebuildAPI, s3Client s3API) *Server {
	return &Server{
		cfg:        cfg,
		store:      st,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		org:        org,
		r53:        r53,
		sts:        stsClient,
		sqs:        sqsClient,
		cb:         cbClient,
		s3:         s3Client,
	}
}

// Register registers SQS handlers with the provided app.
func (s *Server) Register(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	queueName := sqsQueueNameFromURL(s.cfg.ProvisionQueueURL)
	if queueName != "" {
		app.SQS(queueName, s.handleProvisionQueueMessage)
	}

	ruleName := fmt.Sprintf("%s-%s-update-sweep", s.cfg.AppName, s.cfg.Stage)
	app.EventBridge(apptheory.EventBridgeRule(ruleName), s.handleUpdateSweep)
}

func (s *Server) handleProvisionQueueMessage(ctx *apptheory.EventContext, msg events.SQSMessage) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}
	if ctx == nil {
		return fmt.Errorf("event context is nil")
	}

	var jm provisioning.JobMessage
	if err := json.Unmarshal([]byte(msg.Body), &jm); err != nil {
		return nil // drop invalid
	}
	jobID := strings.TrimSpace(jm.JobID)
	if jobID == "" {
		return nil
	}

	switch strings.TrimSpace(jm.Kind) {
	case "provision_job":
		return s.processProvisionJob(ctx.Context(), ctx.RequestID, jobID)
	case "update_job":
		return s.processUpdateJob(ctx.Context(), ctx.RequestID, jobID)
	default:
		return nil
	}
}

func (s *Server) handleUpdateSweep(ctx *apptheory.EventContext, _ events.EventBridgeEvent) (any, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("event context is nil")
	}
	now := time.Now().UTC()
	updateResult, updateErr := s.processActiveUpdateSweep(ctx.Context(), ctx.RequestID, now)
	if updateResult == nil {
		updateResult = map[string]any{}
	}

	provisionResult, provisionErr := s.processActiveProvisionSweep(ctx.Context(), ctx.RequestID, now)
	updateResult["provisioning"] = provisionResult

	if updateErr != nil && provisionErr != nil {
		return updateResult, fmt.Errorf("update sweep failed: %v; provisioning sweep failed: %w", updateErr, provisionErr)
	}
	if updateErr != nil {
		return updateResult, updateErr
	}
	if provisionErr != nil {
		return updateResult, provisionErr
	}
	return updateResult, nil
}

func (s *Server) processActiveProvisionSweep(ctx context.Context, requestID string, now time.Time) (map[string]any, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if s.sqs == nil {
		return map[string]any{
			"active_jobs": 0,
			"processed":   0,
			"errors":      0,
			"skipped":     "sqs not initialized",
			"swept_at":    now.UTC().Format(time.RFC3339),
		}, nil
	}

	items, err := s.listProvisionSweepJobs(ctx)
	if err != nil {
		return nil, err
	}

	counts := provisionSweepCounts{}
	for _, item := range items {
		if !isProvisionJobStorageRow(item) || !provisionJobProcessable(item) {
			continue
		}
		counts.activeJobs++
		processed, err := s.processProvisionSweepJob(ctx, item, requestID, now)
		counts.record(processed, err)
	}

	result := counts.result(now)
	if counts.firstErr != nil {
		return result, fmt.Errorf("provisioning sweep encountered %d errors: %w", counts.errorCount, counts.firstErr)
	}
	return result, nil
}

type provisionSweepCounts struct {
	activeJobs int
	processed  int
	errorCount int
	firstErr   error
}

func (c *provisionSweepCounts) record(processed bool, err error) {
	if err != nil {
		c.errorCount++
		if c.firstErr == nil {
			c.firstErr = err
		}
		return
	}
	if processed {
		c.processed++
	}
}

func (c provisionSweepCounts) result(now time.Time) map[string]any {
	return map[string]any{
		"active_jobs": c.activeJobs,
		"processed":   c.processed,
		"errors":      c.errorCount,
		"swept_at":    now.UTC().Format(time.RFC3339),
	}
}

func (s *Server) listProvisionSweepJobs(ctx context.Context) ([]*models.ProvisionJob, error) {
	var items []*models.ProvisionJob
	err := s.store.DB.WithContext(ctx).
		Model(&models.ProvisionJob{}).
		Where("SK", "=", models.SKJob).
		Limit(provisionSweepLimit).
		All(&items)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, err
	}
	return items, nil
}

func (s *Server) processProvisionSweepJob(ctx context.Context, item *models.ProvisionJob, requestID string, now time.Time) (bool, error) {
	if !isProvisionJobStorageRow(item) {
		return false, nil
	}
	if provisionJobConsentExpired(item, now) {
		clearProvisionJobConsentArtifacts(item)
		return true, s.failJob(ctx, item, requestID, now, "provision_consent_expired", "provisioning consent expired before deploy runner start")
	}
	if !item.ExpiresAt.IsZero() && item.ExpiresAt.Before(now) {
		return true, s.failJob(ctx, item, requestID, now, "expired", "provisioning job has expired")
	}
	if item.HasActiveLease(now) || !provisionJobStaleForSweep(item, now) {
		return false, nil
	}
	return true, s.requeueProvisionJob(ctx, strings.TrimSpace(item.ID), 0)
}

func isProvisionJobStorageRow(item *models.ProvisionJob) bool {
	if item == nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(item.PK), "PROVISION_JOB#") && strings.TrimSpace(item.SK) == models.SKJob
}

func provisionJobHasConsentArtifacts(job *models.ProvisionJob) bool {
	if job == nil {
		return false
	}
	return strings.TrimSpace(job.ConsentMessage) != "" ||
		strings.TrimSpace(job.ConsentSignature) != "" ||
		strings.TrimSpace(job.ConsentEncrypted) != ""
}

func provisionJobConsentExpired(job *models.ProvisionJob, now time.Time) bool {
	if !provisionJobHasConsentArtifacts(job) || job.ConsentExpiresAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !now.Before(job.ConsentExpiresAt)
}

func provisionJobStaleForSweep(item *models.ProvisionJob, now time.Time) bool {
	if item == nil {
		return false
	}
	lastSeen := item.UpdatedAt
	if lastSeen.IsZero() {
		lastSeen = item.CreatedAt
	}
	return lastSeen.IsZero() || now.Sub(lastSeen) >= provisionSweepStaleAfter
}

func (s *Server) processProvisionJob(ctx context.Context, requestID string, jobID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}

	job, err := s.loadProvisionJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job == nil || !provisionJobProcessable(job) {
		return nil
	}

	now := time.Now().UTC()

	if !s.cfg.ManagedProvisioningEnabled {
		return s.failJob(ctx, job, requestID, now, "disabled", "managed provisioning is disabled (set MANAGED_PROVISIONING_ENABLED=true)")
	}

	if missing := s.missingManagedProvisioningConfig(job); len(missing) > 0 {
		return s.failJob(ctx, job, requestID, now, "missing_config", "missing required config: "+strings.Join(missing, ", "))
	}

	leased, err := s.tryAcquireProvisionJobLease(ctx, job, requestID, now)
	if err != nil {
		return err
	}
	if !leased {
		return nil
	}

	return s.runManagedProvisioningStateMachine(ctx, job, requestID, now)
}

func provisionJobLeaseOwner(requestID string, jobID string) string {
	owner := strings.TrimSpace(requestID)
	if owner == "" {
		owner = "worker"
	}
	jobID = strings.TrimSpace(jobID)
	if jobID != "" {
		owner += ":" + jobID
	}
	if len(owner) > 128 {
		return owner[:128]
	}
	return owner
}

func (s *Server) tryAcquireProvisionJobLease(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return false, fmt.Errorf("store not initialized")
	}
	if job == nil {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	owner := provisionJobLeaseOwner(requestID, job.ID)
	expiresAt := now.Add(provisionJobLeaseDuration)
	jobExpiresAt := job.ExpiresAt
	if jobExpiresAt.IsZero() {
		jobExpiresAt = now.Add(30 * 24 * time.Hour)
	}
	update := &models.ProvisionJob{
		ID:             strings.TrimSpace(job.ID),
		InstanceSlug:   strings.TrimSpace(job.InstanceSlug),
		CreatedAt:      job.CreatedAt,
		ExpiresAt:      jobExpiresAt,
		LeaseOwner:     owner,
		LeaseExpiresAt: expiresAt,
		RequestID:      strings.TrimSpace(requestID),
		UpdatedAt:      now,
	}
	_ = update.UpdateKeys()

	err := s.store.DB.WithContext(ctx).
		Model(update).
		IfExists().
		WithConditionExpression(
			"attribute_not_exists(leaseExpiresAt) OR leaseExpiresAt <= :now OR leaseOwner = :owner OR attribute_not_exists(leaseOwner) OR leaseOwner = :empty",
			map[string]any{":now": now, ":owner": owner, ":empty": ""},
		).
		Update("LeaseOwner", "LeaseExpiresAt", "RequestID", "UpdatedAt")
	if theoryErrors.IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	job.LeaseOwner = owner
	job.LeaseExpiresAt = expiresAt
	if job.ExpiresAt.IsZero() {
		job.ExpiresAt = jobExpiresAt
	}
	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	return true, nil
}

func (s *Server) loadProvisionJob(ctx context.Context, jobID string) (*models.ProvisionJob, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	job, err := s.store.GetProvisionJob(ctx, strings.TrimSpace(jobID))
	if theoryErrors.IsNotFound(err) || job == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Server) loadInstance(ctx context.Context, slug string) (*models.Instance, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	slug = strings.TrimSpace(strings.ToLower(slug))
	if slug == "" {
		return nil, fmt.Errorf("instance slug is required")
	}

	var inst models.Instance
	err := s.store.DB.WithContext(ctx).
		Model(&models.Instance{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", slug)).
		Where("SK", "=", models.SKMetadata).
		ConsistentRead().
		First(&inst)
	if theoryErrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func provisionJobProcessable(job *models.ProvisionJob) bool {
	if job == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(job.Status))
	return status == models.ProvisionJobStatusQueued || status == models.ProvisionJobStatusRunning
}

func (s *Server) missingManagedProvisioningConfig(job *models.ProvisionJob) []string {
	if s == nil || job == nil {
		return nil
	}

	var missing []string
	if strings.TrimSpace(s.cfg.ManagedParentHostedZoneID) == "" {
		missing = append(missing, "MANAGED_PARENT_HOSTED_ZONE_ID")
	}
	if strings.TrimSpace(s.cfg.ManagedAccountEmailTemplate) == "" &&
		strings.TrimSpace(job.AccountID) == "" &&
		strings.TrimSpace(job.AccountRequestID) == "" {
		missing = append(missing, "MANAGED_ACCOUNT_EMAIL_TEMPLATE")
	}
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

func (s *Server) failJob(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, code string, msg string) error {
	if job == nil {
		return nil
	}

	job.Status = models.ProvisionJobStatusError
	job.Step = "failed"
	job.ErrorCode = strings.TrimSpace(code)
	job.ErrorMessage = strings.TrimSpace(msg)
	job.Note = job.ErrorMessage
	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	clearProvisionJobConsentArtifacts(job)
	_ = job.UpdateKeys()

	updateInst := &models.Instance{Slug: strings.TrimSpace(job.InstanceSlug)}
	_ = updateInst.UpdateKeys()

	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Put(job)
		tx.UpdateWithBuilder(updateInst, func(ub core.UpdateBuilder) error {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusError)
			ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
			return nil
		}, tabletheory.IfExists())
		return nil
	})
}

const (
	provisionStepQueued             = "queued"
	provisionStepAccountCreate      = "account.create"
	provisionStepAccountCreatePoll  = "account.create.poll"
	provisionStepAccountMove        = "account.move"
	provisionStepAssumeRole         = "account.assumeRole"
	provisionStepChildZone          = "dns.childZone"
	provisionStepParentDelegation   = "dns.parentDelegation"
	provisionStepInstanceConfig     = "instance.config"
	provisionStepDeployStart        = "deploy.start"
	provisionStepDeployWait         = "deploy.wait"
	provisionStepReceiptIngest      = "receipt.ingest"
	provisionStepBodyDeployStart    = "body.deploy.start"
	provisionStepBodyDeployWait     = "body.deploy.wait"
	provisionStepDeployMcpStart     = "deploy.mcp.start"
	provisionStepDeployMcpWait      = "deploy.mcp.wait" // #nosec G101 -- step identifier, not a credential
	provisionStepDone               = "done"
	provisionStepFailed             = "failed"
	provisionMaxTransitionsPerRun   = 6
	provisionMaxAccountCreateAge    = 90 * time.Minute
	provisionMaxAssumeRoleAge       = 30 * time.Minute
	provisionMaxDeployAge           = 3 * time.Hour
	provisionDefaultPollDelay       = 45 * time.Second
	provisionDefaultShortRetryDelay = 20 * time.Second
	provisionJobLeaseDuration       = 10 * time.Second
	provisionSweepLimit             = 200
	provisionSweepStaleAfter        = 2 * time.Minute
	defaultManagedAWSRegion         = "us-east-1"

	noteMissingAccountIDRestart = "missing account id; restarting account allocation"

	codebuildStatusSucceeded  = "SUCCEEDED"
	codebuildStatusInProgress = "IN_PROGRESS"
	codebuildStatusFailed     = "FAILED"
	codebuildStatusFault      = "FAULT"
	codebuildStatusStopped    = "STOPPED"
	codebuildStatusTimedOut   = "TIMED_OUT"
	codebuildStatusUnknown    = "UNKNOWN"
)

func (s *Server) runManagedProvisioningStateMachine(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return nil
	}

	if !job.ExpiresAt.IsZero() && job.ExpiresAt.Before(now) {
		return s.failJob(ctx, job, requestID, now, "expired", "provisioning job has expired")
	}

	s.initializeManagedProvisionJob(job)
	if err := s.startManagedProvisioningJobIfQueued(ctx, job, requestID, now); err != nil {
		return err
	}
	return s.advanceManagedProvisioningLoop(ctx, job, requestID, now)
}

func (s *Server) initializeManagedProvisionJob(job *models.ProvisionJob) {
	if s == nil || job == nil {
		return
	}

	if strings.TrimSpace(job.Step) == "" {
		job.Step = provisionStepQueued
	}
	if strings.TrimSpace(job.Region) == "" {
		job.Region = strings.TrimSpace(s.cfg.ManagedDefaultRegion)
	}
	if strings.TrimSpace(job.Stage) == "" {
		job.Stage = normalizeManagedLesserStage(s.cfg.Stage)
	} else {
		job.Stage = normalizeManagedLesserStage(job.Stage)
	}
	if strings.TrimSpace(job.ParentHostedZoneID) == "" {
		job.ParentHostedZoneID = strings.TrimSpace(s.cfg.ManagedParentHostedZoneID)
	}
	if strings.TrimSpace(job.AccountRoleName) == "" {
		job.AccountRoleName = strings.TrimSpace(s.cfg.ManagedInstanceRoleName)
		if strings.TrimSpace(job.AccountRoleName) == "" {
			job.AccountRoleName = defaultManagedInstanceRoleName
		}
	}
	if strings.TrimSpace(job.BaseDomain) == "" {
		job.BaseDomain = managedBaseDomain(strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(s.cfg.ManagedParentDomain))
	}
}

func managedBaseDomain(slug string, parentDomain string) string {
	slug = strings.TrimSpace(slug)
	parentDomain = strings.TrimSpace(parentDomain)
	if parentDomain == "" {
		parentDomain = "greater.website"
	}
	return fmt.Sprintf("%s.%s", slug, strings.TrimPrefix(parentDomain, "."))
}

func (s *Server) publicBaseURL() string {
	if s == nil {
		return ""
	}

	rootDomain := strings.TrimSpace(s.cfg.WebAuthnRPID)
	if rootDomain == "" {
		rootDomain = "lesser.host"
	}

	stage := strings.ToLower(strings.TrimSpace(s.cfg.Stage))
	if stage == "" {
		stage = defaultControlPlaneStage
	}

	switch stage {
	case managedStageLive, managedStageLiveProdAlias, managedStageLiveLongAlias:
		return "https://" + rootDomain
	default:
		return "https://" + stage + "." + rootDomain
	}
}

func (s *Server) startManagedProvisioningJobIfQueued(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) error {
	if s == nil || job == nil {
		return nil
	}

	status := strings.ToLower(strings.TrimSpace(job.Status))
	if status != models.ProvisionJobStatusQueued {
		return nil
	}

	job.Status = models.ProvisionJobStatusRunning
	job.Note = "starting provisioning"
	job.Step = provisionStepQueued
	return s.persistJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
		ub.Set("ProvisionStatus", models.ProvisionJobStatusRunning)
		ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
		if strings.TrimSpace(job.BaseDomain) != "" {
			ub.Set("HostedBaseDomain", strings.TrimSpace(job.BaseDomain))
		}
		if strings.TrimSpace(job.Region) != "" {
			ub.Set("HostedRegion", strings.TrimSpace(job.Region))
		}
		return nil
	})
}

func (s *Server) advanceManagedProvisioningLoop(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) error {
	if s == nil || job == nil {
		return nil
	}

	for i := 0; i < provisionMaxTransitionsPerRun; i++ {
		requeueDelay, done, err := s.advanceManagedProvisioning(ctx, job, requestID, now)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if requeueDelay > 0 {
			return s.requeueProvisionJob(ctx, strings.TrimSpace(job.ID), requeueDelay)
		}
		// Continue immediately (advanced to next step synchronously).
	}

	// Safety: if we progressed quickly through multiple steps, requeue to continue.
	return s.requeueProvisionJob(ctx, strings.TrimSpace(job.ID), provisionDefaultShortRetryDelay)
}

type managedProvisionStepHandler func(*Server, context.Context, *models.ProvisionJob, string, time.Time) (time.Duration, bool, error)

var managedProvisionStepHandlers = map[string]managedProvisionStepHandler{
	provisionStepQueued:            (*Server).advanceProvisionQueued,
	provisionStepAccountCreate:     (*Server).advanceProvisionAccountCreate,
	provisionStepAccountCreatePoll: (*Server).advanceProvisionAccountCreatePoll,
	provisionStepAccountMove:       (*Server).advanceProvisionAccountMove,
	provisionStepAssumeRole:        (*Server).advanceProvisionAssumeRole,
	provisionStepChildZone:         (*Server).advanceProvisionChildZone,
	provisionStepParentDelegation:  (*Server).advanceProvisionParentDelegation,
	provisionStepInstanceConfig:    (*Server).advanceProvisionInstanceConfig,
	provisionStepDeployStart:       (*Server).advanceProvisionDeployStart,
	provisionStepDeployWait:        (*Server).advanceProvisionDeployWait,
	provisionStepReceiptIngest:     (*Server).advanceProvisionReceiptIngest,
	provisionStepBodyDeployStart:   (*Server).advanceProvisionBodyDeployStart,
	provisionStepBodyDeployWait:    (*Server).advanceProvisionBodyDeployWait,
	provisionStepDeployMcpStart:    (*Server).advanceProvisionDeployMcpStart,
	provisionStepDeployMcpWait:     (*Server).advanceProvisionDeployMcpWait,
}

func (s *Server) advanceManagedProvisioning(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if s == nil || job == nil {
		return 0, true, nil
	}

	step := strings.TrimSpace(job.Step)
	if step == provisionStepDone || step == provisionStepFailed {
		return 0, true, nil
	}

	handler, ok := managedProvisionStepHandlers[step]
	if !ok {
		return 0, false, s.failJob(ctx, job, requestID, now, "unknown_step", "unknown provisioning step: "+step)
	}
	return handler(s, ctx, job, requestID, now)
}

func (s *Server) advanceProvisionQueued(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	job.Step = provisionStepAccountCreate
	job.Note = "allocating AWS account"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceProvisionAccountCreate(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if strings.TrimSpace(job.AccountID) != "" {
		// CSR-012: Validate adopted account before transitioning to account.move.
		// An adopted account (AccountID set but no AccountRequestID) must be
		// verified against Organizations expectations before any move or
		// assume-role side effects occur. Failing early at account.create
		// prevents the state machine from proceeding through OU move and
		// assume-role steps on an invalid adopted account.
		if strings.TrimSpace(job.AccountRequestID) == "" {
			if delay, done, err := s.validateAdoptedProvisionAccount(ctx, job, requestID, now); err != nil || done || delay > 0 {
				return delay, done, err
			}
			if jobFailed := strings.ToLower(strings.TrimSpace(job.Status)) == models.ProvisionJobStatusError; jobFailed {
				return 0, true, nil
			}
		}
		return s.advanceToAccountMove(ctx, job, requestID, now, "AWS account allocated")
	}
	if strings.TrimSpace(job.AccountRequestID) == "" {
		return s.startProvisionAccountCreate(ctx, job, requestID, now)
	}
	return s.advanceToAccountCreatePoll(ctx, job, requestID, now, "waiting for AWS account creation")
}

func ensureProvisionAccountEmail(job *models.ProvisionJob, tmpl string) string {
	if job == nil {
		return ""
	}
	email := strings.TrimSpace(job.AccountEmail)
	if email == "" {
		email = strings.TrimSpace(expandManagedAccountEmailTemplate(tmpl, job.InstanceSlug))
		job.AccountEmail = email
	}
	return email
}

func managedAccountName(prefix, slug string) string {
	name := strings.TrimSpace(strings.TrimSpace(prefix) + strings.TrimSpace(slug))
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

func managedAccountRoleName(jobRole string, defaultRole string) string {
	roleName := strings.TrimSpace(jobRole)
	if roleName == "" {
		roleName = strings.TrimSpace(defaultRole)
	}
	return roleName
}

func (s *Server) startProvisionAccountCreate(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	email := ensureProvisionAccountEmail(job, s.cfg.ManagedAccountEmailTemplate)
	accountName := managedAccountName(s.cfg.ManagedAccountNamePrefix, job.InstanceSlug)
	roleName := managedAccountRoleName(job.AccountRoleName, s.cfg.ManagedInstanceRoleName)

	return s.requestAccountCreate(ctx, job, requestID, now, email, accountName, roleName)
}

func (s *Server) requestAccountCreate(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, email string, accountName string, roleName string) (time.Duration, bool, error) {
	out, err := s.org.CreateAccount(ctx, &organizations.CreateAccountInput{
		AccountName:            aws.String(accountName),
		Email:                  aws.String(email),
		RoleName:               aws.String(roleName),
		IamUserAccessToBilling: orgtypes.IAMUserAccessToBillingAllow,
	})
	if err != nil {
		if isOrgAccessDenied(err) {
			return 0, false, s.failOrgPermissions(ctx, job, requestID, now, "CreateAccount", err)
		}
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "create_account_failed", "organizations CreateAccount failed: "+err.Error())
		}
		job.Note = "retrying account allocation"
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 5*time.Minute), false, nil
	}

	reqID := ""
	if out != nil && out.CreateAccountStatus != nil && out.CreateAccountStatus.Id != nil {
		reqID = strings.TrimSpace(*out.CreateAccountStatus.Id)
	}
	if reqID == "" {
		return 0, false, s.failJob(ctx, job, requestID, now, "create_account_failed", "organizations CreateAccount returned empty request id")
	}

	job.AccountRequestID = reqID
	return s.advanceToAccountCreatePoll(ctx, job, requestID, now, "waiting for AWS account creation")
}

func (s *Server) advanceToAccountMove(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, note string) (time.Duration, bool, error) {
	job.Step = provisionStepAccountMove
	job.Note = strings.TrimSpace(note)
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceToAccountCreatePoll(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, note string) (time.Duration, bool, error) {
	job.Step = provisionStepAccountCreatePoll
	job.Note = strings.TrimSpace(note)
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceProvisionAccountCreatePoll(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if strings.TrimSpace(job.AccountID) != "" {
		return s.advanceToAccountMove(ctx, job, requestID, now, "AWS account ready")
	}
	if strings.TrimSpace(job.AccountRequestID) == "" {
		return s.restartProvisionAccountCreate(ctx, job, requestID, now, "missing account request id; restarting account allocation")
	}

	if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxAccountCreateAge {
		return 0, false, s.failJob(ctx, job, requestID, now, "account_create_timeout", "AWS account creation timed out; check Organizations CreateAccountStatus")
	}

	out, err := s.org.DescribeCreateAccountStatus(ctx, &organizations.DescribeCreateAccountStatusInput{
		CreateAccountRequestId: aws.String(strings.TrimSpace(job.AccountRequestID)),
	})
	if err != nil {
		if isOrgAccessDenied(err) {
			return 0, false, s.failOrgPermissions(ctx, job, requestID, now, "DescribeCreateAccountStatus", err)
		}
		return s.retryProvisionJobOrFail(ctx, job, requestID, now, "describe_account_failed", "organizations DescribeCreateAccountStatus failed: "+err.Error(), provisionDefaultPollDelay, 10*time.Minute)
	}
	if out == nil || out.CreateAccountStatus == nil || out.CreateAccountStatus.State == "" {
		return provisionDefaultPollDelay, false, nil
	}

	return s.handleProvisionAccountCreateStatus(ctx, job, requestID, now, out.CreateAccountStatus)
}

func (s *Server) restartProvisionAccountCreate(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, note string) (time.Duration, bool, error) {
	job.Step = provisionStepAccountCreate
	job.Note = strings.TrimSpace(note)
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) retryProvisionJobOrFail(
	ctx context.Context,
	job *models.ProvisionJob,
	requestID string,
	now time.Time,
	code string,
	msg string,
	baseDelay time.Duration,
	maxDelay time.Duration,
) (time.Duration, bool, error) {
	job.Attempts++
	if job.Attempts >= job.MaxAttempts {
		return 0, false, s.failJob(ctx, job, requestID, now, strings.TrimSpace(code), strings.TrimSpace(msg))
	}
	_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
	return jitteredBackoff(job.Attempts, baseDelay, maxDelay), false, nil
}

func isOrgAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(apiErr.ErrorCode()))
	return code == "accessdeniedexception" || code == "accessdenied"
}

func (s *Server) failOrgPermissions(
	ctx context.Context,
	job *models.ProvisionJob,
	requestID string,
	now time.Time,
	action string,
	err error,
) error {
	msg := fmt.Sprintf("organizations %s access denied: %s", strings.TrimSpace(action), compactErr(err))
	return s.failJob(ctx, job, requestID, now, "org_permissions_missing", msg)
}

func (s *Server) handleProvisionAccountCreateStatus(
	ctx context.Context,
	job *models.ProvisionJob,
	requestID string,
	now time.Time,
	st *orgtypes.CreateAccountStatus,
) (time.Duration, bool, error) {
	if st == nil || st.State == "" {
		return provisionDefaultPollDelay, false, nil
	}

	switch st.State {
	case orgtypes.CreateAccountStateSucceeded:
		accID := strings.TrimSpace(aws.ToString(st.AccountId))
		if accID == "" {
			return 0, false, s.failJob(ctx, job, requestID, now, "account_create_failed", "Organizations CreateAccount SUCCEEDED but AccountId is empty")
		}

		job.AccountID = accID
		job.Note = "AWS account created"
		return s.advanceToAccountMove(ctx, job, requestID, now, job.Note)

	case orgtypes.CreateAccountStateInProgress:
		job.Note = "AWS account creation in progress"
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil

	case orgtypes.CreateAccountStateFailed:
		if st.FailureReason == orgtypes.CreateAccountFailureReasonEmailAlreadyExists {
			if delay, done, err, handled := s.handleAccountCreateEmailExists(ctx, job, requestID, now); handled {
				return delay, done, err
			}
		}
		reason := "unknown"
		if st.FailureReason != "" {
			reason = string(st.FailureReason)
		}
		return 0, false, s.failJob(ctx, job, requestID, now, "account_create_failed", "AWS account creation failed: "+reason)

	default:
		job.Note = "AWS account creation state: " + string(st.State)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil
	}
}

func (s *Server) handleAccountCreateEmailExists(
	ctx context.Context,
	job *models.ProvisionJob,
	requestID string,
	now time.Time,
) (time.Duration, bool, error, bool) {
	email := ensureProvisionAccountEmail(job, s.cfg.ManagedAccountEmailTemplate)
	msg := "AWS Organizations reports the managed account email already exists; explicit validated account adoption is required"
	if strings.TrimSpace(email) != "" {
		msg += " for " + strings.TrimSpace(email)
	}
	return 0, false, s.failJob(ctx, job, requestID, now, "account_email_exists_adoption_required", msg), true
}

func (s *Server) advanceProvisionAccountMove(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if delay, done, err := s.validateAdoptedProvisionAccount(ctx, job, requestID, now); err != nil || done || delay > 0 {
		return delay, done, err
	}
	// CSR-012: validateAdoptedProvisionAccount may fail the job via failJob,
	// which returns (0, false, nil) after persisting the failure. Check the
	// in-memory status to avoid proceeding through OU move and assume-role
	// steps on a job that has already been marked as error.
	if jobFailed := strings.ToLower(strings.TrimSpace(job.Status)) == models.ProvisionJobStatusError; jobFailed {
		return 0, true, nil
	}

	targetOu := strings.TrimSpace(s.cfg.ManagedTargetOrganizationalUnitID)
	if targetOu != "" {
		requeueDelay, done, err := s.moveProvisionAccountToTargetOU(ctx, job, requestID, now, targetOu)
		if err != nil || done || requeueDelay > 0 {
			return requeueDelay, done, err
		}
	}

	return s.advanceToAssumeRole(ctx, job, requestID, now)
}

func (s *Server) moveProvisionAccountToTargetOU(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, targetOu string) (time.Duration, bool, error) {
	accID := strings.TrimSpace(job.AccountID)
	if accID == "" {
		return s.restartProvisionAccountCreate(ctx, job, requestID, now, noteMissingAccountIDRestart)
	}

	parents, err := s.org.ListParents(ctx, &organizations.ListParentsInput{ChildId: aws.String(accID)})
	if err != nil {
		if isOrgAccessDenied(err) {
			return 0, false, s.failOrgPermissions(ctx, job, requestID, now, "ListParents", err)
		}
		return s.retryProvisionJobOrFail(ctx, job, requestID, now, "list_parents_failed", "organizations ListParents failed: "+err.Error(), provisionDefaultShortRetryDelay, 5*time.Minute)
	}

	sourceParent := ""
	if parents != nil && len(parents.Parents) > 0 {
		sourceParent = strings.TrimSpace(aws.ToString(parents.Parents[0].Id))
	}
	if sourceParent == "" || sourceParent == targetOu {
		return 0, false, nil
	}

	_, err = s.org.MoveAccount(ctx, &organizations.MoveAccountInput{
		AccountId:           aws.String(accID),
		SourceParentId:      aws.String(sourceParent),
		DestinationParentId: aws.String(targetOu),
	})
	if err != nil {
		if isOrgAccessDenied(err) {
			return 0, false, s.failOrgPermissions(ctx, job, requestID, now, "MoveAccount", err)
		}
		job.Note = "retrying OU move"
		return s.retryProvisionJobOrFail(ctx, job, requestID, now, "move_account_failed", "organizations MoveAccount failed: "+err.Error(), provisionDefaultShortRetryDelay, 10*time.Minute)
	}

	job.Note = "moved account to OU"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceToAssumeRole(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	job.Step = provisionStepAssumeRole
	job.Note = "assuming provisioning role into instance account"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
		ub.Set("HostedAccountID", strings.TrimSpace(job.AccountID))
		return nil
	}); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceProvisionAssumeRole(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	accID := strings.TrimSpace(job.AccountID)
	if accID == "" {
		job.Step = provisionStepAccountCreate
		job.Note = noteMissingAccountIDRestart
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}

	if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxAssumeRoleAge+provisionMaxAccountCreateAge {
		return 0, false, s.failJob(ctx, job, requestID, now, "assume_role_timeout", "timed out waiting for instance role to become assumable")
	}

	_, retryAfter, err := s.assumeInstanceRole(ctx, accID, strings.TrimSpace(job.AccountRoleName), strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
	if err != nil {
		if isBoundedAssumeRoleReadinessErr(err) {
			job.Note = "waiting for role to become assumable"
			_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
			if retryAfter <= 0 {
				retryAfter = provisionDefaultPollDelay
			}
			return retryAfter, false, nil
		}
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "assume_role_failed", "sts AssumeRole failed: "+err.Error())
		}
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 5*time.Minute), false, nil
	}

	job.Step = provisionStepChildZone
	job.Note = "creating delegated hosted zone"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceProvisionChildZone(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	accID := strings.TrimSpace(job.AccountID)
	if accID == "" {
		job.Step = provisionStepAccountCreate
		job.Note = noteMissingAccountIDRestart
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}

	childZoneID, nameServers, err := s.ensureChildHostedZone(ctx, accID, strings.TrimSpace(job.AccountRoleName), strings.TrimSpace(job.BaseDomain), strings.TrimSpace(job.ChildHostedZoneID), job.ChildNameServers, strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "child_zone_failed", "failed to ensure child hosted zone: "+err.Error())
		}
		job.Note = "failed to ensure child hosted zone; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 10*time.Minute), false, nil
	}

	job.ChildHostedZoneID = strings.TrimSpace(childZoneID)
	job.ChildNameServers = append([]string(nil), nameServers...)
	job.Step = provisionStepParentDelegation
	job.Note = "delegating DNS from parent zone"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
		if strings.TrimSpace(job.ChildHostedZoneID) != "" {
			ub.Set("HostedZoneID", strings.TrimSpace(job.ChildHostedZoneID))
		}
		return nil
	}); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceProvisionParentDelegation(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if strings.TrimSpace(job.ParentHostedZoneID) == "" {
		return 0, false, s.failJob(ctx, job, requestID, now, "missing_parent_zone", "parent hosted zone id is missing")
	}
	if len(job.ChildNameServers) == 0 {
		job.Step = provisionStepChildZone
		job.Note = "missing child zone name servers; reloading child hosted zone"
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}

	if err := s.upsertParentNSDelegation(ctx, strings.TrimSpace(job.ParentHostedZoneID), strings.TrimSpace(job.BaseDomain), job.ChildNameServers); err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "parent_delegation_failed", "failed to upsert parent NS delegation: "+err.Error())
		}
		job.Note = "failed to upsert parent NS delegation; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 10*time.Minute), false, nil
	}

	job.Step = provisionStepInstanceConfig
	job.Note = noteEnsuringInstanceConfiguration
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceProvisionInstanceConfig(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if err := s.requireStoreDB(); err != nil {
		return 0, false, err
	}
	if job == nil {
		return 0, true, nil
	}

	accID := strings.TrimSpace(job.AccountID)
	if accID == "" {
		job.Step = provisionStepAccountCreate
		job.Note = noteMissingAccountIDRestart
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}

	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return s.retryProvisionJobOrFail(ctx, job, requestID, now, "instance_load_failed", "failed to load instance: "+err.Error(), provisionDefaultShortRetryDelay, 2*time.Minute)
	}
	if inst == nil {
		return 0, false, s.failJob(ctx, job, requestID, now, "instance_not_found", "instance record not found")
	}

	publicBaseURL := strings.TrimSpace(s.publicBaseURL())
	attestationsURL := strings.TrimSpace(publicBaseURL)
	translationEnabled := provisionTranslationEnabled(inst)
	// Initial provisioning must not assume directly into the target account to
	// create or read the InstanceKey secret. The deploy runner assumes the target
	// role with the tenant-scoped ExternalId, derives the canonical secret from
	// APP_SLUG/STAGE when no ARN is already recorded, and returns a bounded
	// managed_instance_key proof in its receipt.
	secretArn := strings.TrimSpace(inst.LesserHostInstanceKeySecretARN)

	job.Step = provisionStepDeployStart
	job.Note = "starting instance deploy runner"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, provisionInstanceConfigInstanceUpdate(job, inst, publicBaseURL, attestationsURL, secretArn, translationEnabled)); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func effectiveTranslationEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func effectiveBodyEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveTipEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func effectiveLesserAIEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLesserAIModerationEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLesserAINsfwDetectionEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLesserAISpamDetectionEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLesserAIPiiDetectionEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func effectiveLesserAIContentDetectionEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

const (
	managedInstanceKeySecretTagInstanceSlug = "lesser-host:instance-slug"   // #nosec G101 -- tag key, not a credential
	managedInstanceKeySecretTagKeyID        = "lesser-host:instance-key-id" // #nosec G101 -- tag key, not a credential
	managedInstanceKeySecretTagManaged      = "lesser-host:managed"
	managedInstanceKeySecretTagStage        = "lesser-host:control-plane-stage"
)

func managedInstanceKeySecretName(controlPlaneStage, slug string) string {
	stage := managedInstanceKeySecretStage(controlPlaneStage)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/instance-key", stage, slug)
}

func managedInstanceKeySecretStage(controlPlaneStage string) string {
	stage := strings.ToLower(strings.TrimSpace(controlPlaneStage))
	switch stage {
	case managedStageLiveProdAlias, managedStageLiveLongAlias:
		stage = managedStageLive
	}
	if stage == "" {
		stage = defaultControlPlaneStage
	}
	var b strings.Builder
	for _, r := range stage {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return defaultControlPlaneStage
	}
	return out
}

func secretsManagerTagValue(tags []smtypes.Tag, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	for _, t := range tags {
		if strings.TrimSpace(aws.ToString(t.Key)) == key {
			return strings.TrimSpace(aws.ToString(t.Value))
		}
	}
	return ""
}

func isSecretsManagerNotFound(err error) bool {
	if err == nil {
		return false
	}
	var nf *smtypes.ResourceNotFoundException
	return errors.As(err, &nf)
}

func isSecretsManagerExists(err error) bool {
	if err == nil {
		return false
	}
	var exists *smtypes.ResourceExistsException
	return errors.As(err, &exists)
}

func secretValueToKeyID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func unwrapSecretsManagerSecretString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("secret value is empty")
	}
	if strings.HasPrefix(raw, "{") {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return "", fmt.Errorf("unmarshal secret string: %w", err)
		}
		val := strings.TrimSpace(parsed["secret"])
		if val == "" {
			return "", fmt.Errorf("secret payload missing 'secret' key")
		}
		return val, nil
	}
	return raw, nil
}

func wrapSecretsManagerSecretString(secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", fmt.Errorf("secret value is empty")
	}
	out, err := json.Marshal(map[string]string{"secret": secret})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *Server) ensureInstanceKeyRecord(ctx context.Context, slug, keyID string) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	keyID = strings.TrimSpace(keyID)
	if slug == "" || keyID == "" {
		return fmt.Errorf("slug and keyID are required")
	}

	now := time.Now().UTC()
	key := &models.InstanceKey{
		ID:           keyID,
		InstanceSlug: slug,
		CreatedAt:    now,
	}
	_ = key.UpdateKeys()

	err := s.store.DB.WithContext(ctx).Model(key).IfNotExists().Create()
	if theoryErrors.IsConditionFailed(err) {
		return nil
	}
	return err
}

func (s *Server) ensureManagedInstanceKeySecret(ctx context.Context, job *models.ProvisionJob, inst *models.Instance) (string, error) {
	if err := s.requireStoreDB(); err != nil {
		return "", err
	}
	if job == nil || inst == nil {
		return "", fmt.Errorf("job and instance are required")
	}

	inputs, err := managedInstanceSecretsInputsFromJob(job)
	if err != nil {
		return "", err
	}

	secretName := managedInstanceKeySecretName(s.cfg.Stage, inputs.slug)
	if secretName == "" {
		return "", fmt.Errorf("failed to derive secret name")
	}

	sm, err := s.childSecretsManagerClient(ctx, inputs.accountID, inputs.roleName, inputs.region, inputs.slug, inputs.jobID)
	if err != nil {
		return "", err
	}

	secretID := strings.TrimSpace(inst.LesserHostInstanceKeySecretARN)
	if secretID == "" {
		secretID = secretName
	}

	arn, found, err := s.findManagedInstanceKeySecret(ctx, sm, inputs.slug, secretID, secretName)
	if err != nil {
		return "", err
	}
	if found {
		return arn, nil
	}

	createdArn, keyID, createErr := s.createManagedInstanceKeySecret(ctx, sm, secretName, inputs.slug, s.cfg.Stage)
	if createErr != nil {
		if isSecretsManagerExists(createErr) {
			return s.describeAndEnsureManagedInstanceKeySecret(ctx, sm, inputs.slug, secretName, s.cfg.Stage)
		}
		return "", createErr
	}

	if ensureErr := s.ensureInstanceKeyRecord(ctx, inputs.slug, keyID); ensureErr != nil {
		return "", fmt.Errorf("ensure instance key record: %w", ensureErr)
	}

	createdArn = strings.TrimSpace(createdArn)
	if createdArn != "" {
		return createdArn, nil
	}
	return s.describeAndEnsureManagedInstanceKeySecret(ctx, sm, inputs.slug, secretName, s.cfg.Stage)
}

func (s *Server) findManagedInstanceKeySecret(ctx context.Context, sm secretsManagerAPI, slug string, secretID string, secretName string) (string, bool, error) {
	arn, err := s.describeAndEnsureManagedInstanceKeySecret(ctx, sm, slug, secretID, s.cfg.Stage)
	if err == nil {
		return arn, true, nil
	}
	if isSecretsManagerNotFound(err) {
		return "", false, nil
	}
	if !s.canIgnoreLegacyInstanceKeySecret(secretID, secretName, err) {
		return "", false, err
	}
	arn, err = s.describeAndEnsureManagedInstanceKeySecret(ctx, sm, slug, secretName, s.cfg.Stage)
	if err == nil {
		return arn, true, nil
	}
	if isSecretsManagerNotFound(err) {
		return "", false, nil
	}
	return "", false, err
}

func (s *Server) canIgnoreLegacyInstanceKeySecret(secretID string, secretName string, err error) bool {
	if !isManagedInstanceKeySecretTagError(err) {
		return false
	}
	// Pre-M6 lab managed instances may point at tenant-account secrets that predate the
	// managed/stage/slug tag contract. Do not adopt those secrets by tag-forgery-prone
	// inference; in non-live stages only, ignore the legacy ARN and create/reuse the
	// canonical managed secret instead. Live remains fail-closed.
	if managedInstanceKeySecretStage(s.cfg.Stage) == "live" {
		return false
	}
	return strings.TrimSpace(secretID) != "" && strings.TrimSpace(secretID) != strings.TrimSpace(secretName)
}

// decryptConsentFromJob decrypts the encrypted consent field on the job,
// unpacking the structured JSON payload into ConsentMessage and ConsentSignature.
// Returns an error when the key is missing or decryption fails — the caller
// should fail the job.
func (s *Server) decryptConsentFromJob(job *models.ProvisionJob) error {
	if strings.TrimSpace(job.ConsentEncrypted) == "" {
		return nil
	}
	encKey, encKeyErr := ConsentEncryptionKeyHex(s.cfg.ManagedProvisionConsentEncryptionKeyHex)
	if encKeyErr != nil {
		return fmt.Errorf("consent decryption key: %w", encKeyErr)
	}
	decrypted, decErr := DecryptConsent(job.ConsentEncrypted, encKey)
	if decErr != nil {
		return fmt.Errorf("consent decrypt: %w", decErr)
	}
	// CSR-017: Use structured JSON unpacking instead of newline-delimited
	// splitting so that message/signature separation is unambiguous even
	// when the consent message contains newlines.
	job.ConsentMessage, job.ConsentSignature = UnpackConsent([]byte(decrypted))
	return nil
}

func (s *Server) advanceProvisionDeployStart(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	// CSR-017: Decrypt consent from the encrypted at-rest field before
	// validation and use. Legacy jobs with plaintext ConsentMessage /
	// ConsentSignature (and empty ConsentEncrypted) are handled as-is.
	if decErr := s.decryptConsentFromJob(job); decErr != nil {
		clearProvisionJobConsentArtifacts(job)
		return 0, false, s.failJob(ctx, job, requestID, now, "consent_decrypt_failed", decErr.Error())
	}

	if strings.TrimSpace(job.RunID) != "" {
		job.Step = provisionStepDeployWait
		job.Note = "deploy runner already started"
		clearProvisionJobConsentArtifacts(job)
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return provisionDefaultPollDelay, false, nil
	}

	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return s.retryProvisionJobOrFail(ctx, job, requestID, now, "instance_load_failed", "failed to load instance: "+err.Error(), provisionDefaultShortRetryDelay, 2*time.Minute)
	}
	if inst == nil {
		return 0, false, s.failJob(ctx, job, requestID, now, "instance_not_found", "instance record not found")
	}
	if provisionJobHasConsentArtifacts(job) && job.ConsentExpiresAt.IsZero() {
		clearProvisionJobConsentArtifacts(job)
		return 0, false, s.failJob(ctx, job, requestID, now, "provision_consent_expiration_missing", "provisioning consent expiration is missing")
	}
	if provisionJobConsentExpired(job, now) {
		clearProvisionJobConsentArtifacts(job)
		return 0, false, s.failJob(ctx, job, requestID, now, "provision_consent_expired", "provisioning consent expired before deploy runner start")
	}

	runID, err := s.startDeployRunner(ctx, job)
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "deploy_start_failed", "failed to start deploy runner: "+err.Error())
		}
		job.Note = "failed to start deploy runner; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 10*time.Minute), false, nil
	}

	job.RunID = strings.TrimSpace(runID)
	job.Step = provisionStepDeployWait
	job.Note = noteDeployRunnerInProgress
	clearProvisionJobConsentArtifacts(job)
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func clearProvisionJobConsentArtifacts(job *models.ProvisionJob) {
	if job == nil {
		return
	}
	clearProvisionJobConsentPlaintext(job)
	job.ConsentEncrypted = ""
}

func clearProvisionJobConsentPlaintext(job *models.ProvisionJob) {
	if job == nil {
		return
	}
	if strings.TrimSpace(job.ConsentMessageHash) == "" && job.ConsentMessage != "" {
		sum := sha256.Sum256([]byte(job.ConsentMessage))
		job.ConsentMessageHash = hex.EncodeToString(sum[:])
	}
	job.ConsentMessage = ""
	job.ConsentSignature = ""
}

func (s *Server) advanceProvisionDeployWait(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	status, deepLink, err := s.getDeployRunnerStatus(ctx, strings.TrimSpace(job.RunID))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "deploy_status_failed", "failed to poll deploy runner: "+err.Error())
		}
		job.Note = "failed to poll deploy runner; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultPollDelay, 10*time.Minute), false, nil
	}

	switch status {
	case codebuildStatusSucceeded:
		job.Step = provisionStepReceiptIngest
		job.Note = "ingesting deployment receipt"
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil

	case codebuildStatusInProgress:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failJob(ctx, job, requestID, now, "deploy_timeout", "deploy runner timed out")
		}
		job.Note = noteDeployRunnerInProgress
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil

	case codebuildStatusFailed, codebuildStatusFault, codebuildStatusStopped, codebuildStatusTimedOut:
		msg := "deploy runner failed"
		if deepLink != "" {
			msg = msg + " (CodeBuild: " + deepLink + ")"
		}
		return 0, false, s.failJob(ctx, job, requestID, now, "deploy_failed", msg)

	default:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failJob(ctx, job, requestID, now, "deploy_timeout", "deploy runner timed out")
		}
		job.Note = "deploy runner status: " + status
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil
	}
}

func (s *Server) advanceProvisionReceiptIngest(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	receiptJSON, receipt, err := s.loadProvisionReceipt(ctx, job)
	if err != nil {
		return s.retryProvisionReceiptLoad(ctx, job, requestID, now, err)
	}

	applyLesserUpReceipt(job, receiptJSON, receipt)
	instanceKeySecretARN := ""
	soulBindingSecretARN := ""
	if receipt != nil {
		var keyErr error
		instanceKeySecretARN, keyErr = s.applyProvisionManagedInstanceKeyReceipt(ctx, job, receipt.ManagedInstanceKey)
		if keyErr != nil {
			return 0, false, s.failJob(ctx, job, requestID, now, "receipt_instance_key_invalid", "failed to validate managed instance key receipt: "+keyErr.Error())
		}
		var soulErr error
		soulBindingSecretARN, soulErr = s.applyProvisionSoulBindingIntegrationReceipt(job, receipt.SoulBindingIntegration)
		if soulErr != nil {
			return 0, false, s.failJob(ctx, job, requestID, now, "receipt_soul_binding_invalid", "failed to validate soul binding integration receipt: "+soulErr.Error())
		}
	}

	continueToBody := job.BodyEnabled && job.McpWiredAt.IsZero()

	if continueToBody {
		job.RunID = ""
		if job.BodyProvisionedAt.IsZero() {
			job.Step = provisionStepBodyDeployStart
			job.Note = "starting lesser-body deploy runner"
		} else {
			job.Step = provisionStepDeployMcpStart
			job.Note = noteStartingMcpWiringDeployRunner
		}
	} else {
		job.Step = provisionStepDone
		job.Status = models.ProvisionJobStatusOK
		job.Note = noteProvisioned
	}

	continuing := continueToBody
	if err := s.persistJobAndInstance(ctx, job, requestID, now, provisionReceiptIngestInstanceUpdate(job, continuing, instanceKeySecretARN, soulBindingSecretARN)); err != nil {
		return 0, false, err
	}
	return 0, !continuing, nil
}

func (s *Server) loadProvisionReceipt(ctx context.Context, job *models.ProvisionJob) (string, *lesserUpReceipt, error) {
	receiptKey := s.receiptS3Key(job)
	return s.loadReceiptFromS3(ctx, strings.TrimSpace(s.cfg.ArtifactBucketName), receiptKey)
}

func (s *Server) retryProvisionReceiptLoad(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, err error) (time.Duration, bool, error) {
	job.Attempts++
	if job.Attempts >= job.MaxAttempts {
		return 0, false, s.failJob(ctx, job, requestID, now, "receipt_load_failed", "failed to load receipt: "+err.Error())
	}
	job.Note = "failed to load receipt; retrying: " + compactErr(err)
	_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
	return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 5*time.Minute), false, nil
}

func applyLesserUpReceipt(job *models.ProvisionJob, receiptJSON string, receipt *lesserUpReceipt) {
	if job == nil {
		return
	}

	job.ReceiptJSON = strings.TrimSpace(receiptJSON)
	job.ErrorCode = ""
	job.ErrorMessage = ""

	if receipt == nil {
		return
	}
	if v := strings.TrimSpace(receipt.AccountID); v != "" {
		job.AccountID = v
	}
	if v := strings.TrimSpace(receipt.Region); v != "" {
		job.Region = v
	}
	if v := strings.TrimSpace(receipt.HostedZone.ID); v != "" {
		job.ChildHostedZoneID = normalizeHostedZoneID(v)
	}
}

func provisionReceiptIngestInstanceUpdate(job *models.ProvisionJob, continuing bool, instanceKeySecretARN string, soulBindingSecretARN string) func(core.UpdateBuilder) error {
	return func(ub core.UpdateBuilder) error {
		ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
		if continuing {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusRunning)
		} else {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusOK)
		}
		if strings.TrimSpace(job.AccountID) != "" {
			ub.Set("HostedAccountID", strings.TrimSpace(job.AccountID))
		}
		if strings.TrimSpace(job.Region) != "" {
			ub.Set("HostedRegion", strings.TrimSpace(job.Region))
		}
		if strings.TrimSpace(job.BaseDomain) != "" {
			ub.Set("HostedBaseDomain", strings.TrimSpace(job.BaseDomain))
		}
		if strings.TrimSpace(job.ChildHostedZoneID) != "" {
			ub.Set("HostedZoneID", strings.TrimSpace(job.ChildHostedZoneID))
		}
		if strings.TrimSpace(instanceKeySecretARN) != "" {
			ub.Set("LesserHostInstanceKeySecretARN", strings.TrimSpace(instanceKeySecretARN))
		}
		setSoulBindingIntegrationInstanceARN(ub, soulBindingSecretARN)
		return nil
	}
}

func (s *Server) advanceProvisionBodyDeployStart(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	if !job.BodyEnabled {
		// Skip body + MCP wiring.
		return s.advanceProvisionDone(ctx, job, requestID, now)
	}

	if !job.BodyProvisionedAt.IsZero() {
		return s.advanceProvisionBodyDeployStartBodyAlreadyProvisioned(ctx, job, requestID, now)
	}

	if strings.TrimSpace(job.RunID) != "" {
		return s.advanceProvisionBodyDeployStartRunnerAlreadyStarted(ctx, job, requestID, now)
	}

	return s.advanceProvisionBodyDeployStartStartRunner(ctx, job, requestID, now)
}

func (s *Server) advanceProvisionBodyDeployWait(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	status, deepLink, err := s.getDeployRunnerStatus(ctx, strings.TrimSpace(job.RunID))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "body_deploy_status_failed", "failed to poll lesser-body deploy runner: "+err.Error())
		}
		job.Note = "failed to poll lesser-body deploy runner; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultPollDelay, 10*time.Minute), false, nil
	}

	switch status {
	case codebuildStatusSucceeded:
		job.BodyProvisionedAt = now
		job.Step = provisionStepDeployMcpStart
		job.RunID = ""
		job.Note = noteStartingMcpWiringDeployRunner
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil

	case codebuildStatusInProgress:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failJob(ctx, job, requestID, now, "body_deploy_timeout", "lesser-body deploy runner timed out")
		}
		job.Note = "lesser-body deploy runner in progress"
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil

	case codebuildStatusFailed, codebuildStatusFault, codebuildStatusStopped, codebuildStatusTimedOut:
		msg := "lesser-body deploy runner failed"
		if deepLink != "" {
			msg = msg + " (CodeBuild: " + deepLink + ")"
		}
		return 0, false, s.failJob(ctx, job, requestID, now, "body_deploy_failed", msg)

	default:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failJob(ctx, job, requestID, now, "body_deploy_timeout", "lesser-body deploy runner timed out")
		}
		job.Note = "lesser-body deploy runner status: " + status
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil
	}
}

func (s *Server) advanceProvisionDeployMcpStart(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	if !job.BodyEnabled {
		// Skip MCP wiring.
		return s.advanceProvisionDone(ctx, job, requestID, now)
	}

	if job.BodyProvisionedAt.IsZero() {
		return s.advanceProvisionDeployMcpStartRewindToBody(ctx, job, requestID, now)
	}

	if !job.McpWiredAt.IsZero() {
		return s.advanceProvisionDone(ctx, job, requestID, now)
	}

	if strings.TrimSpace(job.RunID) != "" {
		return s.advanceProvisionDeployMcpStartRunnerAlreadyStarted(ctx, job, requestID, now)
	}

	return s.advanceProvisionDeployMcpStartStartRunner(ctx, job, requestID, now)
}

func (s *Server) advanceProvisionDeployMcpWait(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	status, deepLink, err := s.getDeployRunnerStatus(ctx, strings.TrimSpace(job.RunID))
	if err != nil {
		return s.advanceProvisionDeployMcpWaitRetryPollError(ctx, job, requestID, now, err)
	}

	switch status {
	case codebuildStatusSucceeded:
		job.McpWiredAt = now
		job.RunID = ""
		return s.advanceProvisionDone(ctx, job, requestID, now)

	case codebuildStatusInProgress:
		return s.advanceProvisionDeployMcpWaitInProgress(ctx, job, requestID, now)

	case codebuildStatusFailed, codebuildStatusFault, codebuildStatusStopped, codebuildStatusTimedOut:
		return s.advanceProvisionDeployMcpWaitFailed(ctx, job, requestID, now, deepLink)

	default:
		return s.advanceProvisionDeployMcpWaitUnknownStatus(ctx, job, requestID, now, status)
	}
}

func (s *Server) persistModelAndInstance(ctx context.Context, model any, instanceSlug string, instanceUpdate func(core.UpdateBuilder) error) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	updateInst := &models.Instance{Slug: strings.TrimSpace(instanceSlug)}
	_ = updateInst.UpdateKeys()

	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Put(model)
		if instanceUpdate != nil {
			tx.UpdateWithBuilder(updateInst, instanceUpdate, tabletheory.IfExists())
		}
		return nil
	})
}

func (s *Server) persistJobAndInstance(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, instanceUpdate func(core.UpdateBuilder) error) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if job == nil {
		return fmt.Errorf("job is nil")
	}

	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
	clearProvisionJobConsentPlaintext(job)
	_ = job.UpdateKeys()

	return s.persistModelAndInstance(ctx, job, strings.TrimSpace(job.InstanceSlug), instanceUpdate)
}

func (s *Server) requeueProvisionJob(ctx context.Context, jobID string, delay time.Duration) error {
	return s.requeueJob(ctx, provisioning.JobMessage{Kind: "provision_job", JobID: strings.TrimSpace(jobID)}, delay)
}

func sqsDelaySeconds(delay time.Duration) int32 {
	delaySeconds := int32(delay.Round(time.Second).Seconds())
	if delaySeconds < 0 {
		return 0
	}
	if delaySeconds > 900 {
		return 900
	}
	return delaySeconds
}

func (s *Server) requeueJob(ctx context.Context, msg provisioning.JobMessage, delay time.Duration) error {
	if s == nil || s.sqs == nil {
		return fmt.Errorf("sqs client not initialized")
	}
	url := strings.TrimSpace(s.cfg.ProvisionQueueURL)
	if url == "" {
		return fmt.Errorf("provision queue url is not configured")
	}
	msg.JobID = strings.TrimSpace(msg.JobID)
	if msg.JobID == "" {
		return nil
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = s.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:     aws.String(url),
		MessageBody:  aws.String(string(body)),
		DelaySeconds: sqsDelaySeconds(delay),
	})
	return err
}

func expandManagedAccountEmailTemplate(tmpl string, slug string) string {
	tmpl = strings.TrimSpace(tmpl)
	slug = strings.TrimSpace(slug)
	if tmpl == "" {
		return ""
	}
	return strings.ReplaceAll(tmpl, "{slug}", slug)
}

func ensureAccountMatchesExpected(acct *orgtypes.Account, expectedName string) error {
	if acct == nil {
		return fmt.Errorf("account lookup returned nil")
	}
	expectedName = strings.TrimSpace(expectedName)
	if expectedName != "" {
		actualName := strings.TrimSpace(aws.ToString(acct.Name))
		if actualName != "" && !strings.EqualFold(actualName, expectedName) {
			return fmt.Errorf("account email already exists but name %q does not match expected %q", actualName, expectedName)
		}
	}
	status := acct.Status
	if status != "" && status != orgtypes.AccountStatusActive {
		return fmt.Errorf("account status %s is not active", status)
	}
	return nil
}

func ensureAdoptedAccountMatchesExpected(acct *orgtypes.Account, accountID string, expectedEmail string, expectedName string) error {
	if acct == nil {
		return fmt.Errorf("account lookup returned nil")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || strings.TrimSpace(aws.ToString(acct.Id)) != accountID {
		return fmt.Errorf("adopted account id does not match Organizations account")
	}
	expectedEmail = strings.ToLower(strings.TrimSpace(expectedEmail))
	if expectedEmail == "" {
		return fmt.Errorf("expected account email is required for account adoption")
	}
	actualEmail := strings.ToLower(strings.TrimSpace(aws.ToString(acct.Email)))
	if actualEmail == "" || actualEmail != expectedEmail {
		return fmt.Errorf("adopted account email does not match expected managed account email")
	}
	return ensureAccountMatchesExpected(acct, expectedName)
}

func (s *Server) findAccountByID(ctx context.Context, accountID string) (*orgtypes.Account, error) {
	if s == nil || s.org == nil {
		return nil, fmt.Errorf("org client not initialized")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, nil
	}

	var nextToken *string
	for {
		out, err := s.org.ListAccounts(ctx, &organizations.ListAccountsInput{NextToken: nextToken})
		if err != nil {
			return nil, err
		}
		if out != nil {
			for _, acct := range out.Accounts {
				if strings.TrimSpace(aws.ToString(acct.Id)) == accountID {
					return &acct, nil
				}
			}
			if out.NextToken == nil || strings.TrimSpace(aws.ToString(out.NextToken)) == "" {
				break
			}
			nextToken = out.NextToken
			continue
		}
		break
	}
	return nil, nil
}

func (s *Server) validateAdoptedProvisionAccount(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if s == nil || job == nil {
		return 0, false, nil
	}
	accountID := strings.TrimSpace(job.AccountID)
	if accountID == "" || strings.TrimSpace(job.AccountRequestID) != "" {
		return 0, false, nil
	}

	expectedEmail := strings.TrimSpace(job.AccountEmail)
	if expectedEmail == "" {
		expectedEmail = strings.TrimSpace(expandManagedAccountEmailTemplate(s.cfg.ManagedAccountEmailTemplate, job.InstanceSlug))
	}
	expectedName := managedAccountName(s.cfg.ManagedAccountNamePrefix, job.InstanceSlug)

	acct, err := s.findAccountByID(ctx, accountID)
	if err != nil {
		if isOrgAccessDenied(err) {
			return 0, false, s.failOrgPermissions(ctx, job, requestID, now, "ListAccounts", err)
		}
		return s.retryProvisionJobOrFail(ctx, job, requestID, now, "account_lookup_failed", "account lookup failed for adopted account: "+err.Error(), provisionDefaultShortRetryDelay, 5*time.Minute)
	}
	if acct == nil {
		return 0, false, s.failJob(ctx, job, requestID, now, "account_adoption_invalid", "adopted AWS account is not visible in the managed organization")
	}
	if matchErr := ensureAdoptedAccountMatchesExpected(acct, accountID, expectedEmail, expectedName); matchErr != nil {
		return 0, false, s.failJob(ctx, job, requestID, now, "account_adoption_invalid", matchErr.Error())
	}
	if strings.TrimSpace(job.AccountEmail) == "" {
		job.AccountEmail = expectedEmail
	}
	return 0, false, nil
}

func (s *Server) assumeInstanceRole(ctx context.Context, accountID string, roleName string, slug string, jobID string) (*sts.AssumeRoleOutput, time.Duration, error) {
	if s == nil || s.sts == nil {
		return nil, 0, fmt.Errorf("sts client not initialized")
	}

	accountID = strings.TrimSpace(accountID)
	roleName = strings.TrimSpace(roleName)
	if accountID == "" || roleName == "" {
		return nil, 0, fmt.Errorf("account id and role name are required")
	}

	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)
	sessionName := fmt.Sprintf("lesser-host-%s-%s", strings.TrimSpace(slug), strings.TrimSpace(jobID))
	if len(sessionName) > 64 {
		sessionName = sessionName[:64]
	}

	out, err := s.sts.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String(sessionName),
		DurationSeconds: aws.Int32(3600),
	})
	if err != nil {
		if isRetryableAssumeRoleErr(err) {
			return nil, provisionDefaultPollDelay, newAssumeRoleError(err, roleArn)
		}
		return nil, 0, err
	}
	return out, 0, nil
}

func (s *Server) ensureChildHostedZone(ctx context.Context, accountID string, roleName string, baseDomain string, existingZoneID string, existingNameServers []string, slug string, jobID string) (string, []string, error) {
	accountID = strings.TrimSpace(accountID)
	roleName = strings.TrimSpace(roleName)
	baseDomain = strings.TrimSpace(baseDomain)
	if accountID == "" || roleName == "" || baseDomain == "" {
		return "", nil, fmt.Errorf("accountID, roleName, and baseDomain are required")
	}

	domainDot := ensureTrailingDot(baseDomain)
	childClient, err := s.childRoute53Client(ctx, accountID, roleName, slug, jobID)
	if err != nil {
		return "", nil, err
	}
	return ensureHostedZoneAndNameServers(ctx, childClient, domainDot, existingZoneID, existingNameServers, jobID)
}

func (s *Server) childRoute53Client(ctx context.Context, accountID string, roleName string, slug string, jobID string) (*route53.Client, error) {
	if s == nil {
		return nil, fmt.Errorf("server not initialized")
	}

	assumed, _, err := s.assumeInstanceRole(ctx, accountID, roleName, slug, jobID)
	if err != nil {
		return nil, fmt.Errorf("assume instance role: %w", err)
	}
	if assumed == nil || assumed.Credentials == nil {
		return nil, fmt.Errorf("assume role returned empty credentials")
	}

	creds := credentials.NewStaticCredentialsProvider(
		aws.ToString(assumed.Credentials.AccessKeyId),
		aws.ToString(assumed.Credentials.SecretAccessKey),
		aws.ToString(assumed.Credentials.SessionToken),
	)
	return route53.New(route53.Options{
		Region:      strings.TrimSpace(s.cfg.ManagedDefaultRegion),
		Credentials: aws.NewCredentialsCache(creds),
	}), nil
}

func (s *Server) childSecretsManagerClient(ctx context.Context, accountID string, roleName string, region string, slug string, jobID string) (secretsManagerAPI, error) {
	if s == nil {
		return nil, fmt.Errorf("server not initialized")
	}
	if s.smFactory != nil {
		return s.smFactory(ctx, accountID, roleName, region, slug, jobID)
	}

	region = strings.TrimSpace(region)
	if region == "" {
		region = strings.TrimSpace(s.cfg.ManagedDefaultRegion)
	}
	if region == "" {
		region = defaultManagedAWSRegion
	}

	assumed, _, err := s.assumeInstanceRole(ctx, accountID, roleName, slug, jobID)
	if err != nil {
		return nil, fmt.Errorf("assume instance role: %w", err)
	}
	if assumed == nil || assumed.Credentials == nil {
		return nil, fmt.Errorf("assume role returned empty credentials")
	}

	creds := credentials.NewStaticCredentialsProvider(
		aws.ToString(assumed.Credentials.AccessKeyId),
		aws.ToString(assumed.Credentials.SecretAccessKey),
		aws.ToString(assumed.Credentials.SessionToken),
	)
	return secretsmanager.New(secretsmanager.Options{
		Region:      region,
		Credentials: aws.NewCredentialsCache(creds),
	}), nil
}

func ensureHostedZoneAndNameServers(
	ctx context.Context,
	childClient *route53.Client,
	domainDot string,
	existingZoneID string,
	existingNameServers []string,
	jobID string,
) (string, []string, error) {
	domainDot = strings.TrimSpace(domainDot)
	zoneID := normalizeHostedZoneID(existingZoneID)
	nameServers := normalizeNameServers(existingNameServers)
	if zoneID != "" && len(nameServers) > 0 {
		return zoneID, nameServers, nil
	}

	var err error

	if zoneID == "" {
		zoneID, err = findHostedZoneIDByName(ctx, childClient, domainDot)
		if err != nil {
			return "", nil, fmt.Errorf("list hosted zones by name: %w", err)
		}
	}

	if zoneID == "" {
		zoneID, nameServers, err = createHostedZone(ctx, childClient, domainDot, jobID)
		if err != nil {
			return "", nil, fmt.Errorf("create hosted zone: %w", err)
		}
	}

	if zoneID == "" {
		return "", nil, fmt.Errorf("unable to resolve child hosted zone id for %s", domainDot)
	}

	if len(nameServers) == 0 {
		nameServers, err = getHostedZoneNameServers(ctx, childClient, zoneID)
		if err != nil {
			return "", nil, fmt.Errorf("get hosted zone: %w", err)
		}
		nameServers = normalizeNameServers(nameServers)
	}

	if len(nameServers) == 0 {
		return "", nil, fmt.Errorf("child hosted zone has no name servers")
	}
	return zoneID, nameServers, nil
}

func normalizeNameServers(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return dedupeSortedStrings(out)
}

func findHostedZoneIDByName(ctx context.Context, client *route53.Client, domainDot string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("route53 client is nil")
	}
	out, err := client.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName:  aws.String(domainDot),
		MaxItems: aws.Int32(10),
	})
	if err != nil {
		return "", err
	}
	for _, hz := range out.HostedZones {
		if hz.Name != nil && strings.EqualFold(strings.TrimSpace(*hz.Name), domainDot) {
			if hz.Id != nil {
				return normalizeHostedZoneID(strings.TrimSpace(*hz.Id)), nil
			}
		}
	}
	return "", nil
}

func createHostedZone(ctx context.Context, client *route53.Client, domainDot string, jobID string) (string, []string, error) {
	if client == nil {
		return "", nil, fmt.Errorf("route53 client is nil")
	}
	out, err := client.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String(domainDot),
		CallerReference: aws.String("lesser-host-" + strings.TrimSpace(jobID)),
	})
	if err != nil {
		return "", nil, err
	}

	zoneID := ""
	if out != nil && out.HostedZone != nil && out.HostedZone.Id != nil {
		zoneID = normalizeHostedZoneID(strings.TrimSpace(*out.HostedZone.Id))
	}

	var ns []string
	if out != nil && out.DelegationSet != nil {
		ns = append(ns, out.DelegationSet.NameServers...)
	}
	return zoneID, normalizeNameServers(ns), nil
}

func getHostedZoneNameServers(ctx context.Context, client *route53.Client, zoneID string) ([]string, error) {
	if client == nil {
		return nil, fmt.Errorf("route53 client is nil")
	}
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return nil, fmt.Errorf("zone id is required")
	}
	out, err := client.GetHostedZone(ctx, &route53.GetHostedZoneInput{
		Id: aws.String(zoneID),
	})
	if err != nil {
		return nil, err
	}
	if out == nil || out.DelegationSet == nil {
		return nil, nil
	}
	return out.DelegationSet.NameServers, nil
}

func (s *Server) upsertParentNSDelegation(ctx context.Context, parentZoneID string, baseDomain string, nameServers []string) error {
	if s == nil || s.r53 == nil {
		return fmt.Errorf("route53 client not initialized")
	}
	parentZoneID = strings.TrimSpace(parentZoneID)
	baseDomain = strings.TrimSpace(baseDomain)
	if parentZoneID == "" || baseDomain == "" {
		return fmt.Errorf("parentZoneID and baseDomain are required")
	}
	if len(nameServers) == 0 {
		return fmt.Errorf("nameServers are required")
	}

	ns := make([]string, 0, len(nameServers))
	for _, n := range nameServers {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		ns = append(ns, n)
	}
	sort.Strings(ns)
	ns = dedupeSortedStrings(ns)
	if len(ns) == 0 {
		return fmt.Errorf("nameServers are required")
	}

	records := make([]r53types.ResourceRecord, 0, len(ns))
	for _, n := range ns {
		records = append(records, r53types.ResourceRecord{Value: aws.String(n)})
	}

	_, err := s.r53.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(parentZoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Comment: aws.String("lesser.host managed instance delegation"),
			Changes: []r53types.Change{
				{
					Action: r53types.ChangeActionUpsert,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name:            aws.String(ensureTrailingDot(baseDomain)),
						Type:            r53types.RRTypeNs,
						TTL:             aws.Int64(300),
						ResourceRecords: records,
					},
				},
			},
		},
	})
	return err
}

func (s *Server) provisionRunnerProjectName() (string, error) {
	projectName := strings.TrimSpace(s.cfg.ManagedProvisionRunnerProjectName)
	if projectName == "" {
		return "", fmt.Errorf("runner project name not configured")
	}
	return projectName, nil
}

func (s *Server) validateDeployRunnerJob(job *models.ProvisionJob) error {
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	if strings.TrimSpace(job.InstanceSlug) == "" {
		return fmt.Errorf("instance slug not configured")
	}
	if strings.TrimSpace(job.AdminUsername) == "" {
		return fmt.Errorf("admin username not configured")
	}
	if strings.TrimSpace(job.AdminWalletAddr) == "" {
		return fmt.Errorf("admin wallet not configured")
	}
	return nil
}

func (s *Server) deployRunnerStage(job *models.ProvisionJob) string {
	stage := strings.TrimSpace(job.Stage)
	if stage == "" {
		stage = strings.TrimSpace(s.cfg.Stage)
	}
	return normalizeManagedLesserStage(stage)
}

func (s *Server) buildDeployRunnerEnv(job *models.ProvisionJob, stage, receiptKey, bootstrapKey string) []cbtypes.EnvironmentVariable {
	consentMessage := job.ConsentMessage
	consentMessageB64 := ""
	if consentMessage != "" {
		consentMessageB64 = base64.StdEncoding.EncodeToString([]byte(consentMessage))
	}
	// Preserve exact consent signature bytes for the runner env —
	// the signature was part of the signed payload and must not be
	// trimmed before it reaches the deploy runner.
	consentSignature := job.ConsentSignature

	env := []cbtypes.EnvironmentVariable{
		{Name: aws.String("JOB_ID"), Value: aws.String(strings.TrimSpace(job.ID))},
		{Name: aws.String("APP_SLUG"), Value: aws.String(strings.TrimSpace(job.InstanceSlug))},
		{Name: aws.String("STAGE"), Value: aws.String(stage)},
		{Name: aws.String("ADMIN_USERNAME"), Value: aws.String(strings.TrimSpace(job.AdminUsername))},
		{Name: aws.String("ADMIN_WALLET_ADDRESS"), Value: aws.String(strings.TrimSpace(job.AdminWalletAddr))},
		{Name: aws.String("ADMIN_WALLET_CHAIN_ID"), Value: aws.String(fmt.Sprintf("%d", job.AdminWalletChainID))},
		{Name: aws.String("CONSENT_MESSAGE_B64"), Value: aws.String(consentMessageB64)},
		{Name: aws.String("CONSENT_SIGNATURE"), Value: aws.String(consentSignature)},
		{Name: aws.String("BASE_DOMAIN"), Value: aws.String(strings.TrimSpace(job.BaseDomain))},
		{Name: aws.String("TARGET_ACCOUNT_ID"), Value: aws.String(strings.TrimSpace(job.AccountID))},
		{Name: aws.String("TARGET_ROLE_NAME"), Value: aws.String(strings.TrimSpace(job.AccountRoleName))},
		{Name: aws.String("TARGET_REGION"), Value: aws.String(strings.TrimSpace(job.Region))},
		{Name: aws.String("DEPLOY_EXTERNAL_ID"), Value: aws.String(deployRunnerExternalID(strings.TrimSpace(job.InstanceSlug)))},
		{Name: aws.String("LESSER_VERSION"), Value: aws.String(strings.TrimSpace(job.LesserVersion))},
		{Name: aws.String("ARTIFACT_BUCKET"), Value: aws.String(strings.TrimSpace(s.cfg.ArtifactBucketName))},
		{Name: aws.String("RECEIPT_S3_KEY"), Value: aws.String(receiptKey)},
		{Name: aws.String("BOOTSTRAP_S3_KEY"), Value: aws.String(bootstrapKey)},
		{Name: aws.String("GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubOwner))},
		{Name: aws.String("GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubRepo))},
		{Name: aws.String("LESSER_BODY_GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserBodyGitHubOwner))},
		{Name: aws.String("LESSER_BODY_GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserBodyGitHubRepo))},
		{Name: aws.String("LESSER_BODY_VERSION"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserBodyDefaultVersion))},
	}

	return env
}
