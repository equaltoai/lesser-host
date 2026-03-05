package controlplane

import (
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

var (
	soulENSNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*\.eth\.?$`)
	soulE164Regex    = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
)

type soulPublicAgentChannelsResponse struct {
	AgentID            string                        `json:"agentId"`
	Channels           soulPublicAgentChannelsObject `json:"channels"`
	ContactPreferences *soul.ContactPreferencesV3    `json:"contactPreferences"`
	UpdatedAt          string                        `json:"updatedAt"`
}

type soulPublicAgentChannelsObject struct {
	ENS   *soulPublicENSChannel   `json:"ens"`
	Email *soulPublicEmailChannel `json:"email"`
	Phone *soulPublicPhoneChannel `json:"phone"`
}

type soulPublicENSChannel struct {
	Name            string `json:"name"`
	ResolverAddress string `json:"resolverAddress,omitempty"`
	Chain           string `json:"chain,omitempty"`
}

type soulPublicEmailChannel struct {
	Address      string   `json:"address"`
	Capabilities []string `json:"capabilities"`
	Protocols    []string `json:"protocols,omitempty"`
	Verified     bool     `json:"verified"`
	VerifiedAt   string   `json:"verifiedAt,omitempty"`
	Status       string   `json:"status,omitempty"`
}

type soulPublicPhoneChannel struct {
	Number       string   `json:"number"`
	Capabilities []string `json:"capabilities"`
	Provider     string   `json:"provider,omitempty"`
	Verified     bool     `json:"verified"`
	VerifiedAt   string   `json:"verifiedAt,omitempty"`
	Status       string   `json:"status,omitempty"`
}

type soulPublicAgentContactPreferencesResponse struct {
	AgentID            string                     `json:"agentId"`
	ContactPreferences *soul.ContactPreferencesV3 `json:"contactPreferences"`
	UpdatedAt          string                     `json:"updatedAt"`
}

func (s *Server) handleSoulPublicGetAgentChannels(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	prefs, err := getSoulAgentItemBySK[models.SoulAgentContactPreferences](s, ctx.Context(), agentIDHex, "CONTACT_PREFERENCES")
	if theoryErrors.IsNotFound(err) {
		prefs = nil
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	ens, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx.Context(), agentIDHex, "CHANNEL#ens")
	if theoryErrors.IsNotFound(err) {
		ens = nil
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	email, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx.Context(), agentIDHex, "CHANNEL#email")
	if theoryErrors.IsNotFound(err) {
		email = nil
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	phone, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx.Context(), agentIDHex, "CHANNEL#phone")
	if theoryErrors.IsNotFound(err) {
		phone = nil
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	updatedAt := identity.UpdatedAt
	if prefs != nil && prefs.UpdatedAt.After(updatedAt) {
		updatedAt = prefs.UpdatedAt
	}
	if ens != nil && ens.UpdatedAt.After(updatedAt) {
		updatedAt = ens.UpdatedAt
	}
	if email != nil && email.UpdatedAt.After(updatedAt) {
		updatedAt = email.UpdatedAt
	}
	if phone != nil && phone.UpdatedAt.After(updatedAt) {
		updatedAt = phone.UpdatedAt
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	out := soulPublicAgentChannelsResponse{
		AgentID: agentIDHex,
		Channels: soulPublicAgentChannelsObject{
			ENS:   soulPublicENSChannelFromModel(ens),
			Email: soulPublicEmailChannelFromModel(email),
			Phone: soulPublicPhoneChannelFromModel(phone),
		},
		ContactPreferences: soulPublicContactPreferencesFromModel(prefs),
		UpdatedAt:          updatedAt.UTC().Format(time.RFC3339Nano),
	}

	resp, err := apptheory.JSON(http.StatusOK, out)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

func soulPublicENSChannelFromModel(c *models.SoulAgentChannel) *soulPublicENSChannel {
	if c == nil || strings.TrimSpace(c.Identifier) == "" {
		return nil
	}
	return &soulPublicENSChannel{
		Name:            strings.TrimSpace(c.Identifier),
		ResolverAddress: strings.TrimSpace(c.ENSResolverAddress),
		Chain:           strings.TrimSpace(c.ENSChain),
	}
}

func soulPublicEmailChannelFromModel(c *models.SoulAgentChannel) *soulPublicEmailChannel {
	if c == nil || strings.TrimSpace(c.Identifier) == "" {
		return nil
	}
	out := &soulPublicEmailChannel{
		Address:      strings.TrimSpace(c.Identifier),
		Capabilities: c.Capabilities,
		Protocols:    c.Protocols,
		Verified:     c.Verified,
		Status:       strings.TrimSpace(c.Status),
	}
	if !c.VerifiedAt.IsZero() {
		out.VerifiedAt = c.VerifiedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func soulPublicPhoneChannelFromModel(c *models.SoulAgentChannel) *soulPublicPhoneChannel {
	if c == nil || strings.TrimSpace(c.Identifier) == "" {
		return nil
	}
	out := &soulPublicPhoneChannel{
		Number:       strings.TrimSpace(c.Identifier),
		Capabilities: c.Capabilities,
		Provider:     strings.TrimSpace(c.Provider),
		Verified:     c.Verified,
		Status:       strings.TrimSpace(c.Status),
	}
	if !c.VerifiedAt.IsZero() {
		out.VerifiedAt = c.VerifiedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func soulPublicContactPreferencesFromModel(p *models.SoulAgentContactPreferences) *soul.ContactPreferencesV3 {
	if p == nil || strings.TrimSpace(p.Preferred) == "" {
		return nil
	}

	windows := make([]soul.ContactAvailabilityWindowV3, 0, len(p.AvailabilityWindows))
	for _, w := range p.AvailabilityWindows {
		windows = append(windows, soul.ContactAvailabilityWindowV3{
			Days:      w.Days,
			StartTime: strings.TrimSpace(w.StartTime),
			EndTime:   strings.TrimSpace(w.EndTime),
		})
	}

	cp := &soul.ContactPreferencesV3{
		Preferred: strings.TrimSpace(p.Preferred),
		Fallback:  strings.TrimSpace(p.Fallback),
		Availability: soul.ContactAvailabilityV3{
			Schedule: strings.TrimSpace(p.AvailabilitySchedule),
			Timezone: strings.TrimSpace(p.AvailabilityTimezone),
			Windows:  windows,
		},
		ResponseExpectation: soul.ResponseExpectationV3{
			Target:    strings.TrimSpace(p.ResponseTarget),
			Guarantee: strings.TrimSpace(p.ResponseGuarantee),
		},
		RateLimits:   p.RateLimits,
		Languages:    p.Languages,
		ContentTypes: p.ContentTypes,
	}

	if p.FirstContactRequireSoul || p.FirstContactRequireReputation != nil || p.FirstContactIntroductionExpected {
		cp.FirstContact = &soul.ContactFirstContactV3{
			RequireSoul:          p.FirstContactRequireSoul,
			RequireReputation:    p.FirstContactRequireReputation,
			IntroductionExpected: p.FirstContactIntroductionExpected,
		}
	}

	return cp
}

func (s *Server) handleSoulPublicGetAgentChannelPreferences(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	prefs, err := getSoulAgentItemBySK[models.SoulAgentContactPreferences](s, ctx.Context(), agentIDHex, "CONTACT_PREFERENCES")
	if theoryErrors.IsNotFound(err) {
		prefs = nil
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	updatedAt := identity.UpdatedAt
	if prefs != nil && prefs.UpdatedAt.After(updatedAt) {
		updatedAt = prefs.UpdatedAt
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	out := soulPublicAgentContactPreferencesResponse{
		AgentID:            agentIDHex,
		ContactPreferences: soulPublicContactPreferencesFromModel(prefs),
		UpdatedAt:          updatedAt.UTC().Format(time.RFC3339Nano),
	}

	resp, err := apptheory.JSON(http.StatusOK, out)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

func (s *Server) handleSoulPublicResolveENSName(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	raw, _ := url.PathUnescape(strings.TrimSpace(ctx.Param("ensName")))
	if raw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "ensName is required"}
	}
	if !soulENSNameRegex.MatchString(strings.ToLower(raw)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "ensName is invalid"}
	}

	key := &models.SoulAgentENSResolution{ENSName: raw}
	_ = key.UpdateKeys()

	var item models.SoulAgentENSResolution
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentENSResolution{}).
		Where("PK", "=", key.PK).
		Where("SK", "=", "RESOLUTION").
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.AgentID) == "" {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), item.AgentID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	resp, err := apptheory.JSON(http.StatusOK, soulPublicAgentResponse{Version: "1", Agent: *identity})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

func (s *Server) handleSoulPublicResolveEmail(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	raw, _ := url.PathUnescape(strings.TrimSpace(ctx.Param("emailAddress")))
	if raw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "emailAddress is required"}
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr == nil || strings.TrimSpace(addr.Address) == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "emailAddress is invalid"}
	}

	idx := &models.SoulEmailAgentIndex{Email: addr.Address}
	_ = idx.UpdateKeys()

	var item models.SoulEmailAgentIndex
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulEmailAgentIndex{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", "AGENT").
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.AgentID) == "" {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), item.AgentID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	resp, err := apptheory.JSON(http.StatusOK, soulPublicAgentResponse{Version: "1", Agent: *identity})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

func (s *Server) handleSoulPublicResolvePhone(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	raw, _ := url.PathUnescape(strings.TrimSpace(ctx.Param("phoneNumber")))
	if raw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "phoneNumber is required"}
	}

	idx := &models.SoulPhoneAgentIndex{Phone: raw}
	_ = idx.UpdateKeys()

	// Validate normalized E.164 form (required for stable reverse lookup keys).
	if !soulE164Regex.MatchString(strings.TrimSpace(idx.Phone)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "phoneNumber is invalid"}
	}

	var item models.SoulPhoneAgentIndex
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulPhoneAgentIndex{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", "AGENT").
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.AgentID) == "" {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), item.AgentID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	resp, err := apptheory.JSON(http.StatusOK, soulPublicAgentResponse{Version: "1", Agent: *identity})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}
