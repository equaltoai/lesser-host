package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentVersion stores a version record for a soul agent's registration file.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: VERSION#{versionNumber}
type SoulAgentVersion struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID                    string `theorydb:"attr:agentId" json:"agent_id"`
	VersionNumber              int    `theorydb:"attr:versionNumber" json:"version_number"`
	RegistrationUri            string `theorydb:"attr:registrationUri" json:"registration_uri"`
	RegistrationSHA256         string `theorydb:"attr:registrationSha256" json:"registration_sha256,omitempty"`
	PreviousRegistrationSHA256 string `theorydb:"attr:previousRegistrationSha256" json:"previous_registration_sha256,omitempty"`
	ChangeSummary              string `theorydb:"attr:changeSummary" json:"change_summary,omitempty"`
	SelfAttestation            string `theorydb:"attr:selfAttestation" json:"self_attestation,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for SoulAgentVersion.
func (SoulAgentVersion) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentVersion.
func (v *SoulAgentVersion) BeforeCreate() error {
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if err := v.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", v.AgentID); err != nil {
		return err
	}
	if err := requirePositiveInt("versionNumber", v.VersionNumber); err != nil {
		return err
	}
	if err := requireNonEmpty("registrationUri", v.RegistrationUri); err != nil {
		return err
	}
	if strings.TrimSpace(v.RegistrationSHA256) != "" {
		// Not strictly required, but if present it must look like a SHA256 hex digest.
		if len(strings.TrimSpace(v.RegistrationSHA256)) != 64 {
			return fmt.Errorf("registrationSha256 is invalid")
		}
	}
	if strings.TrimSpace(v.PreviousRegistrationSHA256) != "" {
		if len(strings.TrimSpace(v.PreviousRegistrationSHA256)) != 64 {
			return fmt.Errorf("previousRegistrationSha256 is invalid")
		}
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentVersion.
func (v *SoulAgentVersion) UpdateKeys() error {
	v.AgentID = strings.ToLower(strings.TrimSpace(v.AgentID))
	v.RegistrationUri = strings.TrimSpace(v.RegistrationUri)
	v.RegistrationSHA256 = strings.ToLower(strings.TrimSpace(v.RegistrationSHA256))
	v.PreviousRegistrationSHA256 = strings.ToLower(strings.TrimSpace(v.PreviousRegistrationSHA256))
	v.ChangeSummary = strings.TrimSpace(v.ChangeSummary)
	v.SelfAttestation = strings.TrimSpace(v.SelfAttestation)

	v.PK = fmt.Sprintf("SOUL#AGENT#%s", v.AgentID)
	v.SK = fmt.Sprintf("VERSION#%d", v.VersionNumber)
	return nil
}

// GetPK returns the partition key for SoulAgentVersion.
func (v *SoulAgentVersion) GetPK() string { return v.PK }

// GetSK returns the sort key for SoulAgentVersion.
func (v *SoulAgentVersion) GetSK() string { return v.SK }
