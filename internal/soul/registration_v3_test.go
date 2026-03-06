package soul

import (
	"encoding/json"
	"testing"
)

func validRegistrationV3(t *testing.T) RegistrationFileV3 {
	t.Helper()

	base := validRegistrationV2(t)
	return RegistrationFileV3{
		Version:   "3",
		AgentID:   base.AgentID,
		Domain:    base.Domain,
		LocalID:   base.LocalID,
		Wallet:    base.Wallet,
		Principal: base.Principal,

		SelfDescription: base.SelfDescription,
		Capabilities:    base.Capabilities,
		Boundaries:      base.Boundaries,
		Transparency:    base.Transparency,
		Continuity:      base.Continuity,
		Endpoints:       base.Endpoints,
		Lifecycle:       base.Lifecycle,

		Channels: &ChannelsV3{
			ENS: &ENSChannelV3{
				Name:            "agent-bot.eth",
				ResolverAddress: "0x000000000000000000000000000000000000dEaD",
				Chain:           "mainnet",
			},
			Email: &EmailChannelV3{
				Address:      "agent-bot@example.com",
				Capabilities: []string{"receive", "send"},
				Protocols:    []string{"smtp", "imap"},
				Verified:     true,
				VerifiedAt:   "2026-03-05T12:00:00Z",
			},
			Phone: &PhoneChannelV3{
				Number:       "+15551234567",
				Capabilities: []string{"sms-receive", "sms-send", "voice-receive"},
				Provider:     "telnyx",
				Verified:     true,
				VerifiedAt:   "2026-03-05T12:00:00Z",
			},
		},
		ContactPreferences: &ContactPreferencesV3{
			Preferred: "email",
			Fallback:  "sms",
			Availability: ContactAvailabilityV3{
				Schedule: "custom",
				Timezone: "UTC",
				Windows: []ContactAvailabilityWindowV3{
					{Days: []string{"mon", "tue"}, StartTime: "09:00", EndTime: "17:00"},
				},
			},
			ResponseExpectation: ResponseExpectationV3{
				Target:    "24h",
				Guarantee: "best-effort",
			},
			RateLimits:   map[string]any{"email": map[string]any{"perHour": 5}},
			Languages:    []string{"en", "es"},
			ContentTypes: []string{"text/plain"},
			FirstContact: &ContactFirstContactV3{
				RequireSoul:          true,
				RequireReputation:    ptr(0.7),
				IntroductionExpected: true,
			},
		},

		PreviousVersionURI: ptr("https://example.com/registrations/1.json"),
		ChangeSummary:      base.ChangeSummary,
		Attestations:       base.Attestations,
		Created:            base.Created,
		Updated:            base.Updated,
	}
}

func TestRegistrationFileV3_ParseAndValidate(t *testing.T) {
	t.Parallel()

	if _, err := ParseRegistrationFileV3(nil); err == nil {
		t.Fatalf("expected parse error for empty body")
	}
	if _, err := ParseRegistrationFileV3([]byte("{")); err == nil {
		t.Fatalf("expected parse error for invalid json")
	}

	valid := validRegistrationV3(t)
	body, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed, err := ParseRegistrationFileV3(body)
	if err != nil {
		t.Fatalf("ParseRegistrationFileV3: %v", err)
	}
	if err := parsed.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	var nilReg *RegistrationFileV3
	if err := nilReg.Validate(); err == nil {
		t.Fatalf("expected nil receiver error")
	}

	t.Run("optional nil success", func(t *testing.T) {
		t.Parallel()

		reg := validRegistrationV3(t)
		reg.Channels = nil
		reg.ContactPreferences = nil
		if err := reg.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	cases := []struct {
		name string
		mut  func(*RegistrationFileV3)
	}{
		{name: "bad version", mut: func(r *RegistrationFileV3) { r.Version = "2" }},
		{name: "bad previous version", mut: func(r *RegistrationFileV3) { r.PreviousVersionURI = ptr("://bad") }},
		{name: "bad channels", mut: func(r *RegistrationFileV3) { r.Channels = &ChannelsV3{} }},
		{name: "bad contact preferences", mut: func(r *RegistrationFileV3) { r.ContactPreferences = &ContactPreferencesV3{Preferred: "pager"} }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := validRegistrationV3(t)
			tc.mut(&reg)
			if err := reg.Validate(); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestRegistrationV3ChannelsLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *ChannelsV3
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	if err := (&ChannelsV3{}).Validate(); err == nil {
		t.Fatalf("expected empty channels error")
	}
	if err := (&ChannelsV3{ENS: &ENSChannelV3{Name: "bad"}}).Validate(); err == nil {
		t.Fatalf("expected wrapped ens error")
	}
	if err := (&ChannelsV3{Email: &EmailChannelV3{Address: "bad"}}).Validate(); err == nil {
		t.Fatalf("expected wrapped email error")
	}
	if err := (&ChannelsV3{Phone: &PhoneChannelV3{Number: "bad"}}).Validate(); err == nil {
		t.Fatalf("expected wrapped phone error")
	}
}

func TestRegistrationV3ENSLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *ENSChannelV3
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []ENSChannelV3{
		{},
		{Name: "bad name"},
		{Name: "agent.eth", ResolverAddress: "bad"},
		{Name: "agent.eth", ResolverAddress: "0x0000000000000000000000000000000000000001"},
	}
	for i, tc := range cases {
		tc := tc
		if i < len(cases)-1 {
			if err := tc.Validate(); err == nil {
				t.Fatalf("expected ens error for %#v", tc)
			}
			continue
		}
		if err := tc.Validate(); err != nil {
			t.Fatalf("unexpected ens error: %v", err)
		}
	}
}

func TestRegistrationV3EmailLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *EmailChannelV3
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []EmailChannelV3{
		{},
		{Address: "bad"},
		{Address: "a@example.com"},
		{Address: "a@example.com", Capabilities: []string{"bad"}},
		{Address: "a@example.com", Capabilities: []string{"receive"}, Protocols: []string{"bad"}},
		{Address: "a@example.com", Capabilities: []string{"receive"}, VerifiedAt: "bad"},
		{Address: "a@example.com", Capabilities: []string{"receive", "send"}, Protocols: []string{"smtp"}, VerifiedAt: "2026-03-05T12:00:00Z"},
	}
	for i, tc := range cases {
		tc := tc
		if i < len(cases)-1 {
			if err := tc.Validate(); err == nil {
				t.Fatalf("expected email error for %#v", tc)
			}
			continue
		}
		if err := tc.Validate(); err != nil {
			t.Fatalf("unexpected email error: %v", err)
		}
	}
}

func TestRegistrationV3PhoneLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *PhoneChannelV3
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []PhoneChannelV3{
		{},
		{Number: "123"},
		{Number: "+15551234567"},
		{Number: "+15551234567", Capabilities: []string{"bad"}},
		{Number: "+15551234567", Capabilities: []string{"sms-receive"}, VerifiedAt: "bad"},
		{Number: "+15551234567", Capabilities: []string{"sms-receive", "voice-send"}, VerifiedAt: "2026-03-05T12:00:00Z"},
	}
	for i, tc := range cases {
		tc := tc
		if i < len(cases)-1 {
			if err := tc.Validate(); err == nil {
				t.Fatalf("expected phone error for %#v", tc)
			}
			continue
		}
		if err := tc.Validate(); err != nil {
			t.Fatalf("unexpected phone error: %v", err)
		}
	}
}

func TestRegistrationV3ContactPreferencesLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *ContactPreferencesV3
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []ContactPreferencesV3{
		{Preferred: "pager"},
		{Preferred: "email", Fallback: "pager"},
		{Preferred: "email", Availability: ContactAvailabilityV3{Schedule: "bad"}},
		{Preferred: "email", Availability: ContactAvailabilityV3{Schedule: "always"}, ResponseExpectation: ResponseExpectationV3{Guarantee: "best-effort"}},
		{Preferred: "email", Availability: ContactAvailabilityV3{Schedule: "always"}, ResponseExpectation: ResponseExpectationV3{Target: "24h", Guarantee: "bad"}},
		{Preferred: "email", Availability: ContactAvailabilityV3{Schedule: "always"}, ResponseExpectation: ResponseExpectationV3{Target: "24h", Guarantee: "best-effort"}},
		{Preferred: "email", Availability: ContactAvailabilityV3{Schedule: "always"}, ResponseExpectation: ResponseExpectationV3{Target: "24h", Guarantee: "best-effort"}, Languages: []string{"english"}},
		{Preferred: "email", Availability: ContactAvailabilityV3{Schedule: "always"}, ResponseExpectation: ResponseExpectationV3{Target: "24h", Guarantee: "best-effort"}, Languages: []string{"en"}, FirstContact: &ContactFirstContactV3{RequireReputation: ptr(2.0)}},
		{Preferred: "email", Availability: ContactAvailabilityV3{Schedule: "always"}, ResponseExpectation: ResponseExpectationV3{Target: "24h", Guarantee: "best-effort"}, Languages: []string{"en"}},
	}
	for i, tc := range cases {
		tc := tc
		if i < len(cases)-1 {
			if err := tc.Validate(); err == nil {
				t.Fatalf("expected contact preferences error for %#v", tc)
			}
			continue
		}
		if err := tc.Validate(); err != nil {
			t.Fatalf("unexpected contact preferences error: %v", err)
		}
	}
}

func TestRegistrationV3AvailabilityLeafValidator(t *testing.T) {
	t.Parallel()

	if err := (&ContactAvailabilityV3{Schedule: "bad"}).Validate(); err == nil {
		t.Fatalf("expected bad schedule error")
	}
	if err := (&ContactAvailabilityV3{
		Schedule: "custom",
		Windows:  []ContactAvailabilityWindowV3{{Days: []string{"bad"}, StartTime: "09:00", EndTime: "17:00"}},
	}).Validate(); err == nil {
		t.Fatalf("expected wrapped window error")
	}
	if err := (&ContactAvailabilityV3{
		Schedule: "custom",
		Windows:  []ContactAvailabilityWindowV3{{Days: []string{"mon"}, StartTime: "09:00", EndTime: "17:00"}},
	}).Validate(); err != nil {
		t.Fatalf("unexpected availability error: %v", err)
	}
}

func TestRegistrationV3WindowLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *ContactAvailabilityWindowV3
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []ContactAvailabilityWindowV3{
		{},
		{Days: []string{"bad"}, StartTime: "09:00", EndTime: "17:00"},
		{Days: []string{"mon"}, StartTime: "bad", EndTime: "17:00"},
		{Days: []string{"mon"}, StartTime: "09:00", EndTime: "bad"},
		{Days: []string{"mon"}, StartTime: "09:00", EndTime: "17:00"},
	}
	for i, tc := range cases {
		tc := tc
		if i < len(cases)-1 {
			if err := tc.Validate(); err == nil {
				t.Fatalf("expected window error for %#v", tc)
			}
			continue
		}
		if err := tc.Validate(); err != nil {
			t.Fatalf("unexpected window error: %v", err)
		}
	}
}

func TestRegistrationV3ResponseExpectationLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *ResponseExpectationV3
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	if err := (&ResponseExpectationV3{Guarantee: "best-effort"}).Validate(); err == nil {
		t.Fatalf("expected target required error")
	}
	if err := (&ResponseExpectationV3{Target: "24h", Guarantee: "bad"}).Validate(); err == nil {
		t.Fatalf("expected guarantee error")
	}
	if err := (&ResponseExpectationV3{Target: "24h", Guarantee: "best-effort"}).Validate(); err != nil {
		t.Fatalf("unexpected response expectation error: %v", err)
	}
}

func TestRegistrationV3FirstContactLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *ContactFirstContactV3
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	if err := (&ContactFirstContactV3{RequireReputation: ptr(-0.1)}).Validate(); err == nil {
		t.Fatalf("expected negative reputation error")
	}
	if err := (&ContactFirstContactV3{RequireReputation: ptr(1.1)}).Validate(); err == nil {
		t.Fatalf("expected oversized reputation error")
	}
	if err := (&ContactFirstContactV3{RequireReputation: ptr(0.5)}).Validate(); err != nil {
		t.Fatalf("unexpected first contact error: %v", err)
	}
}
