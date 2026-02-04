package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	AIJobStatusQueued = "queued"
	AIJobStatusOK     = "ok"
	AIJobStatusError  = "error"
)

type AIJob struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID string `theorydb:"attr:id" json:"id"`

	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug,omitempty"`

	Module        string `theorydb:"attr:module" json:"module"`
	PolicyVersion string `theorydb:"attr:policyVersion" json:"policy_version"`
	ModelSet      string `theorydb:"attr:modelSet" json:"model_set"`

	CacheScope string `theorydb:"attr:cacheScope" json:"cache_scope,omitempty"`
	ScopeKey   string `theorydb:"attr:scopeKey" json:"scope_key,omitempty"`
	InputsHash string `theorydb:"attr:inputsHash" json:"inputs_hash"`

	InputsJSON string          `theorydb:"attr:inputsJson" json:"inputs_json,omitempty"`
	Evidence   []AIEvidenceRef `theorydb:"attr:evidence" json:"evidence,omitempty"`

	Status string `theorydb:"attr:status" json:"status"` // queued|ok|error

	Attempts    int64 `theorydb:"attr:attempts" json:"attempts"`
	MaxAttempts int64 `theorydb:"attr:maxAttempts" json:"max_attempts,omitempty"`

	ErrorCode    string `theorydb:"attr:errorCode" json:"error_code,omitempty"`
	ErrorMessage string `theorydb:"attr:errorMessage" json:"error_message,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	RequestID string    `theorydb:"attr:requestId" json:"request_id,omitempty"`
}

func (AIJob) TableName() string { return MainTableName() }

func (j *AIJob) BeforeCreate() error {
	if err := j.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	if j.ExpiresAt.IsZero() {
		j.ExpiresAt = now.Add(24 * time.Hour)
	}
	j.TTL = j.ExpiresAt.Unix()
	if strings.TrimSpace(j.Status) == "" {
		j.Status = AIJobStatusQueued
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 3
	}
	return nil
}

func (j *AIJob) BeforeUpdate() error {
	j.UpdatedAt = time.Now().UTC()
	j.TTL = j.ExpiresAt.Unix()
	return nil
}

func (j *AIJob) UpdateKeys() error {
	j.ID = strings.TrimSpace(j.ID)
	j.InstanceSlug = strings.TrimSpace(j.InstanceSlug)
	j.Module = strings.ToLower(strings.TrimSpace(j.Module))
	j.PolicyVersion = strings.TrimSpace(j.PolicyVersion)
	j.ModelSet = strings.TrimSpace(j.ModelSet)
	j.CacheScope = strings.TrimSpace(j.CacheScope)
	j.ScopeKey = strings.TrimSpace(j.ScopeKey)
	j.InputsHash = strings.TrimSpace(j.InputsHash)
	j.InputsJSON = strings.TrimSpace(j.InputsJSON)
	j.Status = strings.ToLower(strings.TrimSpace(j.Status))

	j.PK = fmt.Sprintf("AIJOB#%s", j.ID)
	j.SK = "JOB"
	j.TTL = j.ExpiresAt.Unix()
	return nil
}

func (j *AIJob) GetPK() string { return j.PK }
func (j *AIJob) GetSK() string { return j.SK }
