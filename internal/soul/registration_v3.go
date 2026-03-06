package soul

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

var (
	regexENSNameEth = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*\.eth$`)
	regexE164       = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
	regexHHMM       = regexp.MustCompile(`^[0-2][0-9]:[0-5][0-9]$`)
	regexLang2      = regexp.MustCompile(`^[a-z]{2}$`)
)

// RegistrationFileV3 is the v3 Soul Registration File schema (lesser-soul/SPEC-v3-draft.md Appendix F).
// v3 is strictly additive over v2.
type RegistrationFileV3 struct {
	Version   string                 `json:"version"`
	AgentID   string                 `json:"agentId"`
	Domain    string                 `json:"domain"`
	LocalID   string                 `json:"localId"`
	Wallet    string                 `json:"wallet"`
	Principal PrincipalDeclarationV2 `json:"principal"`

	SelfDescription SelfDescriptionV2   `json:"selfDescription"`
	Capabilities    []CapabilityV2      `json:"capabilities"`
	Boundaries      []BoundaryV2        `json:"boundaries"`
	Transparency    map[string]any      `json:"transparency"`
	Continuity      []ContinuityEntryV2 `json:"continuity,omitempty"`
	Endpoints       EndpointsV2         `json:"endpoints"`
	Lifecycle       LifecycleV2         `json:"lifecycle"`

	Channels           *ChannelsV3           `json:"channels,omitempty"`
	ContactPreferences *ContactPreferencesV3 `json:"contactPreferences,omitempty"`

	PreviousVersionURI *string `json:"previousVersionUri,omitempty"`
	ChangeSummary      *string `json:"changeSummary,omitempty"`

	Attestations AttestationsV2 `json:"attestations"`
	Created      string         `json:"created"`
	Updated      string         `json:"updated"`
}

type ChannelsV3 struct {
	ENS   *ENSChannelV3   `json:"ens,omitempty"`
	Email *EmailChannelV3 `json:"email,omitempty"`
	Phone *PhoneChannelV3 `json:"phone,omitempty"`
}

type ENSChannelV3 struct {
	Name            string `json:"name"`
	ResolverAddress string `json:"resolverAddress,omitempty"`
	Chain           string `json:"chain,omitempty"`
}

type EmailChannelV3 struct {
	Address      string   `json:"address"`
	Capabilities []string `json:"capabilities"`
	Protocols    []string `json:"protocols,omitempty"`
	Verified     bool     `json:"verified"`
	VerifiedAt   string   `json:"verifiedAt,omitempty"`
}

type PhoneChannelV3 struct {
	Number       string   `json:"number"`
	Capabilities []string `json:"capabilities"`
	Provider     string   `json:"provider,omitempty"`
	Verified     bool     `json:"verified"`
	VerifiedAt   string   `json:"verifiedAt,omitempty"`
}

type ContactPreferencesV3 struct {
	Preferred           string                 `json:"preferred"`
	Fallback            string                 `json:"fallback,omitempty"`
	Availability        ContactAvailabilityV3  `json:"availability"`
	ResponseExpectation ResponseExpectationV3  `json:"responseExpectation"`
	RateLimits          map[string]any         `json:"rateLimits,omitempty"`
	Languages           []string               `json:"languages"`
	ContentTypes        []string               `json:"contentTypes,omitempty"`
	FirstContact        *ContactFirstContactV3 `json:"firstContact,omitempty"`
}

type ContactAvailabilityV3 struct {
	Schedule string                        `json:"schedule"`
	Timezone string                        `json:"timezone,omitempty"`
	Windows  []ContactAvailabilityWindowV3 `json:"windows,omitempty"`
}

type ContactAvailabilityWindowV3 struct {
	Days      []string `json:"days"`
	StartTime string   `json:"startTime"`
	EndTime   string   `json:"endTime"`
}

type ResponseExpectationV3 struct {
	Target    string `json:"target"`
	Guarantee string `json:"guarantee"`
}

type ContactFirstContactV3 struct {
	RequireSoul          bool     `json:"requireSoul,omitempty"`
	RequireReputation    *float64 `json:"requireReputation,omitempty"`
	IntroductionExpected bool     `json:"introductionExpected,omitempty"`
}

func ParseRegistrationFileV3(body []byte) (*RegistrationFileV3, error) {
	if len(body) == 0 {
		return nil, errors.New("registration body is required")
	}
	var reg RegistrationFileV3
	if err := json.Unmarshal(body, &reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

func (r *RegistrationFileV3) Validate() error {
	if r == nil {
		return errRegistrationNil
	}
	if err := validateRegistrationVersion(r.Version, "3"); err != nil {
		return err
	}
	if err := validateRegistrationIdentity(r.AgentID, r.Domain, r.LocalID, r.Wallet); err != nil {
		return err
	}
	if err := r.validateCoreSections(); err != nil {
		return err
	}
	if err := r.Attestations.Validate(); err != nil {
		return fmt.Errorf("attestations: %w", err)
	}
	return validateRegistrationTimestamps(r.Created, r.Updated)
}

func (r *RegistrationFileV3) validateCoreSections() error {
	if err := r.Principal.Validate(); err != nil {
		return fmt.Errorf("principal: %w", err)
	}
	if err := r.SelfDescription.Validate(); err != nil {
		return fmt.Errorf("selfDescription: %w", err)
	}
	if err := validateCapabilitiesV2(r.Capabilities); err != nil {
		return err
	}
	if err := validateBoundariesV2(r.Boundaries); err != nil {
		return err
	}
	if r.Transparency == nil {
		return errors.New("transparency is required")
	}
	if err := r.Endpoints.Validate(); err != nil {
		return fmt.Errorf("endpoints: %w", err)
	}
	if err := r.Lifecycle.Validate(); err != nil {
		return fmt.Errorf("lifecycle: %w", err)
	}
	if err := validateOptionalPreviousVersionURI(r.PreviousVersionURI); err != nil {
		return err
	}
	if err := r.validateOptionalV3Sections(); err != nil {
		return err
	}
	return nil
}

func (r *RegistrationFileV3) validateOptionalV3Sections() error {
	if r.Channels != nil {
		if err := r.Channels.Validate(); err != nil {
			return fmt.Errorf("channels: %w", err)
		}
	}
	if r.ContactPreferences != nil {
		if err := r.ContactPreferences.Validate(); err != nil {
			return fmt.Errorf("contactPreferences: %w", err)
		}
	}
	return nil
}

func (c *ChannelsV3) Validate() error {
	if c == nil {
		return errors.New("is required")
	}
	if c.ENS == nil && c.Email == nil && c.Phone == nil {
		return errors.New("at least one channel is required")
	}
	if c.ENS != nil {
		if err := c.ENS.Validate(); err != nil {
			return fmt.Errorf("ens: %w", err)
		}
	}
	if c.Email != nil {
		if err := c.Email.Validate(); err != nil {
			return fmt.Errorf("email: %w", err)
		}
	}
	if c.Phone != nil {
		if err := c.Phone.Validate(); err != nil {
			return fmt.Errorf("phone: %w", err)
		}
	}
	return nil
}

func (c *ENSChannelV3) Validate() error {
	if c == nil {
		return errors.New("is required")
	}
	name := strings.ToLower(strings.TrimSpace(c.Name))
	if name == "" {
		return errors.New("name is required")
	}
	if !regexENSNameEth.MatchString(name) {
		return errors.New("name is invalid")
	}
	if strings.TrimSpace(c.ResolverAddress) != "" && !common.IsHexAddress(strings.TrimSpace(c.ResolverAddress)) {
		return errors.New("resolverAddress is invalid")
	}
	return nil
}

func (c *EmailChannelV3) Validate() error {
	if c == nil {
		return errors.New("is required")
	}
	addr := strings.TrimSpace(c.Address)
	if addr == "" {
		return errors.New("address is required")
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil || parsed == nil || strings.TrimSpace(parsed.Address) != addr {
		return errors.New("address is invalid")
	}
	if len(c.Capabilities) == 0 {
		return errors.New("capabilities must be a non-empty array")
	}
	for _, cap := range c.Capabilities {
		switch strings.ToLower(strings.TrimSpace(cap)) {
		case "receive", "send":
		default:
			return errors.New("capabilities is invalid")
		}
	}
	for _, p := range c.Protocols {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "smtp", "imap":
		default:
			return errors.New("protocols is invalid")
		}
	}
	if strings.TrimSpace(c.VerifiedAt) != "" {
		if err := validateRFC3339(c.VerifiedAt); err != nil {
			return errors.New("verifiedAt is invalid")
		}
	}
	return nil
}

func (c *PhoneChannelV3) Validate() error {
	if c == nil {
		return errors.New("is required")
	}
	num := strings.TrimSpace(c.Number)
	if num == "" {
		return errors.New("number is required")
	}
	if !regexE164.MatchString(num) {
		return errors.New("number is invalid")
	}
	if len(c.Capabilities) == 0 {
		return errors.New("capabilities must be a non-empty array")
	}
	for _, cap := range c.Capabilities {
		switch strings.ToLower(strings.TrimSpace(cap)) {
		case "sms-receive", "sms-send", "voice-receive", "voice-send":
		default:
			return errors.New("capabilities is invalid")
		}
	}
	if strings.TrimSpace(c.VerifiedAt) != "" {
		if err := validateRFC3339(c.VerifiedAt); err != nil {
			return errors.New("verifiedAt is invalid")
		}
	}
	return nil
}

func (p *ContactPreferencesV3) Validate() error {
	if p == nil {
		return errors.New("is required")
	}
	switch strings.ToLower(strings.TrimSpace(p.Preferred)) {
	case "email", "sms", "voice", "activitypub", "mcp":
	default:
		return errors.New("preferred is invalid")
	}
	if strings.TrimSpace(p.Fallback) != "" {
		switch strings.ToLower(strings.TrimSpace(p.Fallback)) {
		case "email", "sms", "voice", "activitypub", "mcp":
		default:
			return errors.New("fallback is invalid")
		}
	}
	if err := p.Availability.Validate(); err != nil {
		return err
	}
	if err := p.ResponseExpectation.Validate(); err != nil {
		return err
	}
	if len(p.Languages) == 0 {
		return errors.New("languages must be a non-empty array")
	}
	for _, l := range p.Languages {
		if !regexLang2.MatchString(strings.ToLower(strings.TrimSpace(l))) {
			return errors.New("languages is invalid")
		}
	}
	if p.FirstContact != nil {
		if err := p.FirstContact.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (a *ContactAvailabilityV3) Validate() error {
	schedule := strings.ToLower(strings.TrimSpace(a.Schedule))
	switch schedule {
	case "always", "business-hours", "custom":
	default:
		return errors.New("availability.schedule is invalid")
	}
	for i := range a.Windows {
		w := a.Windows[i]
		if err := w.Validate(); err != nil {
			return fmt.Errorf("availability.windows[%d]: %w", i, err)
		}
	}
	return nil
}

func (w *ContactAvailabilityWindowV3) Validate() error {
	if w == nil {
		return errors.New("is required")
	}
	if len(w.Days) == 0 {
		return errors.New("days must be a non-empty array")
	}
	for _, d := range w.Days {
		switch strings.ToLower(strings.TrimSpace(d)) {
		case "mon", "tue", "wed", "thu", "fri", "sat", "sun":
		default:
			return errors.New("days is invalid")
		}
	}
	if !regexHHMM.MatchString(strings.TrimSpace(w.StartTime)) {
		return errors.New("startTime is invalid")
	}
	if !regexHHMM.MatchString(strings.TrimSpace(w.EndTime)) {
		return errors.New("endTime is invalid")
	}
	return nil
}

func (r *ResponseExpectationV3) Validate() error {
	if r == nil {
		return errors.New("is required")
	}
	if strings.TrimSpace(r.Target) == "" {
		return errors.New("target is required")
	}
	switch strings.ToLower(strings.TrimSpace(r.Guarantee)) {
	case "guaranteed", "best-effort":
	default:
		return errors.New("guarantee is invalid")
	}
	return nil
}

func (f *ContactFirstContactV3) Validate() error {
	if f == nil {
		return errors.New("is required")
	}
	if f.RequireReputation != nil {
		if *f.RequireReputation < 0 || *f.RequireReputation > 1 {
			return errors.New("requireReputation is invalid")
		}
	}
	return nil
}
