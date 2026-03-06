package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentContactPreferences stores contact preferences for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: CONTACT_PREFERENCES
type SoulAgentContactPreferences struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id"`

	Preferred string `theorydb:"attr:preferred" json:"preferred"`
	Fallback  string `theorydb:"attr:fallback" json:"fallback,omitempty"`

	AvailabilitySchedule string                          `theorydb:"attr:availabilitySchedule" json:"availability_schedule"`
	AvailabilityTimezone string                          `theorydb:"attr:availabilityTimezone" json:"availability_timezone,omitempty"`
	AvailabilityWindows  []SoulContactAvailabilityWindow `theorydb:"attr:availabilityWindows" json:"availability_windows,omitempty"`

	ResponseTarget    string `theorydb:"attr:responseTarget" json:"response_target,omitempty"`
	ResponseGuarantee string `theorydb:"attr:responseGuarantee" json:"response_guarantee,omitempty"`

	RateLimits map[string]any `theorydb:"attr:rateLimits" json:"rate_limits,omitempty"`

	Languages    []string `theorydb:"attr:languages" json:"languages,omitempty"`
	ContentTypes []string `theorydb:"attr:contentTypes" json:"content_types,omitempty"`

	FirstContactRequireSoul          bool     `theorydb:"attr:firstContactRequireSoul" json:"first_contact_require_soul,omitempty"`
	FirstContactRequireReputation    *float64 `theorydb:"attr:firstContactRequireReputation" json:"first_contact_require_reputation,omitempty"`
	FirstContactIntroductionExpected bool     `theorydb:"attr:firstContactIntroductionExpected" json:"first_contact_introduction_expected,omitempty"`

	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

type SoulContactAvailabilityWindow struct {
	_ struct{} `theorydb:"naming:camelCase"`

	Days      []string `theorydb:"attr:days" json:"days"`
	StartTime string   `theorydb:"attr:startTime" json:"start_time"`
	EndTime   string   `theorydb:"attr:endTime" json:"end_time"`
}

// TableName returns the database table name for SoulAgentContactPreferences.
func (SoulAgentContactPreferences) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentContactPreferences.
func (p *SoulAgentContactPreferences) BeforeCreate() error {
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(p.AvailabilitySchedule) == "" {
		p.AvailabilitySchedule = "always"
	}
	if err := p.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", p.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("preferred", p.Preferred); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates timestamps and keys before updating SoulAgentContactPreferences.
func (p *SoulAgentContactPreferences) BeforeUpdate() error {
	p.UpdatedAt = time.Now().UTC()
	if err := p.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", p.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("preferred", p.Preferred); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentContactPreferences.
func (p *SoulAgentContactPreferences) UpdateKeys() error {
	p.AgentID = strings.ToLower(strings.TrimSpace(p.AgentID))
	p.Preferred = strings.ToLower(strings.TrimSpace(p.Preferred))
	p.Fallback = strings.ToLower(strings.TrimSpace(p.Fallback))
	p.AvailabilitySchedule = strings.ToLower(strings.TrimSpace(p.AvailabilitySchedule))
	p.AvailabilityTimezone = strings.TrimSpace(p.AvailabilityTimezone)
	p.ResponseTarget = strings.TrimSpace(p.ResponseTarget)
	p.ResponseGuarantee = strings.ToLower(strings.TrimSpace(p.ResponseGuarantee))

	p.AvailabilityWindows = normalizeContactAvailabilityWindows(p.AvailabilityWindows)
	p.Languages = normalizeLowerTrimmedStrings(p.Languages)
	p.ContentTypes = normalizeLowerTrimmedStrings(p.ContentTypes)

	if p.FirstContactRequireReputation != nil {
		if *p.FirstContactRequireReputation < 0 || *p.FirstContactRequireReputation > 1 {
			return fmt.Errorf("firstContactRequireReputation must be between 0 and 1")
		}
	}

	p.PK = fmt.Sprintf("SOUL#AGENT#%s", p.AgentID)
	p.SK = "CONTACT_PREFERENCES"
	return nil
}

// GetPK returns the partition key for SoulAgentContactPreferences.
func (p *SoulAgentContactPreferences) GetPK() string { return p.PK }

// GetSK returns the sort key for SoulAgentContactPreferences.
func (p *SoulAgentContactPreferences) GetSK() string { return p.SK }

func normalizeContactAvailabilityWindows(windows []SoulContactAvailabilityWindow) []SoulContactAvailabilityWindow {
	if len(windows) == 0 {
		return nil
	}

	out := make([]SoulContactAvailabilityWindow, 0, len(windows))
	for _, window := range windows {
		out = append(out, SoulContactAvailabilityWindow{
			Days:      normalizeLowerTrimmedStrings(window.Days),
			StartTime: strings.TrimSpace(window.StartTime),
			EndTime:   strings.TrimSpace(window.EndTime),
		})
	}
	return out
}

func normalizeLowerTrimmedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
