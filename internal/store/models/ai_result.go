package models

import (
	"fmt"
	"strings"
	"time"
)

type AIResult struct {
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

	ResultJSON string    `theorydb:"attr:resultJson" json:"result_json"`
	Usage      AIUsage   `theorydb:"attr:usage" json:"usage,omitempty"`
	Errors     []AIError `theorydb:"attr:errors" json:"errors,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`

	JobID     string `theorydb:"attr:jobId" json:"job_id,omitempty"`
	RequestID string `theorydb:"attr:requestId" json:"request_id,omitempty"`
}

func (AIResult) TableName() string { return MainTableName() }

func (r *AIResult) BeforeCreate() error {
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.ExpiresAt.IsZero() {
		r.ExpiresAt = now.Add(7 * 24 * time.Hour)
	}
	r.TTL = r.ExpiresAt.Unix()
	return nil
}

func (r *AIResult) UpdateKeys() error {
	r.ID = strings.TrimSpace(r.ID)
	r.InstanceSlug = strings.TrimSpace(r.InstanceSlug)
	r.Module = strings.ToLower(strings.TrimSpace(r.Module))
	r.PolicyVersion = strings.TrimSpace(r.PolicyVersion)
	r.ModelSet = strings.TrimSpace(r.ModelSet)
	r.CacheScope = strings.TrimSpace(r.CacheScope)
	r.ScopeKey = strings.TrimSpace(r.ScopeKey)
	r.InputsHash = strings.TrimSpace(r.InputsHash)
	r.ResultJSON = strings.TrimSpace(r.ResultJSON)
	r.JobID = strings.TrimSpace(r.JobID)
	r.RequestID = strings.TrimSpace(r.RequestID)

	r.PK = fmt.Sprintf("AIRESULT#%s", r.ID)
	r.SK = "RESULT"
	r.TTL = r.ExpiresAt.Unix()
	return nil
}

func (r *AIResult) GetPK() string { return r.PK }
func (r *AIResult) GetSK() string { return r.SK }
