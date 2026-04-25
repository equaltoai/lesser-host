package controlplane

import (
	"context"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulCommContactabilityResponse struct {
	InstanceSlug string                             `json:"instanceSlug"`
	AgentID      string                             `json:"agentId"`
	Contactable  bool                               `json:"contactable"`
	Preferred    string                             `json:"preferred,omitempty"`
	Fallback     string                             `json:"fallback,omitempty"`
	Channels     []soulCommContactabilityChannel    `json:"channels"`
	Mailbox      soulCommContactabilityMailbox      `json:"mailbox"`
	Availability soulCommContactabilityAvailability `json:"availability"`
	FirstContact soulCommContactabilityFirstContact `json:"firstContact"`
	UpdatedAt    string                             `json:"updatedAt"`
}

type soulCommContactabilityChannel struct {
	ChannelType    string   `json:"channelType"`
	Address        string   `json:"address,omitempty"`
	Number         string   `json:"number,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Capabilities   []string `json:"capabilities"`
	Protocols      []string `json:"protocols,omitempty"`
	Verified       bool     `json:"verified"`
	Status         string   `json:"status"`
	ReceiveAllowed bool     `json:"receiveAllowed"`
	SendAllowed    bool     `json:"sendAllowed"`
}

type soulCommContactabilityMailbox struct {
	ListAllowed    bool `json:"listAllowed"`
	GetAllowed     bool `json:"getAllowed"`
	ContentAllowed bool `json:"contentAllowed"`
	StateAllowed   bool `json:"stateAllowed"`
}

type soulCommContactabilityAvailability struct {
	Schedule string                                     `json:"schedule"`
	Timezone string                                     `json:"timezone,omitempty"`
	Windows  []soulCommContactabilityAvailabilityWindow `json:"windows,omitempty"`
}

type soulCommContactabilityAvailabilityWindow struct {
	Days      []string `json:"days"`
	StartTime string   `json:"startTime"`
	EndTime   string   `json:"endTime"`
}

type soulCommContactabilityFirstContact struct {
	RequireSoul          bool     `json:"requireSoul"`
	RequireReputation    *float64 `json:"requireReputation"`
	IntroductionExpected bool     `json:"introductionExpected"`
}

func (s *Server) handleSoulCommContactability(ctx *apptheory.Context) (*apptheory.Response, error) {
	reqCtx, appErr := s.requireMailboxRequestContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	prefs, email, phone, appErr := s.loadSoulCommContactabilityRecords(ctx.Context(), reqCtx.agentID)
	if appErr != nil {
		return nil, appErr
	}

	resp := buildSoulCommContactabilityResponse(reqCtx, prefs, email, phone)
	return apptheory.JSON(http.StatusOK, resp)
}

func (s *Server) loadSoulCommContactabilityRecords(
	ctx context.Context,
	agentID string,
) (
	prefs *models.SoulAgentContactPreferences,
	email *models.SoulAgentChannel,
	phone *models.SoulAgentChannel,
	appErr *apptheory.AppTheoryError,
) {
	prefs, appErr = loadOptionalSoulCommContactabilityItem[models.SoulAgentContactPreferences](s, ctx, agentID, "CONTACT_PREFERENCES")
	if appErr != nil {
		return nil, nil, nil, appErr
	}
	email, appErr = loadOptionalSoulCommContactabilityItem[models.SoulAgentChannel](s, ctx, agentID, "CHANNEL#email")
	if appErr != nil {
		return nil, nil, nil, appErr
	}
	phone, appErr = loadOptionalSoulCommContactabilityItem[models.SoulAgentChannel](s, ctx, agentID, "CHANNEL#phone")
	if appErr != nil {
		return nil, nil, nil, appErr
	}
	return prefs, email, phone, nil
}

func loadOptionalSoulCommContactabilityItem[T any](
	s *Server,
	ctx context.Context,
	agentID string,
	sk string,
) (*T, *apptheory.AppTheoryError) {
	item, err := getSoulAgentItemBySK[T](s, ctx, agentID, sk)
	if theoryErrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	return item, nil
}

func buildSoulCommContactabilityResponse(
	reqCtx mailboxRequestContext,
	prefs *models.SoulAgentContactPreferences,
	email *models.SoulAgentChannel,
	phone *models.SoulAgentChannel,
) soulCommContactabilityResponse {
	activeIdentity := soulCommContactabilityIdentityActive(reqCtx.identity)
	channels := make([]soulCommContactabilityChannel, 0, 2)
	if activeIdentity {
		channels = appendContactabilityChannel(channels, email)
		channels = appendContactabilityChannel(channels, phone)
	}

	contactable := false
	for _, channel := range channels {
		if channel.ReceiveAllowed || channel.SendAllowed {
			contactable = true
			break
		}
	}

	updatedAt := latestSoulChannelUpdate(time.Time{}, prefs, nil, email, phone)
	if reqCtx.identity != nil && reqCtx.identity.UpdatedAt.After(updatedAt) {
		updatedAt = reqCtx.identity.UpdatedAt
	}

	return soulCommContactabilityResponse{
		InstanceSlug: strings.ToLower(strings.TrimSpace(reqCtx.key.InstanceSlug)),
		AgentID:      reqCtx.agentID,
		Contactable:  contactable,
		Preferred:    contactabilityPreference(prefs, "preferred"),
		Fallback:     contactabilityPreference(prefs, "fallback"),
		Channels:     channels,
		Mailbox:      contactabilityMailbox(channels),
		Availability: contactabilityAvailability(prefs),
		FirstContact: contactabilityFirstContact(prefs),
		UpdatedAt:    updatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func soulCommContactabilityIdentityActive(identity *models.SoulAgentIdentity) bool {
	if identity == nil {
		return false
	}
	status := strings.TrimSpace(identity.LifecycleStatus)
	if status == "" {
		status = strings.TrimSpace(identity.Status)
	}
	return status == models.SoulAgentStatusActive
}

func appendContactabilityChannel(
	out []soulCommContactabilityChannel,
	channel *models.SoulAgentChannel,
) []soulCommContactabilityChannel {
	view, ok := contactabilityChannelView(channel)
	if !ok {
		return out
	}
	return append(out, view)
}

func contactabilityChannelView(channel *models.SoulAgentChannel) (soulCommContactabilityChannel, bool) {
	if !contactabilityChannelVisible(channel) {
		return soulCommContactabilityChannel{}, false
	}

	caps := append([]string(nil), channel.Capabilities...)
	view := soulCommContactabilityChannel{
		ChannelType:    strings.TrimSpace(channel.ChannelType),
		Provider:       strings.TrimSpace(channel.Provider),
		Capabilities:   caps,
		Protocols:      append([]string(nil), channel.Protocols...),
		Verified:       channel.Verified,
		Status:         strings.TrimSpace(channel.Status),
		ReceiveAllowed: contactabilityReceiveAllowed(channel),
		SendAllowed:    contactabilitySendAllowed(channel),
	}
	switch view.ChannelType {
	case models.SoulChannelTypeEmail:
		view.Address = strings.TrimSpace(channel.Identifier)
	case models.SoulChannelTypePhone:
		view.Number = strings.TrimSpace(channel.Identifier)
	}
	return view, true
}

func contactabilityChannelVisible(channel *models.SoulAgentChannel) bool {
	if channel == nil {
		return false
	}
	if strings.TrimSpace(channel.Identifier) == "" {
		return false
	}
	if strings.TrimSpace(channel.Status) != models.SoulChannelStatusActive || !channel.Verified {
		return false
	}
	return !channel.ProvisionedAt.IsZero() && channel.DeprovisionedAt.IsZero()
}

func contactabilityReceiveAllowed(channel *models.SoulAgentChannel) bool {
	switch strings.TrimSpace(channel.ChannelType) {
	case models.SoulChannelTypeEmail:
		return stringSliceContains(channel.Capabilities, "receive")
	case models.SoulChannelTypePhone:
		return stringSliceContains(channel.Capabilities, "sms-receive") || stringSliceContains(channel.Capabilities, "voice-receive")
	default:
		return false
	}
}

func contactabilitySendAllowed(channel *models.SoulAgentChannel) bool {
	switch strings.TrimSpace(channel.ChannelType) {
	case models.SoulChannelTypeEmail:
		return stringSliceContains(channel.Capabilities, "send")
	case models.SoulChannelTypePhone:
		return stringSliceContains(channel.Capabilities, "sms-send") || stringSliceContains(channel.Capabilities, "voice-send")
	default:
		return false
	}
}

func stringSliceContains(values []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == want {
			return true
		}
	}
	return false
}

func contactabilityMailbox(channels []soulCommContactabilityChannel) soulCommContactabilityMailbox {
	receiveAllowed := false
	for _, channel := range channels {
		if channel.ReceiveAllowed {
			receiveAllowed = true
			break
		}
	}
	return soulCommContactabilityMailbox{
		ListAllowed:    receiveAllowed,
		GetAllowed:     receiveAllowed,
		ContentAllowed: receiveAllowed,
		StateAllowed:   receiveAllowed,
	}
}

func contactabilityPreference(prefs *models.SoulAgentContactPreferences, field string) string {
	if prefs == nil {
		return ""
	}
	if field == "fallback" {
		return strings.TrimSpace(prefs.Fallback)
	}
	return strings.TrimSpace(prefs.Preferred)
}

func contactabilityAvailability(prefs *models.SoulAgentContactPreferences) soulCommContactabilityAvailability {
	if prefs == nil {
		return soulCommContactabilityAvailability{Schedule: "always"}
	}
	windows := make([]soulCommContactabilityAvailabilityWindow, 0, len(prefs.AvailabilityWindows))
	for _, window := range prefs.AvailabilityWindows {
		windows = append(windows, soulCommContactabilityAvailabilityWindow{
			Days:      append([]string(nil), window.Days...),
			StartTime: strings.TrimSpace(window.StartTime),
			EndTime:   strings.TrimSpace(window.EndTime),
		})
	}
	schedule := strings.TrimSpace(prefs.AvailabilitySchedule)
	if schedule == "" {
		schedule = "always"
	}
	return soulCommContactabilityAvailability{
		Schedule: schedule,
		Timezone: strings.TrimSpace(prefs.AvailabilityTimezone),
		Windows:  windows,
	}
}

func contactabilityFirstContact(prefs *models.SoulAgentContactPreferences) soulCommContactabilityFirstContact {
	if prefs == nil {
		return soulCommContactabilityFirstContact{}
	}
	return soulCommContactabilityFirstContact{
		RequireSoul:          prefs.FirstContactRequireSoul,
		RequireReputation:    prefs.FirstContactRequireReputation,
		IntroductionExpected: prefs.FirstContactIntroductionExpected,
	}
}
