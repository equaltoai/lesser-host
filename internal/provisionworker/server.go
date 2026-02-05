package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/route53"
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

// Server processes provisioning jobs from the worker queue.
type Server struct {
	cfg config.Config

	store *store.Store

	org organizationsAPI
	r53 route53API
	sts stsAPI
}

// NewServer constructs a Server with AWS service clients and a store.
func NewServer(cfg config.Config, st *store.Store, org organizationsAPI, r53 route53API, stsClient stsAPI) *Server {
	return &Server{
		cfg:   cfg,
		store: st,
		org:   org,
		r53:   r53,
		sts:   stsClient,
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

	job, err := s.store.GetProvisionJob(ctx, jobID)
	if theoryErrors.IsNotFound(err) || job == nil {
		return nil
	}
	if err != nil {
		return err
	}

	status := strings.ToLower(strings.TrimSpace(job.Status))
	if status != models.ProvisionJobStatusQueued && status != models.ProvisionJobStatusRunning {
		return nil
	}

	now := time.Now().UTC()

	if !s.cfg.ManagedProvisioningEnabled {
		return s.failJob(ctx, job, requestID, now, "disabled", "managed provisioning is disabled (set MANAGED_PROVISIONING_ENABLED=true)")
	}

	var missing []string
	if strings.TrimSpace(s.cfg.ManagedParentHostedZoneID) == "" {
		missing = append(missing, "MANAGED_PARENT_HOSTED_ZONE_ID")
	}
	if strings.TrimSpace(s.cfg.ManagedAccountEmailTemplate) == "" && strings.TrimSpace(job.AccountID) == "" && strings.TrimSpace(job.AccountRequestID) == "" {
		missing = append(missing, "MANAGED_ACCOUNT_EMAIL_TEMPLATE")
	}
	if strings.TrimSpace(s.cfg.ManagedInstanceRoleName) == "" {
		missing = append(missing, "MANAGED_INSTANCE_ROLE_NAME")
	}
	if len(missing) > 0 {
		return s.failJob(ctx, job, requestID, now, "missing_config", "missing required config: "+strings.Join(missing, ", "))
	}

	// M9 scaffold: account vending, DNS delegation, and deploy runner are implemented in follow-up changes.
	return s.failJob(ctx, job, requestID, now, "not_implemented", "provisioning worker scaffolding only (M9 in progress)")
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
