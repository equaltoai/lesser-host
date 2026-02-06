package models

import (
	"fmt"
	"strings"
	"time"
)

// ProvisionJobStatus* constants define the lifecycle state of a provisioning job.
const (
	ProvisionJobStatusQueued  = "queued"
	ProvisionJobStatusRunning = "running"
	ProvisionJobStatusOK      = "ok"
	ProvisionJobStatusError   = "error"
)

// ProvisionJob represents an asynchronous instance provisioning workflow.
type ProvisionJob struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID           string `theorydb:"attr:id" json:"id"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`

	Status        string `theorydb:"attr:status" json:"status"`                          // queued|running|ok|error
	Step          string `theorydb:"attr:step" json:"step,omitempty"`                    // implementation-defined
	Note          string `theorydb:"attr:note" json:"note,omitempty"`                    // operator-visible
	RunID         string `theorydb:"attr:runId" json:"run_id,omitempty"`                 // external runner id (e.g. CodeBuild)
	Mode          string `theorydb:"attr:mode" json:"mode,omitempty"`                    // managed|manual
	Plan          string `theorydb:"attr:plan" json:"plan,omitempty"`                    // hosting plan identifier
	Region        string `theorydb:"attr:region" json:"region,omitempty"`                // target region (if applicable)
	Stage         string `theorydb:"attr:stage" json:"stage,omitempty"`                  // optional stage selector
	LesserVersion string `theorydb:"attr:lesserVersion" json:"lesser_version,omitempty"` // semver tag

	// Account allocation / creation.
	AccountRequestID string `theorydb:"attr:accountRequestId" json:"account_request_id,omitempty"`
	AccountID        string `theorydb:"attr:accountId" json:"account_id,omitempty"`
	AccountEmail     string `theorydb:"attr:accountEmail" json:"account_email,omitempty"`
	AccountRoleName  string `theorydb:"attr:accountRoleName" json:"account_role_name,omitempty"`

	// DNS delegation (recommended v1: per-instance hosted zone with parent NS delegation).
	ParentHostedZoneID string   `theorydb:"attr:parentHostedZoneId" json:"parent_hosted_zone_id,omitempty"`
	BaseDomain         string   `theorydb:"attr:baseDomain" json:"base_domain,omitempty"`
	ChildHostedZoneID  string   `theorydb:"attr:childHostedZoneId" json:"child_hosted_zone_id,omitempty"`
	ChildNameServers   []string `theorydb:"attr:childNameServers" json:"child_name_servers,omitempty"`

	// Deployment receipt (optional; can be large).
	ReceiptJSON string `theorydb:"attr:receiptJson" json:"receipt_json,omitempty"`

	Attempts    int64 `theorydb:"attr:attempts" json:"attempts"`
	MaxAttempts int64 `theorydb:"attr:maxAttempts" json:"max_attempts,omitempty"`

	ErrorCode    string `theorydb:"attr:errorCode" json:"error_code,omitempty"`
	ErrorMessage string `theorydb:"attr:errorMessage" json:"error_message,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	RequestID string    `theorydb:"attr:requestId" json:"request_id,omitempty"`
}

// TableName returns the database table name for ProvisionJob.
func (ProvisionJob) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating ProvisionJob.
func (j *ProvisionJob) BeforeCreate() error {
	if err := j.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	if j.ExpiresAt.IsZero() {
		j.ExpiresAt = now.Add(30 * 24 * time.Hour)
	}
	j.TTL = j.ExpiresAt.Unix()
	if strings.TrimSpace(j.Status) == "" {
		j.Status = ProvisionJobStatusQueued
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 10
	}
	j.updateGSI1()
	return nil
}

// BeforeUpdate updates timestamps and TTL before updating ProvisionJob.
func (j *ProvisionJob) BeforeUpdate() error {
	j.UpdatedAt = time.Now().UTC()
	j.TTL = j.ExpiresAt.Unix()
	j.updateGSI1()
	return nil
}

// UpdateKeys updates the database keys for ProvisionJob.
func (j *ProvisionJob) UpdateKeys() error {
	j.ID = strings.TrimSpace(j.ID)
	j.InstanceSlug = strings.TrimSpace(j.InstanceSlug)
	j.Status = strings.ToLower(strings.TrimSpace(j.Status))
	j.Step = strings.TrimSpace(j.Step)
	j.Note = strings.TrimSpace(j.Note)
	j.RunID = strings.TrimSpace(j.RunID)
	j.Mode = strings.ToLower(strings.TrimSpace(j.Mode))
	j.Plan = strings.TrimSpace(j.Plan)
	j.Region = strings.TrimSpace(j.Region)
	j.Stage = strings.TrimSpace(j.Stage)
	j.LesserVersion = strings.TrimSpace(j.LesserVersion)
	j.AccountRequestID = strings.TrimSpace(j.AccountRequestID)
	j.AccountID = strings.TrimSpace(j.AccountID)
	j.AccountEmail = strings.TrimSpace(j.AccountEmail)
	j.AccountRoleName = strings.TrimSpace(j.AccountRoleName)
	j.ParentHostedZoneID = strings.TrimSpace(j.ParentHostedZoneID)
	j.BaseDomain = strings.ToLower(strings.TrimSpace(j.BaseDomain))
	j.ChildHostedZoneID = strings.TrimSpace(j.ChildHostedZoneID)
	j.ReceiptJSON = strings.TrimSpace(j.ReceiptJSON)
	j.ErrorCode = strings.TrimSpace(j.ErrorCode)
	j.ErrorMessage = strings.TrimSpace(j.ErrorMessage)
	j.RequestID = strings.TrimSpace(j.RequestID)

	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 10
	}

	j.PK = fmt.Sprintf("PROVISION_JOB#%s", j.ID)
	j.SK = "JOB"
	j.TTL = j.ExpiresAt.Unix()
	j.updateGSI1()
	return nil
}

// GetPK returns the partition key for ProvisionJob.
func (j *ProvisionJob) GetPK() string { return j.PK }

// GetSK returns the sort key for ProvisionJob.
func (j *ProvisionJob) GetSK() string { return j.SK }

func (j *ProvisionJob) updateGSI1() {
	if j == nil {
		return
	}
	instanceSlug := strings.TrimSpace(j.InstanceSlug)
	if instanceSlug == "" {
		j.GSI1PK = ""
		j.GSI1SK = ""
		return
	}

	createdAt := j.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	j.GSI1PK = fmt.Sprintf("PROVISION_INSTANCE#%s", instanceSlug)
	j.GSI1SK = fmt.Sprintf("%s#%s", createdAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(j.ID))
}
