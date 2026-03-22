package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const canonicalMedicEmail = "medic@lessersoul.ai"

type backfillM12TestDB struct {
	db       *ttmocks.MockExtendedDB
	qChannel *ttmocks.MockQuery
}

func newBackfillM12TestDB() backfillM12TestDB {
	db := ttmocks.NewMockExtendedDB()
	qChannel := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()

	qChannel.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qChannel).Maybe()

	return backfillM12TestDB{db: db, qChannel: qChannel}
}

func TestNormalizePublicBaseURL(t *testing.T) {
	t.Parallel()

	if got := normalizePublicBaseURL(" https://lab.lesser.host/ "); got != "https://lab.lesser.host" {
		t.Fatalf("unexpected normalized url: %q", got)
	}
	if got := normalizePublicBaseURL("lab.lesser.host"); got != "" {
		t.Fatalf("expected invalid url to be rejected, got %q", got)
	}
}

func TestParseConfig(t *testing.T) {
	origArgs := os.Args
	origFlagSet := flag.CommandLine
	defer func() {
		os.Args = origArgs
		flag.CommandLine = origFlagSet
	}()

	t.Setenv("STATE_TABLE_NAME", "lesser-host-lab-state")
	t.Setenv("PUBLIC_BASE_URL", " https://lab.lesser.host/ ")
	t.Setenv("SOUL_EMAIL_INBOUND_DOMAIN", " Inbound.LesserSoul.ai ")

	os.Args = []string{"soul-backfill-m12-channel-inbound-routing", "--agent-id", " 0xAbC "}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	cfg := parseConfig()
	if cfg.agentID != "0xabc" {
		t.Fatalf("unexpected agent id: %q", cfg.agentID)
	}
	if !cfg.backfillEmail || !cfg.backfillPhone || cfg.apply {
		t.Fatalf("unexpected mode flags: %#v", cfg)
	}
	if cfg.publicBaseURL != "https://lab.lesser.host" {
		t.Fatalf("unexpected public base url: %q", cfg.publicBaseURL)
	}
	if cfg.emailInboundDomain != "inbound.lessersoul.ai" {
		t.Fatalf("unexpected email inbound domain: %q", cfg.emailInboundDomain)
	}
}

func TestResolveEmailBackfillTarget(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	target, ok := resolveEmailBackfillTarget(&models.SoulAgentChannel{
		AgentID:       "0xagent",
		ChannelType:   models.SoulChannelTypeEmail,
		Identifier:    "Medic@LesserSoul.ai",
		Provider:      "migadu",
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
	})
	if !ok {
		t.Fatalf("expected target to resolve")
	}
	if target.LocalPart != "medic" || target.Address != canonicalMedicEmail {
		t.Fatalf("unexpected target: %#v", target)
	}

	if _, ok := resolveEmailBackfillTarget(&models.SoulAgentChannel{
		ChannelType:   models.SoulChannelTypeEmail,
		Identifier:    "agent@example.com",
		Provider:      "migadu",
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
	}); ok {
		t.Fatalf("expected non-lessersoul.ai mailbox to be skipped")
	}
}

func TestShouldBackfillPhoneChannel(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	if !shouldBackfillPhoneChannel(&models.SoulAgentChannel{
		ChannelType:   models.SoulChannelTypePhone,
		Identifier:    "+15551234567",
		Provider:      "telnyx",
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
	}) {
		t.Fatalf("expected active provisioned Telnyx number to qualify")
	}
	if shouldBackfillPhoneChannel(&models.SoulAgentChannel{
		ChannelType:     models.SoulChannelTypePhone,
		Identifier:      "+15551234567",
		Provider:        "telnyx",
		Status:          models.SoulChannelStatusActive,
		ProvisionedAt:   now,
		DeprovisionedAt: now,
	}) {
		t.Fatalf("expected deprovisioned number to be skipped")
	}
}

func TestShouldBackfillEmailChannel(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	if !shouldBackfillEmailChannel(&models.SoulAgentChannel{
		ChannelType:   models.SoulChannelTypeEmail,
		Identifier:    canonicalMedicEmail,
		Provider:      "migadu",
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
	}) {
		t.Fatalf("expected active provisioned Migadu mailbox to qualify")
	}
	if shouldBackfillEmailChannel(&models.SoulAgentChannel{
		ChannelType:   models.SoulChannelTypeEmail,
		Identifier:    canonicalMedicEmail,
		Provider:      "other",
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
	}) {
		t.Fatalf("expected non-Migadu mailbox to be skipped")
	}
	if shouldBackfillEmailChannel(&models.SoulAgentChannel{
		ChannelType:   models.SoulChannelTypePhone,
		Identifier:    canonicalMedicEmail,
		Provider:      "migadu",
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
	}) {
		t.Fatalf("expected non-email channel to be skipped")
	}
}

func TestBackfillEmailInboundRoutingDryRun(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	tdb := newBackfillM12TestDB()
	tdb.qChannel.On("All", mock.AnythingOfType("*[]*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentChannel](t, args, 0)
		*dest = []*models.SoulAgentChannel{
			{AgentID: "0x1", ChannelType: models.SoulChannelTypeEmail, Identifier: canonicalMedicEmail, Provider: "migadu", Status: models.SoulChannelStatusActive, ProvisionedAt: now},
			{AgentID: "0x2", ChannelType: models.SoulChannelTypeEmail, Identifier: "ops@example.com", Provider: "migadu", Status: models.SoulChannelStatusActive, ProvisionedAt: now},
			{AgentID: "0x3", ChannelType: models.SoulChannelTypeEmail, Identifier: "scout@lessersoul.ai", Provider: "other", Status: models.SoulChannelStatusActive, ProvisionedAt: now},
		}
	}).Once()

	summary, err := backfillEmailInboundRouting(ctx, store.New(tdb.db), providerClients{
		migaduCreateForwarding: func(_ context.Context, _, _ string) error {
			t.Fatalf("forwarding should not run during dry-run")
			return nil
		},
	}, "", "inbound.lessersoul.ai", false)
	if err != nil {
		t.Fatalf("backfillEmailInboundRouting: %v", err)
	}
	if summary.Scanned != 3 || summary.Eligible != 1 || summary.ProviderUpdates != 0 || summary.Skipped != 2 || summary.Errors != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if len(summary.EligibleChannels) != 1 || summary.EligibleChannels[0] != canonicalMedicEmail+" -> medic@inbound.lessersoul.ai" {
		t.Fatalf("unexpected targets: %#v", summary.EligibleChannels)
	}
}

func TestBackfillEmailInboundRouting_RequiresConfiguredStoreAndClient(t *testing.T) {
	t.Parallel()

	if _, err := backfillEmailInboundRouting(context.Background(), nil, providerClients{
		migaduCreateForwarding: func(context.Context, string, string) error { return nil },
	}, "", "inbound.lessersoul.ai", false); err == nil || err.Error() != "store is not configured" {
		t.Fatalf("unexpected store validation error: %v", err)
	}

	tdb := newBackfillM12TestDB()
	if _, err := backfillEmailInboundRouting(context.Background(), store.New(tdb.db), providerClients{}, "", "inbound.lessersoul.ai", false); err == nil || err.Error() != "migadu forwarding client is not configured" {
		t.Fatalf("unexpected client validation error: %v", err)
	}
}

func TestBackfillEmailInboundRoutingApply(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	tdb := newBackfillM12TestDB()
	tdb.qChannel.On("All", mock.AnythingOfType("*[]*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentChannel](t, args, 0)
		*dest = []*models.SoulAgentChannel{
			{AgentID: "0x1", ChannelType: models.SoulChannelTypeEmail, Identifier: canonicalMedicEmail, Provider: "migadu", Status: models.SoulChannelStatusActive, ProvisionedAt: now},
			{AgentID: "0x2", ChannelType: models.SoulChannelTypeEmail, Identifier: "arch@lessersoul.ai", Provider: "migadu", Status: models.SoulChannelStatusActive, ProvisionedAt: now},
		}
	}).Once()

	calls := 0
	summary, err := backfillEmailInboundRouting(ctx, store.New(tdb.db), providerClients{
		migaduCreateForwarding: func(_ context.Context, localPart string, address string) error {
			calls++
			if address != "arch@inbound.lessersoul.ai" && address != "medic@inbound.lessersoul.ai" {
				t.Fatalf("unexpected forwarding address: %q", address)
			}
			if localPart == "arch" {
				return errors.New("boom")
			}
			return nil
		},
	}, "", "inbound.lessersoul.ai", true)
	if err != nil {
		t.Fatalf("backfillEmailInboundRouting: %v", err)
	}
	if calls != 2 || summary.ProviderUpdates != 1 || summary.Errors != 1 {
		t.Fatalf("unexpected apply summary: %#v calls=%d", summary, calls)
	}
}

func TestBackfillPhoneInboundRouting(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("dry run counts eligible numbers without provider update", func(t *testing.T) {
		tdb := newBackfillM12TestDB()
		tdb.qChannel.On("All", mock.AnythingOfType("*[]*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentChannel](t, args, 0)
			*dest = []*models.SoulAgentChannel{
				{ChannelType: models.SoulChannelTypePhone, Identifier: "+15551234567", Provider: "telnyx", Status: models.SoulChannelStatusActive, ProvisionedAt: now},
				{ChannelType: models.SoulChannelTypePhone, Identifier: "+15557654321", Provider: "telnyx", Status: models.SoulChannelStatusPaused, ProvisionedAt: now},
			}
		}).Once()

		summary, err := backfillPhoneInboundRouting(ctx, store.New(tdb.db), providerClients{
			telnyxUpdateProfile: func(_ context.Context, _ string) error {
				t.Fatalf("telnyx update should not run during dry-run")
				return nil
			},
		}, "", "https://lab.lesser.host", false)
		if err != nil {
			t.Fatalf("backfillPhoneInboundRouting: %v", err)
		}
		if summary.Scanned != 2 || summary.Eligible != 1 || summary.ProviderUpdates != 0 || summary.Skipped != 1 || summary.Errors != 0 {
			t.Fatalf("unexpected dry-run summary: %#v", summary)
		}
	})

	t.Run("apply updates profile once for all eligible numbers", func(t *testing.T) {
		tdb := newBackfillM12TestDB()
		tdb.qChannel.On("All", mock.AnythingOfType("*[]*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentChannel](t, args, 0)
			*dest = []*models.SoulAgentChannel{
				{ChannelType: models.SoulChannelTypePhone, Identifier: "+15551234567", Provider: "telnyx", Status: models.SoulChannelStatusActive, ProvisionedAt: now},
				{ChannelType: models.SoulChannelTypePhone, Identifier: "+15557654321", Provider: "telnyx", Status: models.SoulChannelStatusActive, ProvisionedAt: now},
			}
		}).Once()

		calls := 0
		summary, err := backfillPhoneInboundRouting(ctx, store.New(tdb.db), providerClients{
			telnyxUpdateProfile: func(_ context.Context, webhookURL string) error {
				calls++
				if webhookURL != "https://lab.lesser.host/webhooks/comm/sms/inbound" {
					t.Fatalf("unexpected webhook url: %q", webhookURL)
				}
				return nil
			},
		}, "", "https://lab.lesser.host", true)
		if err != nil {
			t.Fatalf("backfillPhoneInboundRouting: %v", err)
		}
		if calls != 1 || summary.ProviderUpdates != 1 || summary.Eligible != 2 || summary.Errors != 0 {
			t.Fatalf("unexpected apply summary: %#v calls=%d", summary, calls)
		}
	})
}

func TestListChannelsByType_LoadsSingleAgentChannel(t *testing.T) {
	ctx := context.Background()
	tdb := newBackfillM12TestDB()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		dest.AgentID = "0xagent"
		dest.ChannelType = models.SoulChannelTypeEmail
		dest.Identifier = canonicalMedicEmail
	}).Once()

	items, err := listChannelsByType(ctx, store.New(tdb.db), "0xagent", models.SoulChannelTypeEmail)
	if err != nil {
		t.Fatalf("listChannelsByType: %v", err)
	}
	if len(items) != 1 || items[0].Identifier != canonicalMedicEmail {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestDefaultProviderClients_RequireInputs(t *testing.T) {
	t.Parallel()

	if err := defaultMigaduCreateForwarding(context.Background(), "", canonicalMedicEmail); err == nil || err.Error() != "migadu forwarding localPart and address are required" {
		t.Fatalf("unexpected migadu validation error: %v", err)
	}
	if err := defaultTelnyxUpdateMessagingProfile(context.Background(), ""); err == nil || err.Error() != "telnyx webhookURL is required" {
		t.Fatalf("unexpected telnyx validation error: %v", err)
	}
	if got := emptyDefault("", "fallback"); got != "fallback" {
		t.Fatalf("unexpected emptyDefault fallback: %q", got)
	}
	if got := emptyDefault("value", "fallback"); got != "value" {
		t.Fatalf("unexpected emptyDefault passthrough: %q", got)
	}
}

func TestDefaultMigaduCreateForwardingSuccess(t *testing.T) {
	restoreProviderClientsForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := r.URL.Path; got != "/domains/lessersoul.ai/mailboxes/medic/forwardings" {
			t.Fatalf("unexpected path: %s", got)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "migadu-user" || pass != "migadu-token" {
			t.Fatalf("unexpected basic auth: ok=%v user=%q pass=%q", ok, user, pass)
		}
		var body migaduCreateForwardingRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Address != "medic@inbound.lessersoul.ai" {
			t.Fatalf("unexpected forwarding body: %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
		return secrets.MigaduCredentials{Username: "migadu-user", APIToken: "migadu-token"}, nil
	}
	migaduAPIBaseURL = server.URL
	newHTTPClient = server.Client

	if err := defaultMigaduCreateForwarding(context.Background(), "medic", "medic@inbound.lessersoul.ai"); err != nil {
		t.Fatalf("defaultMigaduCreateForwarding: %v", err)
	}
}

func TestDefaultMigaduCreateForwardingProviderFailure(t *testing.T) {
	restoreProviderClientsForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad target", http.StatusBadRequest)
	}))
	defer server.Close()

	migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
		return secrets.MigaduCredentials{Username: "migadu-user", APIToken: "migadu-token"}, nil
	}
	migaduAPIBaseURL = server.URL
	newHTTPClient = server.Client

	err := defaultMigaduCreateForwarding(context.Background(), "medic", "medic@inbound.lessersoul.ai")
	if err == nil || err.Error() != `migadu forwarding: status=400 body="bad target"` {
		t.Fatalf("unexpected migadu error: %v", err)
	}
}

func TestDefaultMigaduCreateForwardingCredentialValidation(t *testing.T) {
	t.Run("loader error", func(t *testing.T) {
		restoreProviderClientsForTest(t)
		migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
			return secrets.MigaduCredentials{}, errors.New("boom")
		}
		err := defaultMigaduCreateForwarding(context.Background(), "medic", "medic@inbound.lessersoul.ai")
		if err == nil || err.Error() != "boom" {
			t.Fatalf("unexpected loader error: %v", err)
		}
	})

	t.Run("missing api token", func(t *testing.T) {
		restoreProviderClientsForTest(t)
		migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
			return secrets.MigaduCredentials{Username: "migadu-user"}, nil
		}
		err := defaultMigaduCreateForwarding(context.Background(), "medic", "medic@inbound.lessersoul.ai")
		if err == nil || err.Error() != "migadu api key missing" {
			t.Fatalf("unexpected api key error: %v", err)
		}
	})

	t.Run("missing username", func(t *testing.T) {
		restoreProviderClientsForTest(t)
		migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
			return secrets.MigaduCredentials{APIToken: "migadu-token"}, nil
		}
		err := defaultMigaduCreateForwarding(context.Background(), "medic", "medic@inbound.lessersoul.ai")
		if err == nil || err.Error() != "migadu username missing" {
			t.Fatalf("unexpected username error: %v", err)
		}
	})
}

func TestDefaultTelnyxUpdateMessagingProfile(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		restoreProviderClientsForTest(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if got := r.URL.Path; got != "/messaging_profiles/mp-123" {
				t.Fatalf("unexpected path: %s", got)
			}
			if got := r.Header.Get("authorization"); got != "Bearer telnyx-token" {
				t.Fatalf("unexpected auth header: %q", got)
			}
			var body telnyxUpdateMessagingProfileRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.WebhookURL != "https://lab.lesser.host/webhooks/comm/sms/inbound" {
				t.Fatalf("unexpected body: %#v", body)
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		defer server.Close()

		telnyxCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.TelnyxCredentials, error) {
			return secrets.TelnyxCredentials{APIKey: "telnyx-token", MessagingProfileID: "mp-123"}, nil
		}
		telnyxAPIBaseURL = server.URL
		newHTTPClient = server.Client

		if err := defaultTelnyxUpdateMessagingProfile(context.Background(), "https://lab.lesser.host/webhooks/comm/sms/inbound"); err != nil {
			t.Fatalf("defaultTelnyxUpdateMessagingProfile: %v", err)
		}
	})

	t.Run("provider failure is surfaced", func(t *testing.T) {
		restoreProviderClientsForTest(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad profile", http.StatusBadRequest)
		}))
		defer server.Close()

		telnyxCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.TelnyxCredentials, error) {
			return secrets.TelnyxCredentials{APIKey: "telnyx-token", MessagingProfileID: "mp-123"}, nil
		}
		telnyxAPIBaseURL = server.URL
		newHTTPClient = server.Client

		err := defaultTelnyxUpdateMessagingProfile(context.Background(), "https://lab.lesser.host/webhooks/comm/sms/inbound")
		if err == nil || err.Error() != `telnyx messaging profile update: status=400 body="bad profile"` {
			t.Fatalf("unexpected telnyx error: %v", err)
		}
	})
}

func TestDefaultTelnyxUpdateMessagingProfileCredentialValidation(t *testing.T) {
	t.Run("loader error", func(t *testing.T) {
		restoreProviderClientsForTest(t)
		telnyxCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.TelnyxCredentials, error) {
			return secrets.TelnyxCredentials{}, errors.New("boom")
		}
		err := defaultTelnyxUpdateMessagingProfile(context.Background(), "https://lab.lesser.host/webhooks/comm/sms/inbound")
		if err == nil || err.Error() != "boom" {
			t.Fatalf("unexpected loader error: %v", err)
		}
	})

	t.Run("missing api key", func(t *testing.T) {
		restoreProviderClientsForTest(t)
		telnyxCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.TelnyxCredentials, error) {
			return secrets.TelnyxCredentials{MessagingProfileID: "mp-123"}, nil
		}
		err := defaultTelnyxUpdateMessagingProfile(context.Background(), "https://lab.lesser.host/webhooks/comm/sms/inbound")
		if err == nil || err.Error() != "telnyx api key missing" {
			t.Fatalf("unexpected api key error: %v", err)
		}
	})

	t.Run("missing messaging profile id", func(t *testing.T) {
		restoreProviderClientsForTest(t)
		telnyxCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.TelnyxCredentials, error) {
			return secrets.TelnyxCredentials{APIKey: "telnyx-token"}, nil
		}
		err := defaultTelnyxUpdateMessagingProfile(context.Background(), "https://lab.lesser.host/webhooks/comm/sms/inbound")
		if err == nil || err.Error() != "telnyx messaging_profile_id missing" {
			t.Fatalf("unexpected messaging profile error: %v", err)
		}
	})
}

func restoreProviderClientsForTest(t *testing.T) {
	t.Helper()

	origMigaduLoader := migaduCredsLoader
	origTelnyxLoader := telnyxCredsLoader
	origMigaduBaseURL := migaduAPIBaseURL
	origTelnyxBaseURL := telnyxAPIBaseURL
	origClientFactory := newHTTPClient
	t.Cleanup(func() {
		migaduCredsLoader = origMigaduLoader
		telnyxCredsLoader = origTelnyxLoader
		migaduAPIBaseURL = origMigaduBaseURL
		telnyxAPIBaseURL = origTelnyxBaseURL
		newHTTPClient = origClientFactory
	})
}

func TestListChannelsByType_IgnoresMissingSingleAgentChannel(t *testing.T) {
	ctx := context.Background()
	tdb := newBackfillM12TestDB()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()

	items, err := listChannelsByType(ctx, store.New(tdb.db), "0xagent", models.SoulChannelTypePhone)
	if err != nil {
		t.Fatalf("listChannelsByType: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %#v", items)
	}
}
