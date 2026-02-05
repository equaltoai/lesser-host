package models

import (
	"fmt"
	"strings"
	"time"
)

// Attestation stores a signed attestation for an AI module output.
type Attestation struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID string `theorydb:"attr:id" json:"id"`

	ActorURI    string `theorydb:"attr:actorUri" json:"actor_uri,omitempty"`
	ObjectURI   string `theorydb:"attr:objectUri" json:"object_uri,omitempty"`
	ContentHash string `theorydb:"attr:contentHash" json:"content_hash,omitempty"`

	Module        string `theorydb:"attr:module" json:"module"`
	PolicyVersion string `theorydb:"attr:policyVersion" json:"policy_version"`
	ModelSet      string `theorydb:"attr:modelSet" json:"model_set,omitempty"`

	JWS string `theorydb:"attr:jws" json:"jws"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
}

// TableName returns the database table name for Attestation.
func (Attestation) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating Attestation.
func (a *Attestation) BeforeCreate() error {
	if err := a.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	if a.ExpiresAt.IsZero() {
		a.ExpiresAt = now.Add(7 * 24 * time.Hour)
	}
	a.TTL = a.ExpiresAt.Unix()
	return nil
}

// UpdateKeys updates the database keys for Attestation.
func (a *Attestation) UpdateKeys() error {
	a.ID = strings.TrimSpace(a.ID)
	a.ActorURI = strings.TrimSpace(a.ActorURI)
	a.ObjectURI = strings.TrimSpace(a.ObjectURI)
	a.ContentHash = strings.TrimSpace(a.ContentHash)

	a.Module = strings.ToLower(strings.TrimSpace(a.Module))
	a.PolicyVersion = strings.TrimSpace(a.PolicyVersion)
	a.ModelSet = strings.TrimSpace(a.ModelSet)
	a.JWS = strings.TrimSpace(a.JWS)

	a.PK = fmt.Sprintf("ATTESTATION#%s", a.ID)
	a.SK = "ATTESTATION"
	a.TTL = a.ExpiresAt.Unix()
	return nil
}

// GetPK returns the partition key for Attestation.
func (a *Attestation) GetPK() string { return a.PK }

// GetSK returns the sort key for Attestation.
func (a *Attestation) GetSK() string { return a.SK }
