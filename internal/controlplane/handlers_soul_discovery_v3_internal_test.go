package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func requireAppErrorCode(t *testing.T, err error, want string) {
	t.Helper()

	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected *apptheory.AppError, got %T: %v", err, err)
	}
	if appErr.Code != want {
		t.Fatalf("expected app error %q, got %q", want, appErr.Code)
	}
}

func TestSoulPublicDiscoveryV3ENSChannelFromModel(t *testing.T) {
	t.Parallel()

	if got := soulPublicENSChannelFromModel(nil); got != nil {
		t.Fatalf("expected nil ENS channel for nil model, got %#v", got)
	}
	if got := soulPublicENSChannelFromModel(&models.SoulAgentChannel{Identifier: " "}); got != nil {
		t.Fatalf("expected nil ENS channel for blank identifier, got %#v", got)
	}

	got := soulPublicENSChannelFromModel(&models.SoulAgentChannel{
		Identifier:         " agent.lessersoul.eth ",
		ENSResolverAddress: " 0xresolver ",
		ENSChain:           " base ",
	})
	if got == nil {
		t.Fatalf("expected ENS channel")
	}
	if got.Name != "agent.lessersoul.eth" || got.ResolverAddress != "0xresolver" || got.Chain != "base" {
		t.Fatalf("unexpected ENS channel: %#v", got)
	}
}

func TestSoulPublicDiscoveryV3EmailChannelFromModel(t *testing.T) {
	t.Parallel()

	if got := soulPublicEmailChannelFromModel(&models.SoulAgentChannel{Identifier: " "}); got != nil {
		t.Fatalf("expected nil email channel for blank identifier, got %#v", got)
	}

	got := soulPublicEmailChannelFromModel(&models.SoulAgentChannel{
		Identifier:   " person@example.com ",
		Capabilities: []string{"receive"},
		Protocols:    []string{"smtp"},
		Verified:     false,
		Status:       " active ",
	})
	if got == nil {
		t.Fatalf("expected email channel")
	}
	if got.Address != "person@example.com" || got.Status != "active" || got.VerifiedAt != "" {
		t.Fatalf("unexpected email channel: %#v", got)
	}
}

func TestSoulPublicDiscoveryV3PhoneChannelFromModel(t *testing.T) {
	t.Parallel()

	if got := soulPublicPhoneChannelFromModel(&models.SoulAgentChannel{Identifier: " "}); got != nil {
		t.Fatalf("expected nil phone channel for blank identifier, got %#v", got)
	}

	verifiedAt := time.Date(2026, 3, 4, 10, 15, 0, 0, time.UTC)
	got := soulPublicPhoneChannelFromModel(&models.SoulAgentChannel{
		Identifier:   " +14155550123 ",
		Capabilities: []string{"sms"},
		Provider:     " telnyx ",
		Verified:     true,
		VerifiedAt:   verifiedAt,
		Status:       " paused ",
	})
	if got == nil {
		t.Fatalf("expected phone channel")
	}
	if got.Number != "+14155550123" || got.Provider != "telnyx" || got.Status != "paused" {
		t.Fatalf("unexpected phone channel: %#v", got)
	}
	if got.VerifiedAt != verifiedAt.Format(time.RFC3339Nano) {
		t.Fatalf("expected verifiedAt %q, got %q", verifiedAt.Format(time.RFC3339Nano), got.VerifiedAt)
	}
}

func TestSoulPublicDiscoveryV3ContactPreferencesFromModelNilOrBlank(t *testing.T) {
	t.Parallel()

	if got := soulPublicContactPreferencesFromModel(nil); got != nil {
		t.Fatalf("expected nil contact preferences, got %#v", got)
	}
	if got := soulPublicContactPreferencesFromModel(&models.SoulAgentContactPreferences{Preferred: " "}); got != nil {
		t.Fatalf("expected nil contact preferences for blank preferred, got %#v", got)
	}
}

func TestSoulPublicDiscoveryV3ContactPreferencesFromModelMapsFields(t *testing.T) {
	t.Parallel()

	minReputation := 0.42
	got := soulPublicContactPreferencesFromModel(&models.SoulAgentContactPreferences{
		Preferred:            " email ",
		Fallback:             " phone ",
		AvailabilitySchedule: " weekdays ",
		AvailabilityTimezone: " America/New_York ",
		AvailabilityWindows: []models.SoulContactAvailabilityWindow{
			{Days: []string{"mon", "tue"}, StartTime: " 09:00 ", EndTime: " 17:00 "},
		},
		ResponseTarget:                   " PT2H ",
		ResponseGuarantee:                " best-effort ",
		RateLimits:                       map[string]any{"daily": 3},
		Languages:                        []string{"en"},
		ContentTypes:                     []string{"text/plain"},
		FirstContactRequireSoul:          true,
		FirstContactRequireReputation:    &minReputation,
		FirstContactIntroductionExpected: true,
	})
	if got == nil {
		t.Fatalf("expected contact preferences")
	}
	assertSoulPublicContactPreferences(t, got, minReputation)
}

func assertSoulPublicContactPreferences(t *testing.T, got *soul.ContactPreferencesV3, minReputation float64) {
	t.Helper()
	if got.Preferred != commChannelEmail || got.Fallback != models.SoulChannelTypePhone {
		t.Fatalf("unexpected preferred/fallback: %#v", got)
	}
	if got.Availability.Schedule != "weekdays" || got.Availability.Timezone != "America/New_York" {
		t.Fatalf("unexpected availability: %#v", got.Availability)
	}
	if len(got.Availability.Windows) != 1 || got.Availability.Windows[0].StartTime != "09:00" || got.Availability.Windows[0].EndTime != "17:00" {
		t.Fatalf("unexpected windows: %#v", got.Availability.Windows)
	}
	if got.ResponseExpectation.Target != "PT2H" || got.ResponseExpectation.Guarantee != "best-effort" {
		t.Fatalf("unexpected response expectation: %#v", got.ResponseExpectation)
	}
	if got.FirstContact == nil || got.FirstContact.RequireReputation == nil || *got.FirstContact.RequireReputation != minReputation || !got.FirstContact.RequireSoul || !got.FirstContact.IntroductionExpected {
		t.Fatalf("unexpected first contact: %#v", got.FirstContact)
	}
}

func TestHandleSoulPublicGetAgentChannels_DiscoveryV3ENSAndPhone(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("44", 32)
	identityUpdated := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	ensUpdated := identityUpdated.Add(30 * time.Minute)
	phoneUpdated := identityUpdated.Add(2 * time.Hour)
	verifiedAt := phoneUpdated.Add(-20 * time.Minute)

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentID,
			Domain:    "example.com",
			LocalID:   "agent-caro",
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: identityUpdated,
		}
	}).Once()
	tdb.qPrefs.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:            agentID,
			ChannelType:        models.SoulChannelTypeENS,
			Identifier:         "agent-caro.lessersoul.eth",
			ENSResolverAddress: "0xresolver",
			ENSChain:           "base",
			UpdatedAt:          ensUpdated,
		}
	}).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:      agentID,
			ChannelType:  models.SoulChannelTypePhone,
			Identifier:   "+14155550123",
			Capabilities: []string{"sms"},
			Provider:     "telnyx",
			Verified:     true,
			VerifiedAt:   verifiedAt,
			Status:       models.SoulChannelStatusActive,
			UpdatedAt:    phoneUpdated,
		}
	}).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgentChannels(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulPublicAgentChannelsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.AgentID != agentID {
		t.Fatalf("expected agentId %q, got %q", agentID, out.AgentID)
	}
	if out.Channels.ENS == nil || out.Channels.ENS.Name != "agent-caro.lessersoul.eth" {
		t.Fatalf("unexpected ENS channel: %#v", out.Channels.ENS)
	}
	if out.Channels.Email != nil {
		t.Fatalf("expected no email channel, got %#v", out.Channels.Email)
	}
	if out.Channels.Phone == nil || out.Channels.Phone.Number != "+14155550123" || out.Channels.Phone.VerifiedAt != verifiedAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected phone channel: %#v", out.Channels.Phone)
	}
	if out.ContactPreferences != nil {
		t.Fatalf("expected nil contact preferences, got %#v", out.ContactPreferences)
	}
	if out.UpdatedAt != phoneUpdated.Format(time.RFC3339Nano) {
		t.Fatalf("expected updatedAt %q, got %q", phoneUpdated.Format(time.RFC3339Nano), out.UpdatedAt)
	}
}

func TestHandleSoulPublicGetAgentChannelPreferences_DiscoveryV3Success(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("55", 32)
	identityUpdated := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	prefsUpdated := identityUpdated.Add(90 * time.Minute)
	minReputation := 0.75

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentID,
			Domain:    "example.com",
			LocalID:   "agent-drew",
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: identityUpdated,
		}
	}).Once()
	tdb.qPrefs.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentContactPreferences](t, args, 0)
		*dest = models.SoulAgentContactPreferences{
			AgentID:                          agentID,
			Preferred:                        "email",
			Fallback:                         "phone",
			AvailabilitySchedule:             "weekdays",
			AvailabilityTimezone:             "UTC",
			AvailabilityWindows:              []models.SoulContactAvailabilityWindow{{Days: []string{"mon", "tue"}, StartTime: "09:00", EndTime: "17:00"}},
			ResponseTarget:                   "PT4H",
			ResponseGuarantee:                "best-effort",
			RateLimits:                       map[string]any{"daily": 2},
			Languages:                        []string{"en"},
			ContentTypes:                     []string{"text/plain"},
			FirstContactRequireReputation:    &minReputation,
			FirstContactIntroductionExpected: true,
			UpdatedAt:                        prefsUpdated,
		}
	}).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgentChannelPreferences(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulPublicAgentContactPreferencesResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.AgentID != agentID || out.ContactPreferences == nil {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.ContactPreferences.Preferred != commChannelEmail || out.ContactPreferences.Fallback != models.SoulChannelTypePhone {
		t.Fatalf("unexpected contact preferences: %#v", out.ContactPreferences)
	}
	if out.ContactPreferences.FirstContact == nil || out.ContactPreferences.FirstContact.RequireReputation == nil || *out.ContactPreferences.FirstContact.RequireReputation != minReputation {
		t.Fatalf("unexpected first contact: %#v", out.ContactPreferences.FirstContact)
	}
	if out.UpdatedAt != prefsUpdated.Format(time.RFC3339Nano) {
		t.Fatalf("expected updatedAt %q, got %q", prefsUpdated.Format(time.RFC3339Nano), out.UpdatedAt)
	}
}

func TestHandleSoulPublicGetAgentChannelPreferences_DiscoveryV3PrefsNotFoundUsesIdentityTimestamp(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("55", 32)
	identityUpdated := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentID,
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: identityUpdated,
		}
	}).Once()
	tdb.qPrefs.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgentChannelPreferences(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var out soulPublicAgentContactPreferencesResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ContactPreferences != nil {
		t.Fatalf("expected nil contact preferences, got %#v", out.ContactPreferences)
	}
	if out.UpdatedAt != identityUpdated.Format(time.RFC3339Nano) {
		t.Fatalf("expected updatedAt %q, got %q", identityUpdated.Format(time.RFC3339Nano), out.UpdatedAt)
	}
}

func TestHandleSoulPublicResolveLookup_DiscoveryV3(t *testing.T) {
	t.Parallel()

	suites := []struct {
		name string
		cfg  discoveryResolveSuiteConfig
	}{
		{
			name: "ens_name",
			cfg: newDiscoveryResolveSuiteConfig(
				"ensName",
				" ",
				"agent.example.com",
				"agent.lessersoul.eth",
				"Agent%2Elessersoul.eth",
				func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
					return s.handleSoulPublicResolveENSName(ctx)
				},
				func(t *testing.T, tdb *soulPublicTestDB) {
					tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()
				},
				discoveryBlankAgentSetup(discoveryENSResolveSetup),
				discoveryIdentityNotFoundSetup(discoveryENSResolveSetup),
				discoverySuccessSetup(discoveryENSResolveSetup, "agent-ens"),
			),
		},
		{
			name: "phone",
			cfg: newDiscoveryResolveSuiteConfig(
				"phoneNumber",
				" ",
				"14155550123",
				"+14155550123",
				"%2B14155550123",
				func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
					return s.handleSoulPublicResolvePhone(ctx)
				},
				func(t *testing.T, tdb *soulPublicTestDB) {
					tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
				},
				discoveryBlankAgentSetup(discoveryPhoneResolveSetup),
				discoveryIdentityNotFoundSetup(discoveryPhoneResolveSetup),
				discoverySuccessSetup(discoveryPhoneResolveSetup, "agent-phone"),
			),
		},
	}

	for _, suite := range suites {
		suite := suite
		t.Run(suite.name, func(t *testing.T) {
			t.Parallel()
			runDiscoveryResolveSuite(t, suite.cfg)
		})
	}
}

func TestHandleSoulPublicResolveEmail_DiscoveryV3Errors(t *testing.T) {
	t.Parallel()

	t.Run("empty_address_is_rejected", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		_, err := s.handleSoulPublicResolveEmail(&apptheory.Context{Params: map[string]string{"emailAddress": " "}})
		requireAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("invalid_address_is_rejected", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		_, err := s.handleSoulPublicResolveEmail(&apptheory.Context{Params: map[string]string{"emailAddress": "not-an-email"}})
		requireAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("index_without_agent_id_returns_not_found", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
			*dest = models.SoulEmailAgentIndex{Email: "agent@lessersoul.ai", AgentID: " "}
		}).Once()

		_, err := s.handleSoulPublicResolveEmail(&apptheory.Context{Params: map[string]string{"emailAddress": "agent@lessersoul.ai"}})
		requireAppErrorCode(t, err, "app.not_found")
	})

	t.Run("identity_not_found_returns_not_found", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		agentID := "0x" + strings.Repeat("88", 32)

		tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
			*dest = models.SoulEmailAgentIndex{Email: "agent@lessersoul.ai", AgentID: agentID}
		}).Once()
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

		_, err := s.handleSoulPublicResolveEmail(&apptheory.Context{Params: map[string]string{"emailAddress": "agent@lessersoul.ai"}})
		requireAppErrorCode(t, err, "app.not_found")
	})
}

type discoveryResolveCall func(*Server, *apptheory.Context) (*apptheory.Response, error)
type discoveryResolveSetup func(t *testing.T, tdb *soulPublicTestDB, agentID string)
type discoveryResolveNotFoundSetup func(t *testing.T, tdb *soulPublicTestDB)
type discoveryResolveBlankAgentSetup func(t *testing.T, tdb *soulPublicTestDB)

type discoveryResolveSuiteConfig struct {
	paramKey              string
	emptyValue            string
	invalidValue          string
	notFoundValue         string
	successValue          string
	call                  discoveryResolveCall
	notFoundSetup         discoveryResolveNotFoundSetup
	blankAgentSetup       discoveryResolveBlankAgentSetup
	identityNotFoundSetup discoveryResolveSetup
	successSetup          discoveryResolveSetup
}

func discoveryBlankAgentSetup(setup discoveryResolveSetup) discoveryResolveBlankAgentSetup {
	return func(t *testing.T, tdb *soulPublicTestDB) {
		setup(t, tdb, " ")
	}
}

func discoveryIdentityNotFoundSetup(setup discoveryResolveSetup) discoveryResolveSetup {
	return func(t *testing.T, tdb *soulPublicTestDB, agentID string) {
		setup(t, tdb, agentID)
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()
	}
}

func discoverySuccessSetup(setup discoveryResolveSetup, localID string) discoveryResolveSetup {
	return func(t *testing.T, tdb *soulPublicTestDB, agentID string) {
		setup(t, tdb, agentID)
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: localID, Status: models.SoulAgentStatusActive}
		}).Once()
		tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	}
}

func discoveryENSResolveSetup(t *testing.T, tdb *soulPublicTestDB, agentID string) {
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
}

func discoveryPhoneResolveSetup(t *testing.T, tdb *soulPublicTestDB, agentID string) {
	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulPhoneAgentIndex](t, args, 0)
		*dest = models.SoulPhoneAgentIndex{Phone: "+14155550123", AgentID: agentID}
	}).Once()
}

func newDiscoveryResolveSuiteConfig(
	paramKey string,
	emptyValue string,
	invalidValue string,
	notFoundValue string,
	successValue string,
	call discoveryResolveCall,
	notFoundSetup discoveryResolveNotFoundSetup,
	blankAgentSetup discoveryResolveBlankAgentSetup,
	identityNotFoundSetup discoveryResolveSetup,
	successSetup discoveryResolveSetup,
) discoveryResolveSuiteConfig {
	return discoveryResolveSuiteConfig{
		paramKey:              paramKey,
		emptyValue:            emptyValue,
		invalidValue:          invalidValue,
		notFoundValue:         notFoundValue,
		successValue:          successValue,
		call:                  call,
		notFoundSetup:         notFoundSetup,
		blankAgentSetup:       blankAgentSetup,
		identityNotFoundSetup: identityNotFoundSetup,
		successSetup:          successSetup,
	}
}

func runDiscoveryResolveSuite(t *testing.T, cfg discoveryResolveSuiteConfig) {
	runDiscoveryResolveCase(t, discoveryResolveCaseConfig{
		name:       "empty_value_is_rejected",
		paramKey:   cfg.paramKey,
		paramValue: cfg.emptyValue,
		wantCode:   "app.bad_request",
		call:       cfg.call,
	})
	runDiscoveryResolveCase(t, discoveryResolveCaseConfig{
		name:       "invalid_value_is_rejected",
		paramKey:   cfg.paramKey,
		paramValue: cfg.invalidValue,
		wantCode:   "app.bad_request",
		call:       cfg.call,
	})
	runDiscoveryResolveCase(t, discoveryResolveCaseConfig{
		name:       "index_not_found_returns_not_found",
		paramKey:   cfg.paramKey,
		paramValue: cfg.notFoundValue,
		wantCode:   "app.not_found",
		call:       cfg.call,
		setup: func(t *testing.T, tdb *soulPublicTestDB, _ string) {
			cfg.notFoundSetup(t, tdb)
		},
	})
	runDiscoveryResolveCase(t, discoveryResolveCaseConfig{
		name:       "blank_agent_id_returns_not_found",
		paramKey:   cfg.paramKey,
		paramValue: cfg.notFoundValue,
		wantCode:   "app.not_found",
		call:       cfg.call,
		setup: func(t *testing.T, tdb *soulPublicTestDB, _ string) {
			cfg.blankAgentSetup(t, tdb)
		},
	})
	runDiscoveryResolveCase(t, discoveryResolveCaseConfig{
		name:       "identity_not_found_returns_not_found",
		paramKey:   cfg.paramKey,
		paramValue: cfg.notFoundValue,
		wantCode:   "app.not_found",
		call:       cfg.call,
		setup:      cfg.identityNotFoundSetup,
	})
	runDiscoveryResolveCase(t, discoveryResolveCaseConfig{
		name:        "success_returns_agent",
		paramKey:    cfg.paramKey,
		paramValue:  cfg.successValue,
		call:        cfg.call,
		setup:       cfg.successSetup,
		expectAgent: "0x" + strings.Repeat("77", 32),
	})
}

type discoveryResolveCaseConfig struct {
	name        string
	paramKey    string
	paramValue  string
	wantCode    string
	call        discoveryResolveCall
	setup       discoveryResolveSetup
	expectAgent string
}

func runDiscoveryResolveCase(t *testing.T, cfg discoveryResolveCaseConfig) {
	t.Run(cfg.name, func(t *testing.T) {
		t.Parallel()
		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		if cfg.setup != nil {
			setupAgentID := "0x" + strings.Repeat("99", 32)
			if cfg.expectAgent != "" {
				setupAgentID = cfg.expectAgent
			}
			cfg.setup(t, &tdb, setupAgentID)
		}

		resp, err := cfg.call(s, &apptheory.Context{Params: map[string]string{cfg.paramKey: cfg.paramValue}})
		if cfg.expectAgent == "" {
			requireAppErrorCode(t, err, cfg.wantCode)
			return
		}
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
		}
		var out soulPublicAgentResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Agent.AgentID != cfg.expectAgent {
			t.Fatalf("expected agentID %q, got %#v", cfg.expectAgent, out)
		}
	})
}
