package models

import (
	"fmt"
	"strings"
	"time"
)

type LinkSafetyBasicSummary struct {
	_ struct{} `theorydb:"naming:camelCase"`

	TotalLinks int `theorydb:"attr:totalLinks" json:"total_links"`

	LowCount     int `theorydb:"attr:lowCount" json:"low_count"`
	MediumCount  int `theorydb:"attr:mediumCount" json:"medium_count"`
	HighCount    int `theorydb:"attr:highCount" json:"high_count"`
	BlockedCount int `theorydb:"attr:blockedCount" json:"blocked_count"`
	InvalidCount int `theorydb:"attr:invalidCount" json:"invalid_count"`

	OverallRisk string `theorydb:"attr:overallRisk" json:"overall_risk"`
}

type LinkSafetyBasicLinkResult struct {
	_ struct{} `theorydb:"naming:camelCase"`

	URL           string   `theorydb:"attr:url" json:"url"`
	NormalizedURL string   `theorydb:"attr:normalizedUrl" json:"normalized_url,omitempty"`
	Host          string   `theorydb:"attr:host" json:"host,omitempty"`
	Flags         []string `theorydb:"attr:flags" json:"flags,omitempty"`

	Risk string `theorydb:"attr:risk" json:"risk"`

	ErrorCode    string `theorydb:"attr:errorCode" json:"error_code,omitempty"`
	ErrorMessage string `theorydb:"attr:errorMessage" json:"error_message,omitempty"`
}

type LinkSafetyBasicResult struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID            string `theorydb:"attr:id" json:"id"`
	PolicyVersion string `theorydb:"attr:policyVersion" json:"policy_version"`

	ActorURI    string `theorydb:"attr:actorUri" json:"actor_uri,omitempty"`
	ObjectURI   string `theorydb:"attr:objectUri" json:"object_uri,omitempty"`
	ContentHash string `theorydb:"attr:contentHash" json:"content_hash,omitempty"`
	LinksHash   string `theorydb:"attr:linksHash" json:"links_hash"`

	Links   []LinkSafetyBasicLinkResult `theorydb:"attr:links" json:"links"`
	Summary LinkSafetyBasicSummary      `theorydb:"attr:summary" json:"summary"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	RequestID string    `theorydb:"attr:requestId" json:"request_id,omitempty"`
}

func (LinkSafetyBasicResult) TableName() string { return MainTableName() }

func (r *LinkSafetyBasicResult) BeforeCreate() error {
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

func (r *LinkSafetyBasicResult) UpdateKeys() error {
	r.ID = strings.TrimSpace(r.ID)
	r.PK = fmt.Sprintf("LINK_SAFETY_BASIC#%s", r.ID)
	r.SK = "RESULT"
	r.TTL = r.ExpiresAt.Unix()
	return nil
}

func (r *LinkSafetyBasicResult) GetPK() string { return r.PK }
func (r *LinkSafetyBasicResult) GetSK() string { return r.SK }
