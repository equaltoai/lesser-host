package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSoulAgentChannel_KeysAndNormalization(t *testing.T) {
	t.Parallel()

	c := &SoulAgentChannel{
		AgentID:      " 0xABC ",
		ChannelType:  " EMAIL ",
		Identifier:   " Agent-Bob@Lessersoul.ai ",
		Capabilities: []string{"Send", "receive", "send"},
		Protocols:    []string{"SMTP", "smtp"},
		Provider:     " Migadu ",
	}
	require.NoError(t, c.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", c.PK)
	require.Equal(t, "CHANNEL#email", c.SK)
	require.Equal(t, "0xabc", c.AgentID)
	require.Equal(t, "email", c.ChannelType)
	require.Equal(t, "agent-bob@lessersoul.ai", c.Identifier)
	require.Equal(t, []string{"receive", "send"}, c.Capabilities)
	require.Equal(t, []string{"smtp"}, c.Protocols)
	require.Equal(t, "migadu", c.Provider)
	require.Equal(t, SoulChannelStatusActive, c.Status)
	require.False(t, c.UpdatedAt.IsZero())
}

func TestSoulEmailAgentIndex_Keys(t *testing.T) {
	t.Parallel()

	i := &SoulEmailAgentIndex{Email: " Agent-Bob@Lessersoul.ai ", AgentID: " 0xABC "}
	require.NoError(t, i.BeforeCreate())
	require.Equal(t, "SOUL#EMAIL#agent-bob@lessersoul.ai", i.PK)
	require.Equal(t, "AGENT", i.SK)
	require.Equal(t, "agent-bob@lessersoul.ai", i.Email)
	require.Equal(t, "0xabc", i.AgentID)
}

func TestSoulPhoneAgentIndex_Keys(t *testing.T) {
	t.Parallel()

	i := &SoulPhoneAgentIndex{Phone: " +1 (555) 012-3456 ", AgentID: " 0xABC "}
	require.NoError(t, i.BeforeCreate())
	require.Equal(t, "SOUL#PHONE#+15550123456", i.PK)
	require.Equal(t, "AGENT", i.SK)
	require.Equal(t, "+15550123456", i.Phone)
	require.Equal(t, "0xabc", i.AgentID)
}

func TestSoulChannelAgentIndex_Keys(t *testing.T) {
	t.Parallel()

	i := &SoulChannelAgentIndex{
		ChannelType: " Email ",
		Domain:      " Example.COM ",
		LocalID:     " @Bob/ ",
		AgentID:     " 0xABC ",
	}
	require.NoError(t, i.BeforeCreate())
	require.Equal(t, "SOUL#CHANNEL#email", i.PK)
	require.Equal(t, "DOMAIN#example.com#LOCAL#bob#AGENT#0xabc", i.SK)
	require.Equal(t, "email", i.ChannelType)
	require.Equal(t, "example.com", i.Domain)
	require.Equal(t, "bob", i.LocalID)
	require.Equal(t, "0xabc", i.AgentID)
}

func TestSoulAgentContactPreferences_Keys(t *testing.T) {
	t.Parallel()

	p := &SoulAgentContactPreferences{
		AgentID:              " 0xABC ",
		Preferred:            " EMAIL ",
		Fallback:             " ActivityPub ",
		AvailabilitySchedule: " BUSINESS-HOURS ",
		AvailabilityTimezone: " America/New_York ",
		AvailabilityWindows: []SoulContactAvailabilityWindow{
			{Days: []string{"Mon", "Tue"}, StartTime: "09:00", EndTime: "17:00"},
		},
		Languages: []string{"EN", "es"},
	}
	require.NoError(t, p.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", p.PK)
	require.Equal(t, "CONTACT_PREFERENCES", p.SK)
	require.Equal(t, "email", p.Preferred)
	require.Equal(t, "activitypub", p.Fallback)
	require.Equal(t, "business-hours", p.AvailabilitySchedule)
	require.Equal(t, "America/New_York", p.AvailabilityTimezone)
	require.Equal(t, []string{"mon", "tue"}, p.AvailabilityWindows[0].Days)
	require.Equal(t, []string{"en", "es"}, p.Languages)
	require.False(t, p.UpdatedAt.IsZero())
}

func TestSoulAgentCommActivity_TTLAndKeys(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 4, 12, 0, 0, 123456789, time.UTC)
	a := &SoulAgentCommActivity{
		AgentID:     " 0xABC ",
		ActivityID:  " act-1 ",
		ChannelType: " email ",
		Timestamp:   ts,
	}
	require.NoError(t, a.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", a.PK)
	require.Equal(t, "COMM#2026-03-04T12:00:00.123456789Z#act-1", a.SK)
	require.Equal(t, SoulCommDirectionInbound, a.Direction)
	require.Equal(t, SoulCommBoundaryCheckSkipped, a.BoundaryCheck)
	require.Equal(t, ts.Add(90*24*time.Hour).Unix(), a.TTL)
}

func TestSoulAgentCommQueue_TTLAndKeys(t *testing.T) {
	t.Parallel()

	received := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	sched := time.Date(2026, 3, 4, 18, 0, 0, 0, time.UTC)
	q := &SoulAgentCommQueue{
		AgentID:               " 0xABC ",
		MessageID:             " msg-1 ",
		ChannelType:           " email ",
		FromAddress:           " Alice@Example.com ",
		Body:                  " Hello ",
		ReceivedAt:            received,
		ScheduledDeliveryTime: sched,
	}
	require.NoError(t, q.BeforeCreate())

	require.Equal(t, "COMM#QUEUE#0xabc", q.PK)
	require.Equal(t, "MSG#2026-03-04T18:00:00.000000000Z#msg-1", q.SK)
	require.Equal(t, SoulCommQueueStatusQueued, q.Status)
	require.Equal(t, received.Add(72*time.Hour).Unix(), q.TTL)
	require.Equal(t, "alice@example.com", q.FromAddress)
	require.Equal(t, "Hello", q.Body)
}

func TestSoulAgentENSResolution_Keys(t *testing.T) {
	t.Parallel()

	r := &SoulAgentENSResolution{
		ENSName: " Agent-Bob.Lessersoul.eth. ",
		AgentID: " 0xABC ",
		LocalID: " @Bob/ ",
		Domain:  " Example.COM ",
		Email:   " Agent-Bob@Lessersoul.ai ",
		Phone:   " +1 (555) 012-3456 ",
	}
	require.NoError(t, r.BeforeCreate())

	require.Equal(t, "ENS#NAME#agent-bob.lessersoul.eth", r.PK)
	require.Equal(t, "RESOLUTION", r.SK)
	require.Equal(t, "agent-bob.lessersoul.eth", r.ENSName)
	require.Equal(t, "0xabc", r.AgentID)
	require.Equal(t, "bob", r.LocalID)
	require.Equal(t, "example.com", r.Domain)
	require.Equal(t, "agent-bob@lessersoul.ai", r.Email)
	require.Equal(t, "+15550123456", r.Phone)
	require.False(t, r.CreatedAt.IsZero())
	require.False(t, r.UpdatedAt.IsZero())
}
