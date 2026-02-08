package provisionworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
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

// Server processes provisioning jobs from the worker queue.
type Server struct {
	cfg config.Config

	store *store.Store

	org organizationsAPI
	r53 route53API
	sts stsAPI
	sqs sqsAPI
	cb  codebuildAPI
	s3  s3API
}

// NewServer constructs a Server with AWS service clients and a store.
func NewServer(cfg config.Config, st *store.Store, org organizationsAPI, r53 route53API, stsClient stsAPI, sqsClient sqsAPI, cbClient codebuildAPI, s3Client s3API) *Server {
	return &Server{
		cfg:   cfg,
		store: st,
		org:   org,
		r53:   r53,
		sts:   stsClient,
		sqs:   sqsClient,
		cb:    cbClient,
		s3:    s3Client,
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
	if strings.TrimSpace(jm.Kind) != "provision_job" {
		return nil
	}
	jobID := strings.TrimSpace(jm.JobID)
	if jobID == "" {
		return nil
	}
	return s.processProvisionJob(ctx.Context(), ctx.RequestID, jobID)
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
	provisionStepDeployStart        = "deploy.start"
	provisionStepDeployWait         = "deploy.wait"
	provisionStepReceiptIngest      = "receipt.ingest"
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

type lesserUpReceipt struct {
	Version    int    `json:"version"`
	App        string `json:"app"`
	BaseDomain string `json:"base_domain"`
	AccountID  string `json:"account_id"`
	Region     string `json:"region"`
	HostedZone struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"hosted_zone"`
}

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

func (s *Server) advanceManagedProvisioning(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if s == nil || job == nil {
		return 0, true, nil
	}

	switch strings.TrimSpace(job.Step) {
	case provisionStepQueued:
		return s.advanceProvisionQueued(ctx, job, requestID, now)
	case provisionStepAccountCreate:
		return s.advanceProvisionAccountCreate(ctx, job, requestID, now)
	case provisionStepAccountCreatePoll:
		return s.advanceProvisionAccountCreatePoll(ctx, job, requestID, now)
	case provisionStepAccountMove:
		return s.advanceProvisionAccountMove(ctx, job, requestID, now)
	case provisionStepAssumeRole:
		return s.advanceProvisionAssumeRole(ctx, job, requestID, now)
	case provisionStepChildZone:
		return s.advanceProvisionChildZone(ctx, job, requestID, now)
	case provisionStepParentDelegation:
		return s.advanceProvisionParentDelegation(ctx, job, requestID, now)
	case provisionStepDeployStart:
		return s.advanceProvisionDeployStart(ctx, job, requestID, now)
	case provisionStepDeployWait:
		return s.advanceProvisionDeployWait(ctx, job, requestID, now)
	case provisionStepReceiptIngest:
		return s.advanceProvisionReceiptIngest(ctx, job, requestID, now)
	case provisionStepDone, provisionStepFailed:
		return 0, true, nil
	default:
		step := strings.TrimSpace(job.Step)
		return 0, false, s.failJob(ctx, job, requestID, now, "unknown_step", "unknown provisioning step: "+step)
	}
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

func (s *Server) startProvisionAccountCreate(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	email := strings.TrimSpace(job.AccountEmail)
	if email == "" {
		email = strings.TrimSpace(expandManagedAccountEmailTemplate(s.cfg.ManagedAccountEmailTemplate, job.InstanceSlug))
		job.AccountEmail = email
	}

	accountName := strings.TrimSpace(strings.TrimSpace(s.cfg.ManagedAccountNamePrefix) + strings.TrimSpace(job.InstanceSlug))
	if len(accountName) > 50 {
		accountName = accountName[:50]
	}

	roleName := strings.TrimSpace(job.AccountRoleName)
	if roleName == "" {
		roleName = strings.TrimSpace(s.cfg.ManagedInstanceRoleName)
	}

	out, err := s.org.CreateAccount(ctx, &organizations.CreateAccountInput{
		AccountName:            aws.String(accountName),
		Email:                  aws.String(email),
		RoleName:               aws.String(roleName),
		IamUserAccessToBilling: orgtypes.IAMUserAccessToBillingAllow,
	})
	if err != nil {
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

	job.Step = provisionStepDeployStart
	job.Note = "starting instance deploy runner"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
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
	job.Note = "deploy runner in progress"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceProvisionDeployWait(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
		return 0, false, s.failJob(ctx, job, requestID, now, "deploy_timeout", "deploy runner timed out")
	}

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
		job.Note = "deploy runner in progress"
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil

	case codebuildStatusFailed, codebuildStatusFault, codebuildStatusStopped, codebuildStatusTimedOut:
		msg := "deploy runner failed"
		if deepLink != "" {
			msg = msg + " (CodeBuild: " + deepLink + ")"
		}
		return 0, false, s.failJob(ctx, job, requestID, now, "deploy_failed", msg)

	default:
		job.Note = "deploy runner status: " + status
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return provisionDefaultPollDelay, false, nil
	}
}

func (s *Server) advanceProvisionReceiptIngest(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	receiptKey := s.receiptS3Key(job)
	receiptJSON, receipt, err := s.loadReceiptFromS3(ctx, strings.TrimSpace(s.cfg.ArtifactBucketName), receiptKey)
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, "receipt_load_failed", "failed to load receipt: "+err.Error())
		}
		job.Note = "failed to load receipt; retrying: " + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 5*time.Minute), false, nil
	}

	job.ReceiptJSON = strings.TrimSpace(receiptJSON)
	job.Step = provisionStepDone
	job.Status = models.ProvisionJobStatusOK
	job.Note = "provisioned"
	job.ErrorCode = ""
	job.ErrorMessage = ""

	if receipt != nil {
		if strings.TrimSpace(receipt.AccountID) != "" {
			job.AccountID = strings.TrimSpace(receipt.AccountID)
		}
		if strings.TrimSpace(receipt.Region) != "" {
			job.Region = strings.TrimSpace(receipt.Region)
		}
		if strings.TrimSpace(receipt.HostedZone.ID) != "" {
			job.ChildHostedZoneID = normalizeHostedZoneID(strings.TrimSpace(receipt.HostedZone.ID))
		}
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
		return nil
	}); err != nil {
		return 0, false, err
	}
	return 0, true, nil
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

func (s *Server) requeueProvisionJob(ctx context.Context, jobID string, delay time.Duration) error {
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

	body, err := json.Marshal(provisioning.JobMessage{Kind: "provision_job", JobID: jobID})
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

func expandManagedAccountEmailTemplate(tmpl string, slug string) string {
	tmpl = strings.TrimSpace(tmpl)
	slug = strings.TrimSpace(slug)
	if tmpl == "" {
		return ""
	}
	return strings.ReplaceAll(tmpl, "{slug}", slug)
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

func (s *Server) startDeployRunner(ctx context.Context, job *models.ProvisionJob) (string, error) {
	if s == nil || s.cb == nil {
		return "", fmt.Errorf("codebuild client not initialized")
	}
	if job == nil {
		return "", fmt.Errorf("job is nil")
	}

	projectName := strings.TrimSpace(s.cfg.ManagedProvisionRunnerProjectName)
	if projectName == "" {
		return "", fmt.Errorf("runner project name not configured")
	}

	if strings.TrimSpace(job.AdminUsername) == "" {
		return "", fmt.Errorf("admin username not configured")
	}
	if strings.TrimSpace(job.AdminWalletAddr) == "" {
		return "", fmt.Errorf("admin wallet not configured")
	}

	receiptKey := s.receiptS3Key(job)
	bootstrapKey := s.bootstrapS3Key(job)

	stage := strings.TrimSpace(job.Stage)
	if stage == "" {
		stage = strings.TrimSpace(s.cfg.Stage)
	}

	var env []cbtypes.EnvironmentVariable
	env = append(env,
		cbtypes.EnvironmentVariable{Name: aws.String("JOB_ID"), Value: aws.String(strings.TrimSpace(job.ID))},
		cbtypes.EnvironmentVariable{Name: aws.String("APP_SLUG"), Value: aws.String(strings.TrimSpace(job.InstanceSlug))},
		cbtypes.EnvironmentVariable{Name: aws.String("STAGE"), Value: aws.String(stage)},
		cbtypes.EnvironmentVariable{Name: aws.String("ADMIN_USERNAME"), Value: aws.String(strings.TrimSpace(job.AdminUsername))},
		cbtypes.EnvironmentVariable{Name: aws.String("ADMIN_WALLET_ADDRESS"), Value: aws.String(strings.TrimSpace(job.AdminWalletAddr))},
		cbtypes.EnvironmentVariable{Name: aws.String("ADMIN_WALLET_CHAIN_ID"), Value: aws.String(fmt.Sprintf("%d", job.AdminWalletChainID))},
		cbtypes.EnvironmentVariable{Name: aws.String("BASE_DOMAIN"), Value: aws.String(strings.TrimSpace(job.BaseDomain))},
		cbtypes.EnvironmentVariable{Name: aws.String("TARGET_ACCOUNT_ID"), Value: aws.String(strings.TrimSpace(job.AccountID))},
		cbtypes.EnvironmentVariable{Name: aws.String("TARGET_ROLE_NAME"), Value: aws.String(strings.TrimSpace(job.AccountRoleName))},
		cbtypes.EnvironmentVariable{Name: aws.String("TARGET_REGION"), Value: aws.String(strings.TrimSpace(job.Region))},
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_VERSION"), Value: aws.String(strings.TrimSpace(job.LesserVersion))},
		cbtypes.EnvironmentVariable{Name: aws.String("ARTIFACT_BUCKET"), Value: aws.String(strings.TrimSpace(s.cfg.ArtifactBucketName))},
		cbtypes.EnvironmentVariable{Name: aws.String("RECEIPT_S3_KEY"), Value: aws.String(receiptKey)},
		cbtypes.EnvironmentVariable{Name: aws.String("BOOTSTRAP_S3_KEY"), Value: aws.String(bootstrapKey)},
		cbtypes.EnvironmentVariable{Name: aws.String("GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubOwner))},
		cbtypes.EnvironmentVariable{Name: aws.String("GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubRepo))},
	)
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

func (s *Server) receiptS3Key(job *models.ProvisionJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/%s/state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}

func (s *Server) bootstrapS3Key(job *models.ProvisionJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/bootstrap.json", strings.TrimSpace(job.InstanceSlug))
}

func (s *Server) loadReceiptFromS3(ctx context.Context, bucket string, key string) (string, *lesserUpReceipt, error) {
	if s == nil || s.s3 == nil {
		return "", nil, fmt.Errorf("s3 client not initialized")
	}
	bucket = strings.TrimSpace(bucket)
	key = strings.TrimSpace(key)
	if bucket == "" || key == "" {
		return "", nil, fmt.Errorf("bucket and key are required")
	}

	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = out.Body.Close() }()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return "", nil, err
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return "", nil, fmt.Errorf("receipt is empty")
	}

	var parsed lesserUpReceipt
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw, nil, err
	}
	if strings.TrimSpace(parsed.BaseDomain) == "" || strings.TrimSpace(parsed.App) == "" {
		return raw, &parsed, fmt.Errorf("receipt is missing required fields")
	}
	return raw, &parsed, nil
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
