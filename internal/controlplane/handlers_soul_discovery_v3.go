package controlplane

import (
	"context"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

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
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, newAppTheoryError("app.not_found", "not found")
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	prefs, ens, email, phone, appErr := s.loadSoulAgentChannelRecords(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	updatedAt := latestSoulChannelUpdate(identity.UpdatedAt, prefs, ens, email, phone)

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
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	setSoulPrivateNoStoreHeaders(resp)
	return resp, nil
}

func (s *Server) loadSoulAgentChannelRecords(
	ctx *apptheory.Context,
	agentIDHex string,
) (
	prefs *models.SoulAgentContactPreferences,
	ens *models.SoulAgentChannel,
	email *models.SoulAgentChannel,
	phone *models.SoulAgentChannel,
	appErr *apptheory.AppTheoryError,
) {
	prefs, appErr = loadSoulOptionalItem[models.SoulAgentContactPreferences](s, ctx, agentIDHex, "CONTACT_PREFERENCES")
	if appErr != nil {
		return nil, nil, nil, nil, appErr
	}
	ens, appErr = loadSoulOptionalItem[models.SoulAgentChannel](s, ctx, agentIDHex, "CHANNEL#ens")
	if appErr != nil {
		return nil, nil, nil, nil, appErr
	}
	email, appErr = loadSoulOptionalItem[models.SoulAgentChannel](s, ctx, agentIDHex, "CHANNEL#email")
	if appErr != nil {
		return nil, nil, nil, nil, appErr
	}
	phone, appErr = loadSoulOptionalItem[models.SoulAgentChannel](s, ctx, agentIDHex, "CHANNEL#phone")
	if appErr != nil {
		return nil, nil, nil, nil, appErr
	}
	return prefs, ens, email, phone, nil
}

func loadSoulOptionalItem[T any](s *Server, ctx *apptheory.Context, agentIDHex string, sk string) (*T, *apptheory.AppTheoryError) {
	item, err := getSoulAgentItemBySK[T](s, ctx.Context(), agentIDHex, sk)
	if theoryErrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	return item, nil
}

func latestSoulChannelUpdate(
	base time.Time,
	prefs *models.SoulAgentContactPreferences,
	ens *models.SoulAgentChannel,
	email *models.SoulAgentChannel,
	phone *models.SoulAgentChannel,
) time.Time {
	updatedAt := base
	for _, ts := range []time.Time{
		soulOptionalUpdatedAt(prefs),
		soulOptionalUpdatedAt(ens),
		soulOptionalUpdatedAt(email),
		soulOptionalUpdatedAt(phone),
	} {
		if ts.After(updatedAt) {
			updatedAt = ts
		}
	}
	if updatedAt.IsZero() {
		return time.Now().UTC()
	}
	return updatedAt
}

func soulOptionalUpdatedAt(item any) time.Time {
	switch value := item.(type) {
	case *models.SoulAgentContactPreferences:
		if value != nil {
			return value.UpdatedAt
		}
	case *models.SoulAgentChannel:
		if value != nil {
			return value.UpdatedAt
		}
	}
	return time.Time{}
}

func soulPublicENSChannelFromModel(c *models.SoulAgentChannel) *soulPublicENSChannel {
	if c == nil || strings.TrimSpace(c.Identifier) == "" {
		return nil
	}
	if soul.IsLegacyBareManagedENSName(c.Identifier) {
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

func soulAgentContactPreferencesResponse(
	agentID string,
	baseUpdatedAt time.Time,
	prefs *models.SoulAgentContactPreferences,
) soulPublicAgentContactPreferencesResponse {
	updatedAt := baseUpdatedAt
	if prefs != nil && prefs.UpdatedAt.After(updatedAt) {
		updatedAt = prefs.UpdatedAt
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	return soulPublicAgentContactPreferencesResponse{
		AgentID:            agentID,
		ContactPreferences: soulPublicContactPreferencesFromModel(prefs),
		UpdatedAt:          updatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (s *Server) handleSoulPublicGetAgentChannelPreferences(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, newAppTheoryError("app.not_found", "not found")
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	prefs, err := getSoulAgentItemBySK[models.SoulAgentContactPreferences](s, ctx.Context(), agentIDHex, "CONTACT_PREFERENCES")
	if theoryErrors.IsNotFound(err) {
		prefs = nil
	} else if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	out := soulAgentContactPreferencesResponse(agentIDHex, identity.UpdatedAt, prefs)

	resp, err := apptheory.JSON(http.StatusOK, out)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	setSoulPrivateNoStoreHeaders(resp)
	return resp, nil
}

func (s *Server) handleSoulPublicResolveENSName(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, newAppTheoryError("app.not_found", "not found")
	}

	raw, appErr := normalizeSoulPublicResolveENSName(ctx.Param("ensName"))
	if appErr != nil {
		return nil, appErr
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
		return nil, newAppTheoryError("app.not_found", "not found")
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if strings.TrimSpace(item.AgentID) == "" {
		return nil, newAppTheoryError("app.not_found", "not found")
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), item.AgentID)
	if theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.not_found", "not found")
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if !soulIdentityPubliclyResolvable(identity) {
		return nil, newAppTheoryError("app.not_found", "not found")
	}
	if appErr := s.requirePublicResolvableSoulChannel(ctx, strings.TrimSpace(item.AgentID), models.SoulChannelTypeENS, raw); appErr != nil {
		return nil, appErr
	}

	resp, err := apptheory.JSON(http.StatusOK, soulPublicAgentResponse{Version: "1", Agent: s.buildSoulPublicAgentView(ctx.Context(), identity)})
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

func normalizeSoulPublicResolveENSName(rawParam string) (string, *apptheory.AppTheoryError) {
	raw, _ := url.PathUnescape(strings.TrimSpace(rawParam))
	if raw == "" {
		return "", newAppTheoryError(appErrCodeBadRequest, "ensName is required")
	}
	if !soulENSNameRegex.MatchString(strings.ToLower(raw)) {
		return "", newAppTheoryError(appErrCodeBadRequest, "ensName is invalid")
	}
	if soul.IsLegacyBareManagedENSName(raw) {
		return "", newAppTheoryError("app.not_found", "not found")
	}
	return raw, nil
}

func (s *Server) handleSoulPublicResolveEmail(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if appErr := s.requireSoulPrivateReachabilityPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	emailAddress, appErr := soulResolveEmailParam(ctx)
	if appErr != nil {
		return nil, appErr
	}
	agentID, appErr := s.lookupSoulEmailAgentID(ctx.Context(), emailAddress)
	if appErr != nil {
		return nil, appErr
	}
	return s.soulPrivateResolveContact(ctx, agentID, models.SoulChannelTypeEmail, emailAddress)
}

func soulResolveEmailParam(ctx *apptheory.Context) (string, *apptheory.AppTheoryError) {
	raw, _ := url.PathUnescape(strings.TrimSpace(ctx.Param("emailAddress")))
	if raw == "" {
		return "", newAppTheoryError(appErrCodeBadRequest, "emailAddress is required")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr == nil || strings.TrimSpace(addr.Address) == "" {
		return "", newAppTheoryError(appErrCodeBadRequest, "emailAddress is invalid")
	}
	return strings.TrimSpace(addr.Address), nil
}

func (s *Server) lookupSoulEmailAgentID(ctx context.Context, emailAddress string) (string, *apptheory.AppTheoryError) {
	idx := &models.SoulEmailAgentIndex{Email: emailAddress}
	_ = idx.UpdateKeys()
	return s.lookupSoulContactAgentID(ctx, idx, idx.PK, func() string { return idx.AgentID })
}

func (s *Server) handleSoulPublicResolvePhone(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if appErr := s.requireSoulPrivateReachabilityPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	phoneNumber, appErr := soulResolvePhoneParam(ctx)
	if appErr != nil {
		return nil, appErr
	}
	agentID, appErr := s.lookupSoulPhoneAgentID(ctx.Context(), phoneNumber)
	if appErr != nil {
		return nil, appErr
	}
	return s.soulPrivateResolveContact(ctx, agentID, models.SoulChannelTypePhone, phoneNumber)
}

func (s *Server) requireSoulPrivateReachabilityPrereqs(ctx *apptheory.Context) *apptheory.AppTheoryError {
	if appErr := requireStoreDB(s); appErr != nil {
		return appErr
	}
	if !s.cfg.SoulEnabled {
		return newAppTheoryError("app.not_found", "not found")
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return appErr
	}
	return nil
}

func (s *Server) soulPrivateResolveContact(
	ctx *apptheory.Context,
	agentID string,
	channelType string,
	identifier string,
) (*apptheory.Response, error) {
	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentID)
	if theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.not_found", "not found")
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if !soulIdentityPubliclyResolvable(identity) {
		return nil, newAppTheoryError("app.not_found", "not found")
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, soulReachabilityAccessError(accessErr)
	}
	if appErr := s.requirePublicResolvableSoulChannel(ctx, strings.TrimSpace(agentID), channelType, identifier); appErr != nil {
		return nil, appErr
	}

	resp, err := apptheory.JSON(http.StatusOK, soulPublicAgentResponse{Version: "1", Agent: s.buildSoulPublicAgentView(ctx.Context(), identity)})
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	setSoulPrivateNoStoreHeaders(resp)
	return resp, nil
}

func (s *Server) lookupSoulPhoneAgentID(ctx context.Context, phoneNumber string) (string, *apptheory.AppTheoryError) {
	idx := &models.SoulPhoneAgentIndex{Phone: phoneNumber}
	_ = idx.UpdateKeys()
	return s.lookupSoulContactAgentID(ctx, idx, idx.PK, func() string { return idx.AgentID })
}

func (s *Server) lookupSoulContactAgentID(
	ctx context.Context,
	item any,
	pk string,
	agentID func() string,
) (string, *apptheory.AppTheoryError) {
	err := s.store.DB.WithContext(ctx).
		Model(item).
		Where("PK", "=", pk).
		Where("SK", "=", "AGENT").
		First(item)
	if theoryErrors.IsNotFound(err) {
		return "", newAppTheoryError("app.not_found", "not found")
	}
	if err != nil {
		return "", newAppTheoryError("app.internal", "internal error")
	}
	id := strings.TrimSpace(agentID())
	if id == "" {
		return "", newAppTheoryError("app.not_found", "not found")
	}
	return id, nil
}

func setSoulPrivateNoStoreHeaders(resp *apptheory.Response) {
	if resp == nil {
		return
	}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	resp.Headers["cache-control"] = []string{cacheControlNoStore}
	resp.Headers["pragma"] = []string{"no-cache"}
}

func soulReachabilityAccessError(appErr *apptheory.AppTheoryError) *apptheory.AppTheoryError {
	if appErr == nil {
		return nil
	}
	switch appErr.Code {
	case appErrCodeUnauthorized, appErrCodeForbidden, appErrCodeBadRequest:
		return newAppTheoryError("app.not_found", "not found")
	default:
		return appErr
	}
}

func soulResolvePhoneParam(ctx *apptheory.Context) (string, *apptheory.AppTheoryError) {
	raw, _ := url.PathUnescape(strings.TrimSpace(ctx.Param("phoneNumber")))
	if raw == "" {
		return "", newAppTheoryError(appErrCodeBadRequest, "phoneNumber is required")
	}

	idx := &models.SoulPhoneAgentIndex{Phone: raw}
	_ = idx.UpdateKeys()

	// Validate normalized E.164 form (required for stable reverse lookup keys).
	if !soulE164Regex.MatchString(strings.TrimSpace(idx.Phone)) {
		return "", newAppTheoryError(appErrCodeBadRequest, "phoneNumber is invalid")
	}
	return idx.Phone, nil
}

func soulIdentityPubliclyResolvable(identity *models.SoulAgentIdentity) bool {
	if identity == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(identity.LifecycleStatus))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(identity.Status))
	}
	return status == models.SoulAgentStatusActive
}

func (s *Server) requirePublicResolvableSoulChannel(ctx *apptheory.Context, agentIDHex string, channelType string, identifier string) *apptheory.AppTheoryError {
	ch, appErr := loadSoulOptionalItem[models.SoulAgentChannel](s, ctx, agentIDHex, "CHANNEL#"+strings.ToLower(strings.TrimSpace(channelType)))
	if appErr != nil {
		return appErr
	}
	if ch == nil {
		return newAppTheoryError("app.not_found", "not found")
	}
	chCopy := *ch
	_ = chCopy.UpdateKeys()
	switch strings.ToLower(strings.TrimSpace(channelType)) {
	case models.SoulChannelTypeEmail, models.SoulChannelTypePhone:
		if !trustedManagedSoulChannelForIndex(&chCopy) {
			return newAppTheoryError("app.not_found", "not found")
		}
	case models.SoulChannelTypeENS:
		if soul.IsLegacyBareManagedENSName(identifier) || soul.IsLegacyBareManagedENSName(chCopy.Identifier) {
			return newAppTheoryError("app.not_found", "not found")
		}
		if chCopy.Status != models.SoulChannelStatusActive {
			return newAppTheoryError("app.not_found", "not found")
		}
	default:
		return newAppTheoryError("app.not_found", "not found")
	}
	want := strings.TrimSpace(identifier)
	switch chCopy.ChannelType {
	case models.SoulChannelTypeEmail:
		addr, err := mail.ParseAddress(want)
		if err != nil || addr == nil {
			return newAppTheoryError("app.not_found", "not found")
		}
		want = strings.ToLower(strings.TrimSpace(addr.Address))
	case models.SoulChannelTypePhone:
		phoneIdx := &models.SoulPhoneAgentIndex{Phone: identifier}
		_ = phoneIdx.UpdateKeys()
		want = phoneIdx.Phone
	case models.SoulChannelTypeENS:
		ensIdx := &models.SoulAgentENSResolution{ENSName: identifier}
		_ = ensIdx.UpdateKeys()
		want = ensIdx.ENSName
	}
	if !strings.EqualFold(strings.TrimSpace(chCopy.Identifier), want) {
		return newAppTheoryError("app.not_found", "not found")
	}
	return nil
}
