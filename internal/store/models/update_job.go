package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	UpdateJobStatusQueued  = "queued"
	UpdateJobStatusRunning = "running"
	UpdateJobStatusOK      = "ok"
	UpdateJobStatusError   = "error"
)

// UpdateJob represents an asynchronous managed instance update workflow.
type UpdateJob struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID           string `theorydb:"attr:id" json:"id"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`

	Status string `theorydb:"attr:status" json:"status"`            // queued|running|ok|error
	Step   string `theorydb:"attr:step" json:"step,omitempty"`      // implementation-defined
	Note   string `theorydb:"attr:note" json:"note,omitempty"`      // operator-visible
	RunID  string `theorydb:"attr:runId" json:"run_id,omitempty"`   // external runner id (e.g. CodeBuild)
	RunURL string `theorydb:"attr:runUrl" json:"run_url,omitempty"` // external runner deep link (e.g. CodeBuild logs)

	AccountID       string `theorydb:"attr:accountId" json:"account_id,omitempty"`
	AccountRoleName string `theorydb:"attr:accountRoleName" json:"account_role_name,omitempty"`
	Region          string `theorydb:"attr:region" json:"region,omitempty"`
	BaseDomain      string `theorydb:"attr:baseDomain" json:"base_domain,omitempty"`

	LesserVersion string `theorydb:"attr:lesserVersion" json:"lesser_version,omitempty"`

	// Desired configuration snapshot (applied during update).
	LesserHostBaseURL              string `theorydb:"attr:lesserHostBaseUrl" json:"lesser_host_base_url,omitempty"`
	LesserHostAttestationsURL      string `theorydb:"attr:lesserHostAttestationsUrl" json:"lesser_host_attestations_url,omitempty"`
	LesserHostInstanceKeySecretARN string `theorydb:"attr:lesserHostInstanceKeySecretArn" json:"lesser_host_instance_key_secret_arn,omitempty"`
	TranslationEnabled             bool   `theorydb:"attr:translationEnabled" json:"translation_enabled"`

	TipEnabled         bool   `theorydb:"attr:tipEnabled" json:"tip_enabled"`
	TipChainID         int64  `theorydb:"attr:tipChainId" json:"tip_chain_id,omitempty"`
	TipContractAddress string `theorydb:"attr:tipContractAddress" json:"tip_contract_address,omitempty"`

	AIEnabled                 bool `theorydb:"attr:aiEnabled" json:"ai_enabled"`
	AIModerationEnabled       bool `theorydb:"attr:aiModerationEnabled" json:"ai_moderation_enabled"`
	AINsfwDetectionEnabled    bool `theorydb:"attr:aiNsfwDetectionEnabled" json:"ai_nsfw_detection_enabled"`
	AISpamDetectionEnabled    bool `theorydb:"attr:aiSpamDetectionEnabled" json:"ai_spam_detection_enabled"`
	AIPiiDetectionEnabled     bool `theorydb:"attr:aiPiiDetectionEnabled" json:"ai_pii_detection_enabled"`
	AIContentDetectionEnabled bool `theorydb:"attr:aiContentDetectionEnabled" json:"ai_content_detection_enabled"`

	// Optional instance key rotation (safe overlap by leaving prior keys unrevoked).
	RotateInstanceKey    bool   `theorydb:"attr:rotateInstanceKey" json:"rotate_instance_key,omitempty"`
	RotatedInstanceKeyID string `theorydb:"attr:rotatedInstanceKeyId" json:"rotated_instance_key_id,omitempty"`

	// Post-deploy verification signals (nil until verification runs).
	VerifyTranslationOK  *bool  `theorydb:"attr:verifyTranslationOk" json:"verify_translation_ok,omitempty"`
	VerifyTrustOK        *bool  `theorydb:"attr:verifyTrustOk" json:"verify_trust_ok,omitempty"`
	VerifyTipsOK         *bool  `theorydb:"attr:verifyTipsOk" json:"verify_tips_ok,omitempty"`
	VerifyAIOK           *bool  `theorydb:"attr:verifyAiOk" json:"verify_ai_ok,omitempty"`
	VerifyTranslationErr string `theorydb:"attr:verifyTranslationErr" json:"verify_translation_err,omitempty"`
	VerifyTrustErr       string `theorydb:"attr:verifyTrustErr" json:"verify_trust_err,omitempty"`
	VerifyTipsErr        string `theorydb:"attr:verifyTipsErr" json:"verify_tips_err,omitempty"`
	VerifyAIErr          string `theorydb:"attr:verifyAiErr" json:"verify_ai_err,omitempty"`

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

func (UpdateJob) TableName() string { return MainTableName() }

func (j *UpdateJob) BeforeCreate() error {
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
		j.Status = UpdateJobStatusQueued
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 10
	}
	j.updateGSI1()
	return nil
}

func (j *UpdateJob) BeforeUpdate() error {
	j.UpdatedAt = time.Now().UTC()
	j.TTL = j.ExpiresAt.Unix()
	j.updateGSI1()
	return nil
}

func (j *UpdateJob) UpdateKeys() error {
	j.ID = strings.TrimSpace(j.ID)
	j.InstanceSlug = strings.ToLower(strings.TrimSpace(j.InstanceSlug))
	j.Status = strings.ToLower(strings.TrimSpace(j.Status))
	j.Step = strings.TrimSpace(j.Step)
	j.Note = strings.TrimSpace(j.Note)
	j.RunID = strings.TrimSpace(j.RunID)
	j.RunURL = strings.TrimSpace(j.RunURL)
	j.AccountID = strings.TrimSpace(j.AccountID)
	j.AccountRoleName = strings.TrimSpace(j.AccountRoleName)
	j.Region = strings.TrimSpace(j.Region)
	j.BaseDomain = strings.ToLower(strings.TrimSpace(j.BaseDomain))
	j.LesserVersion = strings.TrimSpace(j.LesserVersion)
	j.LesserHostBaseURL = strings.TrimSpace(j.LesserHostBaseURL)
	j.LesserHostAttestationsURL = strings.TrimSpace(j.LesserHostAttestationsURL)
	j.LesserHostInstanceKeySecretARN = strings.TrimSpace(j.LesserHostInstanceKeySecretARN)
	j.TipContractAddress = strings.TrimSpace(j.TipContractAddress)
	j.RotatedInstanceKeyID = strings.TrimSpace(j.RotatedInstanceKeyID)
	j.VerifyTranslationErr = strings.TrimSpace(j.VerifyTranslationErr)
	j.VerifyTrustErr = strings.TrimSpace(j.VerifyTrustErr)
	j.VerifyTipsErr = strings.TrimSpace(j.VerifyTipsErr)
	j.VerifyAIErr = strings.TrimSpace(j.VerifyAIErr)
	j.ReceiptJSON = strings.TrimSpace(j.ReceiptJSON)
	j.ErrorCode = strings.TrimSpace(j.ErrorCode)
	j.ErrorMessage = strings.TrimSpace(j.ErrorMessage)
	j.RequestID = strings.TrimSpace(j.RequestID)

	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 10
	}

	j.PK = fmt.Sprintf("UPDATE_JOB#%s", j.ID)
	j.SK = SKJob
	j.TTL = j.ExpiresAt.Unix()
	j.updateGSI1()
	return nil
}

func (j *UpdateJob) GetPK() string { return j.PK }
func (j *UpdateJob) GetSK() string { return j.SK }

func (j *UpdateJob) updateGSI1() {
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
	j.GSI1PK = fmt.Sprintf("UPDATE_INSTANCE#%s", instanceSlug)
	j.GSI1SK = fmt.Sprintf("%s#%s", createdAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(j.ID))
}
