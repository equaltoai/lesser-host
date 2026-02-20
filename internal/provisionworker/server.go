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
	"net/url"
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
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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

	httpClient *http.Client

	org organizationsAPI
	r53 route53API
	sts stsAPI
	sqs sqsAPI
	cb  codebuildAPI
	s3  s3API

	smFactory secretsManagerClientFactory
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

	return s.runManagedProvisioningStateMachine(ctx, job, requestID, now)
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
	job.RequestID = strings.TrimSpace(requestID)
	job.UpdatedAt = now
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
	provisionStepSoulDeployStart    = "soul.deploy.start"
	provisionStepSoulDeployWait     = "soul.deploy.wait"
	provisionStepSoulInitStart      = "soul.init.start"
	provisionStepSoulInitWait       = "soul.init.wait"
	provisionStepSoulReceiptIngest  = "soul.receipt.ingest"
	provisionStepDone               = "done"
	provisionStepFailed             = "failed"
	provisionMaxTransitionsPerRun   = 6
	provisionMaxAccountCreateAge    = 90 * time.Minute
	provisionMaxAssumeRoleAge       = 30 * time.Minute
	provisionMaxDeployAge           = 3 * time.Hour
	provisionDefaultPollDelay       = 45 * time.Second
	provisionDefaultShortRetryDelay = 20 * time.Second

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
	case "live", "prod", "production":
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
	provisionStepSoulDeployStart:   (*Server).advanceProvisionSoulDeployStart,
	provisionStepSoulDeployWait:    (*Server).advanceProvisionSoulDeployWait,
	provisionStepSoulInitStart:     (*Server).advanceProvisionSoulInitStart,
	provisionStepSoulInitWait:      (*Server).advanceProvisionSoulInitWait,
	provisionStepSoulReceiptIngest: (*Server).advanceProvisionSoulReceiptIngest,
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

	handled, delay, done, err := s.tryReuseAccountByEmail(ctx, job, requestID, now, email, accountName)
	if handled {
		return delay, done, err
	}

	return s.requestAccountCreate(ctx, job, requestID, now, email, accountName, roleName)
}

func (s *Server) tryReuseAccountByEmail(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, email string, accountName string) (bool, time.Duration, bool, error) {
	if strings.TrimSpace(email) == "" {
		return false, 0, false, nil
	}

	acct, err := s.findAccountByEmail(ctx, email)
	if err != nil {
		if isOrgAccessDenied(err) {
			return true, 0, false, s.failOrgPermissions(ctx, job, requestID, now, "ListAccounts", err)
		}
		delay, done, retryErr := s.retryProvisionJobOrFail(ctx, job, requestID, now, "account_lookup_failed", "account lookup failed: "+err.Error(), provisionDefaultShortRetryDelay, 5*time.Minute)
		return true, delay, done, retryErr
	}
	if acct == nil {
		return false, 0, false, nil
	}

	if matchErr := ensureAccountMatchesExpected(acct, accountName); matchErr != nil {
		return true, 0, false, s.failJob(ctx, job, requestID, now, "account_email_conflict", matchErr.Error())
	}
	if strings.TrimSpace(job.AccountID) == "" {
		job.AccountID = strings.TrimSpace(aws.ToString(acct.Id))
	}
	job.Note = "AWS account already exists; reusing"
	delay, done, err := s.advanceToAccountMove(ctx, job, requestID, now, job.Note)
	return true, delay, done, err
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
	if err := s.persistJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
		ub.Set("HostedAccountID", strings.TrimSpace(job.AccountID))
		return nil
	}); err != nil {
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
	email := strings.TrimSpace(job.AccountEmail)
	if email == "" {
		email = strings.TrimSpace(expandManagedAccountEmailTemplate(s.cfg.ManagedAccountEmailTemplate, job.InstanceSlug))
		job.AccountEmail = email
	}
	accountName := strings.TrimSpace(strings.TrimSpace(s.cfg.ManagedAccountNamePrefix) + strings.TrimSpace(job.InstanceSlug))
	if len(accountName) > 50 {
		accountName = accountName[:50]
	}
	acct, err := s.findAccountByEmail(ctx, email)
	if err != nil {
		if isOrgAccessDenied(err) {
			return 0, false, s.failOrgPermissions(ctx, job, requestID, now, "ListAccounts", err), true
		}
		delay, done, retryErr := s.retryProvisionJobOrFail(ctx, job, requestID, now, "account_lookup_failed", "account lookup failed after email exists: "+err.Error(), provisionDefaultShortRetryDelay, 5*time.Minute)
		return delay, done, retryErr, true
	}
	if acct == nil {
		return 0, false, nil, false
	}
	if matchErr := ensureAccountMatchesExpected(acct, accountName); matchErr != nil {
		return 0, false, s.failJob(ctx, job, requestID, now, "account_email_conflict", matchErr.Error()), true
	}
	job.AccountID = strings.TrimSpace(aws.ToString(acct.Id))
	job.Note = "AWS account already exists; reusing"
	delay, done, err := s.advanceToAccountMove(ctx, job, requestID, now, job.Note)
	return delay, done, err, true
}

func (s *Server) advanceProvisionAccountMove(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
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
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
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
		if errors.Is(err, errAssumeRoleNotReady) {
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

	secretArn, err := s.ensureManagedInstanceKeySecret(ctx, job, inst)
	if err != nil {
		return s.retryProvisionJobOrFail(ctx, job, requestID, now, "instance_key_secret_failed", "failed to ensure instance key secret: "+err.Error(), provisionDefaultShortRetryDelay, 5*time.Minute)
	}

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
	managedInstanceKeySecretTagInstanceSlug = "lesser-host:instance-slug"
	managedInstanceKeySecretTagKeyID        = "lesser-host:instance-key-id"
	managedInstanceKeySecretTagManaged      = "lesser-host:managed"
)

func managedInstanceKeySecretName(controlPlaneStage, slug string) string {
	stage := strings.ToLower(strings.TrimSpace(controlPlaneStage))
	if stage == "" {
		stage = defaultControlPlaneStage
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("lesser-host/%s/instances/%s/instance-key", stage, slug)
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

	arn, describeErr := s.describeAndEnsureManagedInstanceKeySecret(ctx, sm, inputs.slug, secretID)
	if describeErr == nil {
		return arn, nil
	}
	if !isSecretsManagerNotFound(describeErr) {
		return "", describeErr
	}

	createdArn, keyID, createErr := s.createManagedInstanceKeySecret(ctx, sm, secretName, inputs.slug)
	if createErr != nil {
		if isSecretsManagerExists(createErr) {
			return s.describeAndEnsureManagedInstanceKeySecret(ctx, sm, inputs.slug, secretName)
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
	return s.describeAndEnsureManagedInstanceKeySecret(ctx, sm, inputs.slug, secretName)
}

func (s *Server) rotateManagedInstanceKeySecret(ctx context.Context, job *models.ProvisionJob, secretArn string) (string, error) {
	if err := s.requireStoreDB(); err != nil {
		return "", err
	}
	if job == nil {
		return "", fmt.Errorf("job is required")
	}
	secretArn = strings.TrimSpace(secretArn)
	if secretArn == "" {
		return "", fmt.Errorf("secretArn is required")
	}

	inputs, inputErr := managedInstanceSecretsInputsFromJob(job)
	if inputErr != nil || strings.TrimSpace(inputs.region) == "" {
		return "", fmt.Errorf("missing required rotation inputs")
	}

	sm, clientErr := s.childSecretsManagerClient(ctx, inputs.accountID, inputs.roleName, inputs.region, inputs.slug, inputs.jobID)
	if clientErr != nil {
		return "", clientErr
	}

	_, keyID, secretJSON, err := generateInstanceKeySecret()
	if err != nil {
		return "", err
	}

	if ensureErr := s.ensureInstanceKeyRecord(ctx, inputs.slug, keyID); ensureErr != nil {
		return "", fmt.Errorf("ensure instance key record: %w", ensureErr)
	}

	if _, err := sm.UpdateSecret(ctx, &secretsmanager.UpdateSecretInput{
		SecretId:     aws.String(secretArn),
		SecretString: aws.String(secretJSON),
	}); err != nil {
		return "", err
	}

	updateManagedInstanceKeySecretTags(ctx, sm, secretArn, inputs.slug, keyID)

	return keyID, nil
}

func (s *Server) advanceProvisionDeployStart(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if strings.TrimSpace(job.RunID) != "" {
		job.Step = provisionStepDeployWait
		job.Note = "deploy runner already started"
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
	if strings.TrimSpace(inst.LesserHostInstanceKeySecretARN) == "" {
		job.Step = provisionStepInstanceConfig
		job.Note = noteEnsuringInstanceConfiguration
		persistErr := s.persistJobAndInstance(ctx, job, requestID, now, nil)
		if persistErr != nil {
			return 0, false, persistErr
		}
		return 0, false, nil
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
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
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

	continueToSoul := job.SoulEnabled && job.SoulProvisionedAt.IsZero()
	if continueToSoul {
		job.Step = provisionStepSoulDeployStart
		job.Note = "starting soul deploy runner"
		job.RunID = ""
	} else {
		job.Step = provisionStepDone
		job.Status = models.ProvisionJobStatusOK
		job.Note = noteProvisioned
	}

	if err := s.persistJobAndInstance(ctx, job, requestID, now, provisionReceiptIngestInstanceUpdate(job, continueToSoul)); err != nil {
		return 0, false, err
	}
	return 0, !continueToSoul, nil
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

func provisionReceiptIngestInstanceUpdate(job *models.ProvisionJob, continueToSoul bool) func(core.UpdateBuilder) error {
	return func(ub core.UpdateBuilder) error {
		ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
		if continueToSoul {
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
		return nil
	}
}

func (s *Server) advanceProvisionSoulDeployStart(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}
	if !job.SoulEnabled {
		job.Step = provisionStepDone
		job.Status = models.ProvisionJobStatusOK
		job.Note = noteProvisioned
		if err := s.persistJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusOK)
			ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
			return nil
		}); err != nil {
			return 0, false, err
		}
		return 0, true, nil
	}

	if strings.TrimSpace(job.RunID) != "" {
		job.Step = provisionStepSoulDeployWait
		job.Note = "soul deploy runner already started"
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return provisionDefaultPollDelay, false, nil
	}

	runID, err := s.startDeployRunnerWithMode(ctx, job, "soul-deploy", s.soulReceiptS3Key(job))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_deploy_start_failed", "failed to start soul deploy runner: "+err.Error())
		}
		job.Note = "failed to start soul deploy runner; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 10*time.Minute), false, nil
	}

	job.RunID = strings.TrimSpace(runID)
	job.Step = provisionStepSoulDeployWait
	job.Note = "soul deploy runner in progress"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceProvisionSoulDeployWait(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	status, deepLink, err := s.getDeployRunnerStatus(ctx, strings.TrimSpace(job.RunID))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_deploy_status_failed", "failed to poll soul deploy runner: "+err.Error())
		}
		job.Note = "failed to poll soul deploy runner; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultPollDelay, 10*time.Minute), false, nil
	}

	switch status {
	case codebuildStatusSucceeded:
		job.Step = provisionStepSoulInitStart
		job.RunID = ""
		job.Note = "starting soul init runner"
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil

	case codebuildStatusInProgress:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_deploy_timeout", "soul deploy runner timed out")
		}
		job.Note = "soul deploy runner in progress"
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil

	case codebuildStatusFailed, codebuildStatusFault, codebuildStatusStopped, codebuildStatusTimedOut:
		msg := "soul deploy runner failed"
		if deepLink != "" {
			msg = msg + " (CodeBuild: " + deepLink + ")"
		}
		return 0, false, s.failJob(ctx, job, requestID, now, "soul_deploy_failed", msg)

	default:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_deploy_timeout", "soul deploy runner timed out")
		}
		job.Note = "soul deploy runner status: " + status
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil
	}
}

func (s *Server) advanceProvisionSoulInitStart(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}
	if strings.TrimSpace(job.RunID) != "" {
		job.Step = provisionStepSoulInitWait
		job.Note = "soul init runner already started"
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return provisionDefaultPollDelay, false, nil
	}

	runID, err := s.startDeployRunnerWithMode(ctx, job, "soul-init", s.soulReceiptS3Key(job))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_init_start_failed", "failed to start soul init runner: "+err.Error())
		}
		job.Note = "failed to start soul init runner; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 10*time.Minute), false, nil
	}

	job.RunID = strings.TrimSpace(runID)
	job.Step = provisionStepSoulInitWait
	job.Note = "soul init runner in progress"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceProvisionSoulInitWait(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	status, deepLink, err := s.getDeployRunnerStatus(ctx, strings.TrimSpace(job.RunID))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_init_status_failed", "failed to poll soul init runner: "+err.Error())
		}
		job.Note = "failed to poll soul init runner; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultPollDelay, 10*time.Minute), false, nil
	}

	switch status {
	case codebuildStatusSucceeded:
		job.Step = provisionStepSoulReceiptIngest
		job.Note = "ingesting soul receipt"
		if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
			return 0, false, err
		}
		return 0, false, nil

	case codebuildStatusInProgress:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_init_timeout", "soul init runner timed out")
		}
		job.Note = "soul init runner in progress"
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil

	case codebuildStatusFailed, codebuildStatusFault, codebuildStatusStopped, codebuildStatusTimedOut:
		msg := "soul init runner failed"
		if deepLink != "" {
			msg = msg + " (CodeBuild: " + deepLink + ")"
		}
		return 0, false, s.failJob(ctx, job, requestID, now, "soul_init_failed", msg)

	default:
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_init_timeout", "soul init runner timed out")
		}
		job.Note = "soul init runner status: " + status
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil
	}
}

func (s *Server) advanceProvisionSoulReceiptIngest(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	receiptKey := s.soulReceiptS3Key(job)
	receiptJSON, receipt, err := s.loadSoulReceiptFromS3(ctx, strings.TrimSpace(s.cfg.ArtifactBucketName), receiptKey)
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "soul_receipt_load_failed", "failed to load soul receipt: "+err.Error())
		}
		job.Note = "failed to load soul receipt; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 5*time.Minute), false, nil
	}

	job.SoulReceiptJSON = strings.TrimSpace(receiptJSON)
	job.SoulProvisionedAt = now
	job.Step = provisionStepDone
	job.Status = models.ProvisionJobStatusOK
	job.Note = noteProvisioned
	job.ErrorCode = ""
	job.ErrorMessage = ""

	soulVersion := ""
	if receipt != nil {
		soulVersion = strings.TrimSpace(receipt.SoulVersion)
	}

	if err := s.persistJobAndInstance(ctx, job, requestID, now, func(ub core.UpdateBuilder) error {
		ub.Set("ProvisionStatus", models.ProvisionJobStatusOK)
		ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
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

		ub.Set("SoulProvisionedAt", job.SoulProvisionedAt)
		if soulVersion != "" {
			ub.Set("SoulVersion", soulVersion)
		}
		return nil
	}); err != nil {
		return 0, false, err
	}
	return 0, true, nil
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

func (s *Server) findAccountByEmail(ctx context.Context, email string) (*orgtypes.Account, error) {
	if s == nil || s.org == nil {
		return nil, fmt.Errorf("org client not initialized")
	}
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
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
				if strings.EqualFold(strings.TrimSpace(aws.ToString(acct.Email)), email) {
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

var errAssumeRoleNotReady = errors.New("assume role not ready")

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
			return nil, provisionDefaultPollDelay, errAssumeRoleNotReady
		}
		return nil, 0, err
	}
	return out, 0, nil
}

func isRetryableAssumeRoleErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "AccessDenied") ||
		strings.Contains(msg, "AccessDeniedException") ||
		strings.Contains(msg, "NoSuchEntity") ||
		strings.Contains(msg, "could not be found") ||
		strings.Contains(msg, "is not authorized") ||
		strings.Contains(msg, "InvalidClientTokenId")
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
		region = "us-east-1"
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
	consentMessage := strings.TrimSpace(job.ConsentMessage)
	consentMessageB64 := ""
	if consentMessage != "" {
		consentMessageB64 = base64.StdEncoding.EncodeToString([]byte(consentMessage))
	}
	consentSignature := strings.TrimSpace(job.ConsentSignature)

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
		{Name: aws.String("LESSER_VERSION"), Value: aws.String(strings.TrimSpace(job.LesserVersion))},
		{Name: aws.String("ARTIFACT_BUCKET"), Value: aws.String(strings.TrimSpace(s.cfg.ArtifactBucketName))},
		{Name: aws.String("RECEIPT_S3_KEY"), Value: aws.String(receiptKey)},
		{Name: aws.String("BOOTSTRAP_S3_KEY"), Value: aws.String(bootstrapKey)},
		{Name: aws.String("GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubOwner))},
		{Name: aws.String("GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubRepo))},
	}
	if strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN) != "" {
		env = append(env, cbtypes.EnvironmentVariable{
			Name:  aws.String("MANAGED_ORG_VENDING_ROLE_ARN"),
			Value: aws.String(strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN)),
		})
	}

	return env
}

func codebuildBuildID(out *codebuild.StartBuildOutput) (string, error) {
	if out == nil || out.Build == nil {
		return "", fmt.Errorf("codebuild StartBuild returned empty build")
	}
	if out.Build.Id != nil && strings.TrimSpace(*out.Build.Id) != "" {
		return strings.TrimSpace(*out.Build.Id), nil
	}
	if out.Build.Arn != nil && strings.TrimSpace(*out.Build.Arn) != "" {
		return strings.TrimSpace(*out.Build.Arn), nil
	}
	return "", fmt.Errorf("codebuild StartBuild returned empty build id")
}

func (s *Server) startDeployRunner(ctx context.Context, job *models.ProvisionJob) (string, error) {
	return s.startDeployRunnerWithMode(ctx, job, "lesser", s.receiptS3Key(job))
}

func (s *Server) startDeployRunnerWithMode(ctx context.Context, job *models.ProvisionJob, mode string, receiptKey string) (string, error) {
	if s == nil || s.cb == nil {
		return "", fmt.Errorf("codebuild client not initialized")
	}
	if err := s.validateDeployRunnerJob(job); err != nil {
		return "", err
	}
	projectName, err := s.provisionRunnerProjectName()
	if err != nil {
		return "", err
	}

	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return "", err
	}
	if inst == nil {
		return "", fmt.Errorf("instance not found")
	}

	lesserHostURL := strings.TrimSpace(inst.LesserHostBaseURL)
	if lesserHostURL == "" {
		lesserHostURL = strings.TrimSpace(s.publicBaseURL())
	}
	lesserHostAttestationsURL := strings.TrimSpace(inst.LesserHostAttestationsURL)
	if lesserHostAttestationsURL == "" {
		lesserHostAttestationsURL = lesserHostURL
	}
	instanceKeySecretArn := strings.TrimSpace(inst.LesserHostInstanceKeySecretARN)
	if instanceKeySecretArn == "" {
		return "", fmt.Errorf("instance key secret arn is missing")
	}
	if lesserHostURL == "" {
		return "", fmt.Errorf("lesser host base url is missing")
	}

	bootstrapKey := s.bootstrapS3Key(job)
	stage := s.deployRunnerStage(job)
	env := s.buildDeployRunnerEnv(job, stage, receiptKey, bootstrapKey)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "lesser"
	}
	env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("RUN_MODE"), Value: aws.String(mode)})
	if strings.HasPrefix(mode, "soul") {
		env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("SOUL_VERSION"), Value: aws.String(strings.TrimSpace(inst.SoulVersion))})
	}
	tipEnabled := effectiveTipEnabled(inst.TipEnabled)
	env = append(env,
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_URL"), Value: aws.String(lesserHostURL)},
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_ATTESTATIONS_URL"), Value: aws.String(lesserHostAttestationsURL)},
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_INSTANCE_KEY_ARN"), Value: aws.String(instanceKeySecretArn)},
		cbtypes.EnvironmentVariable{Name: aws.String("TRANSLATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveTranslationEnabled(inst.TranslationEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("TIP_ENABLED"), Value: aws.String(fmt.Sprintf("%t", tipEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAIEnabled(inst.LesserAIEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_MODERATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAIModerationEnabled(inst.LesserAIModerationEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_NSFW_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAINsfwDetectionEnabled(inst.LesserAINsfwDetectionEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_SPAM_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAISpamDetectionEnabled(inst.LesserAISpamDetectionEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_PII_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAIPiiDetectionEnabled(inst.LesserAIPiiDetectionEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_CONTENT_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAIContentDetectionEnabled(inst.LesserAIContentDetectionEnabled)))},
	)
	if tipEnabled {
		env = append(env,
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CHAIN_ID"), Value: aws.String(fmt.Sprintf("%d", inst.TipChainID))},
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CONTRACT_ADDRESS"), Value: aws.String(strings.TrimSpace(inst.TipContractAddress))},
		)
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

func (s *Server) getDeployRunnerStatus(ctx context.Context, runID string) (string, string, error) {
	if s == nil || s.cb == nil {
		return "", "", fmt.Errorf("codebuild client not initialized")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "", "", fmt.Errorf("runID is required")
	}

	out, err := s.cb.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{
		Ids: []string{runID},
	})
	if err != nil {
		return "", "", err
	}
	if out == nil || len(out.Builds) == 0 {
		return "", "", fmt.Errorf("build not found")
	}
	build := out.Builds[0]
	return normalizeCodebuildStatus(build.BuildStatus), codebuildBuildDeepLink(build), nil
}

func codebuildBuildDeepLink(build cbtypes.Build) string {
	if build.Logs == nil || build.Logs.DeepLink == nil {
		return ""
	}
	return strings.TrimSpace(*build.Logs.DeepLink)
}

func normalizeCodebuildStatus(st cbtypes.StatusType) string {
	switch st {
	case cbtypes.StatusTypeInProgress:
		return codebuildStatusInProgress
	case cbtypes.StatusTypeSucceeded:
		return codebuildStatusSucceeded
	case cbtypes.StatusTypeFailed:
		return codebuildStatusFailed
	case cbtypes.StatusTypeFault:
		return codebuildStatusFault
	case cbtypes.StatusTypeStopped:
		return codebuildStatusStopped
	case cbtypes.StatusTypeTimedOut:
		return codebuildStatusTimedOut
	default:
		status := strings.TrimSpace(string(st))
		if status == "" {
			return codebuildStatusUnknown
		}
		return status
	}
}

func ensureTrailingDot(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

func normalizeHostedZoneID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "/hostedzone/")
	id = strings.TrimPrefix(id, "hostedzone/")
	return id
}

func dedupeSortedStrings(in []string) []string {
	out := make([]string, 0, len(in))
	var last string
	for i, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if i == 0 || s != last {
			out = append(out, s)
			last = s
		}
	}
	return out
}

func compactErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "unknown error"
	}
	const maxLen = 350
	if len(msg) > maxLen {
		msg = msg[:maxLen] + "…"
	}
	return msg
}

func jitteredBackoff(attempt int64, minDelay time.Duration, maxDelay time.Duration) time.Duration {
	if attempt <= 0 {
		return minDelay
	}
	delay := minDelay
	for i := int64(1); i < attempt; i++ {
		delay *= 2
		if delay >= maxDelay {
			delay = maxDelay
			break
		}
	}
	// Cheap jitter: add up to 10% based on attempt parity.
	jitter := time.Duration(int64(delay) / 10)
	if attempt%2 == 0 {
		delay += jitter
	}
	if delay < minDelay {
		return minDelay
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func sqsQueueNameFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
