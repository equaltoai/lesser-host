package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	migaduBaseURL     = "https://api.migadu.com/v1"
	migaduEmailDomain = "lessersoul.ai"
	telnyxBaseURL     = "https://api.telnyx.com/v2"
)

var (
	migaduCredsLoader = secrets.MigaduCreds
	telnyxCredsLoader = secrets.TelnyxCreds
	migaduAPIBaseURL  = migaduBaseURL
	telnyxAPIBaseURL  = telnyxBaseURL
	newHTTPClient     = func() *http.Client {
		return &http.Client{Timeout: 10 * time.Second}
	}
)

type config struct {
	agentID            string
	apply              bool
	backfillEmail      bool
	backfillPhone      bool
	publicBaseURL      string
	emailInboundDomain string
}

type emailTarget struct {
	AgentID   string
	LocalPart string
	Address   string
}

type channelBackfillSummary struct {
	Scanned          int
	Eligible         int
	ProviderUpdates  int
	Skipped          int
	Errors           int
	EligibleChannels []string
}

type providerClients struct {
	migaduCreateForwarding func(ctx context.Context, localPart string, address string) error
	telnyxUpdateProfile    func(ctx context.Context, webhookURL string) error
}

type migaduCreateForwardingRequest struct {
	Address string `json:"address"`
}

type telnyxUpdateMessagingProfileRequest struct {
	WebhookURL string `json:"webhook_url"`
}

func main() {
	cfg := parseConfig()

	mode := "dry-run"
	if cfg.apply {
		mode = "apply"
	}
	fmt.Fprintf(
		os.Stdout,
		"soul-backfill-m12-channel-inbound-routing mode=%s table=%s base_url=%s agent=%s\n",
		mode,
		models.MainTableName(),
		cfg.publicBaseURL,
		emptyDefault(cfg.agentID, "all"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	db, err := store.LambdaInit()
	if err != nil {
		die("init store: %v", err)
	}
	st := store.New(db)

	clients := providerClients{
		migaduCreateForwarding: defaultMigaduCreateForwarding,
		telnyxUpdateProfile:    defaultTelnyxUpdateMessagingProfile,
	}

	exitCode := 0
	if cfg.backfillEmail {
		emailSummary, err := backfillEmailInboundRouting(ctx, st, clients, cfg.agentID, cfg.emailInboundDomain, cfg.apply)
		if err != nil {
			die("email backfill: %v", err)
		}
		fmt.Fprintf(
			os.Stdout,
			"email scanned=%d eligible=%d provider_updates=%d skipped=%d errors=%d\n",
			emailSummary.Scanned,
			emailSummary.Eligible,
			emailSummary.ProviderUpdates,
			emailSummary.Skipped,
			emailSummary.Errors,
		)
		for _, target := range emailSummary.EligibleChannels {
			fmt.Fprintf(os.Stdout, "  email target=%s\n", target)
		}
		if emailSummary.Errors > 0 {
			exitCode = 2
		}
	}

	if cfg.backfillPhone {
		phoneSummary, err := backfillPhoneInboundRouting(ctx, st, clients, cfg.agentID, cfg.publicBaseURL, cfg.apply)
		if err != nil {
			die("phone backfill: %v", err)
		}
		fmt.Fprintf(
			os.Stdout,
			"phone scanned=%d eligible=%d provider_updates=%d skipped=%d errors=%d\n",
			phoneSummary.Scanned,
			phoneSummary.Eligible,
			phoneSummary.ProviderUpdates,
			phoneSummary.Skipped,
			phoneSummary.Errors,
		)
		for _, target := range phoneSummary.EligibleChannels {
			fmt.Fprintf(os.Stdout, "  phone target=%s\n", target)
		}
		if phoneSummary.Errors > 0 {
			exitCode = 2
		}
	}

	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func parseConfig() config {
	var cfg config
	flag.StringVar(&cfg.agentID, "agent-id", "", "Optional target agent id (0x... 32-byte hex)")
	flag.BoolVar(&cfg.apply, "apply", false, "Apply provider updates (default: dry-run)")
	flag.BoolVar(&cfg.backfillEmail, "email", true, "Backfill Migadu inbound forwarding")
	flag.BoolVar(&cfg.backfillPhone, "phone", true, "Backfill Telnyx messaging profile webhook")
	flag.StringVar(&cfg.publicBaseURL, "public-base-url", strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "Public base URL, e.g. https://lab.lesser.host")
	flag.StringVar(&cfg.emailInboundDomain, "email-inbound-domain", strings.TrimSpace(os.Getenv("SOUL_EMAIL_INBOUND_DOMAIN")), "Inbound bridge email domain, e.g. inbound.lessersoul.ai")
	flag.Parse()

	cfg.agentID = strings.ToLower(strings.TrimSpace(cfg.agentID))
	cfg.publicBaseURL = normalizePublicBaseURL(cfg.publicBaseURL)
	cfg.emailInboundDomain = strings.ToLower(strings.TrimSpace(cfg.emailInboundDomain))

	if !cfg.backfillEmail && !cfg.backfillPhone {
		die("must enable at least one of --email or --phone")
	}
	if strings.TrimSpace(os.Getenv("STATE_TABLE_NAME")) == "" {
		die("STATE_TABLE_NAME is required")
	}
	if cfg.backfillPhone && cfg.publicBaseURL == "" {
		die("public base URL is required; set --public-base-url or PUBLIC_BASE_URL")
	}
	if cfg.backfillEmail && cfg.emailInboundDomain == "" {
		die("email inbound domain is required; set --email-inbound-domain or SOUL_EMAIL_INBOUND_DOMAIN")
	}
	return cfg
}

func backfillEmailInboundRouting(
	ctx context.Context,
	st *store.Store,
	clients providerClients,
	agentID string,
	emailInboundDomain string,
	apply bool,
) (channelBackfillSummary, error) {
	if st == nil || st.DB == nil {
		return channelBackfillSummary{}, errors.New("store is not configured")
	}
	if clients.migaduCreateForwarding == nil {
		return channelBackfillSummary{}, errors.New("migadu forwarding client is not configured")
	}

	items, err := listChannelsByType(ctx, st, agentID, models.SoulChannelTypeEmail)
	if err != nil {
		return channelBackfillSummary{}, err
	}

	summary := channelBackfillSummary{}
	for _, item := range items {
		if item == nil {
			continue
		}
		summary.Scanned++
		target, ok := resolveEmailBackfillTarget(item)
		if !ok {
			summary.Skipped++
			continue
		}
		summary.Eligible++
		forwardingAddress := target.LocalPart + "@" + strings.ToLower(strings.TrimSpace(emailInboundDomain))
		summary.EligibleChannels = append(summary.EligibleChannels, target.Address+" -> "+forwardingAddress)
		if !apply {
			continue
		}
		if err := clients.migaduCreateForwarding(ctx, target.LocalPart, forwardingAddress); err != nil {
			summary.Errors++
			fmt.Fprintf(os.Stdout, "warn email backfill failed agent=%s address=%s err=%v\n", target.AgentID, target.Address, err)
			continue
		}
		summary.ProviderUpdates++
	}
	return summary, nil
}

func backfillPhoneInboundRouting(
	ctx context.Context,
	st *store.Store,
	clients providerClients,
	agentID string,
	publicBaseURL string,
	apply bool,
) (channelBackfillSummary, error) {
	if st == nil || st.DB == nil {
		return channelBackfillSummary{}, errors.New("store is not configured")
	}
	if clients.telnyxUpdateProfile == nil {
		return channelBackfillSummary{}, errors.New("telnyx update client is not configured")
	}

	items, err := listChannelsByType(ctx, st, agentID, models.SoulChannelTypePhone)
	if err != nil {
		return channelBackfillSummary{}, err
	}

	summary := channelBackfillSummary{}
	for _, item := range items {
		if item == nil {
			continue
		}
		summary.Scanned++
		if !shouldBackfillPhoneChannel(item) {
			summary.Skipped++
			continue
		}
		summary.Eligible++
		summary.EligibleChannels = append(summary.EligibleChannels, strings.TrimSpace(item.Identifier))
	}
	if !apply || summary.Eligible == 0 {
		return summary, nil
	}

	webhookURL := publicBaseURL + "/webhooks/comm/sms/inbound"
	if err := clients.telnyxUpdateProfile(ctx, webhookURL); err != nil {
		summary.Errors++
		fmt.Fprintf(os.Stdout, "warn phone backfill failed webhook=%s err=%v\n", webhookURL, err)
		return summary, nil
	}
	summary.ProviderUpdates = 1
	return summary, nil
}

func listChannelsByType(ctx context.Context, st *store.Store, agentID string, channelType string) ([]*models.SoulAgentChannel, error) {
	if st == nil || st.DB == nil {
		return nil, errors.New("store is not configured")
	}
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if channelType == "" {
		return nil, errors.New("channel type is required")
	}

	if agentID != "" {
		item, err := loadChannel(ctx, st, agentID, channelType)
		if theoryErrors.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if item == nil {
			return nil, nil
		}
		return []*models.SoulAgentChannel{item}, nil
	}

	var items []*models.SoulAgentChannel
	err := st.DB.WithContext(ctx).
		Model(&models.SoulAgentChannel{}).
		Where("SK", "=", "CHANNEL#"+channelType).
		All(&items)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func loadChannel(ctx context.Context, st *store.Store, agentID string, channelType string) (*models.SoulAgentChannel, error) {
	if st == nil || st.DB == nil {
		return nil, errors.New("store is not configured")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if agentID == "" || channelType == "" {
		return nil, errors.New("agent id and channel type are required")
	}

	var item models.SoulAgentChannel
	err := st.DB.WithContext(ctx).
		Model(&models.SoulAgentChannel{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", "CHANNEL#"+channelType).
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func resolveEmailBackfillTarget(item *models.SoulAgentChannel) (emailTarget, bool) {
	if !shouldBackfillEmailChannel(item) {
		return emailTarget{}, false
	}

	address := strings.ToLower(strings.TrimSpace(item.Identifier))
	localPart, domain, ok := strings.Cut(address, "@")
	if !ok || strings.TrimSpace(localPart) == "" || !strings.EqualFold(strings.TrimSpace(domain), migaduEmailDomain) {
		return emailTarget{}, false
	}

	return emailTarget{
		AgentID:   strings.TrimSpace(item.AgentID),
		LocalPart: strings.TrimSpace(localPart),
		Address:   address,
	}, true
}

func shouldBackfillEmailChannel(item *models.SoulAgentChannel) bool {
	if item == nil {
		return false
	}
	if strings.TrimSpace(item.Identifier) == "" || item.ProvisionedAt.IsZero() || !item.DeprovisionedAt.IsZero() {
		return false
	}
	if strings.ToLower(strings.TrimSpace(item.ChannelType)) != models.SoulChannelTypeEmail {
		return false
	}
	if strings.ToLower(strings.TrimSpace(item.Provider)) != "migadu" {
		return false
	}
	return strings.ToLower(strings.TrimSpace(item.Status)) == models.SoulChannelStatusActive
}

func shouldBackfillPhoneChannel(item *models.SoulAgentChannel) bool {
	if item == nil {
		return false
	}
	if strings.TrimSpace(item.Identifier) == "" || item.ProvisionedAt.IsZero() || !item.DeprovisionedAt.IsZero() {
		return false
	}
	if strings.ToLower(strings.TrimSpace(item.ChannelType)) != models.SoulChannelTypePhone {
		return false
	}
	if strings.ToLower(strings.TrimSpace(item.Provider)) != "telnyx" {
		return false
	}
	return strings.ToLower(strings.TrimSpace(item.Status)) == models.SoulChannelStatusActive
}

func normalizePublicBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://") {
		return ""
	}
	return raw
}

func defaultMigaduCreateForwarding(ctx context.Context, localPart string, address string) error {
	localPart = strings.TrimSpace(localPart)
	address = strings.TrimSpace(address)
	if localPart == "" || address == "" {
		return fmt.Errorf("migadu forwarding localPart and address are required")
	}

	creds, err := migaduCredsLoader(ctx, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(creds.APIToken) == "" {
		return fmt.Errorf("migadu api key missing")
	}
	if strings.TrimSpace(creds.Username) == "" {
		return fmt.Errorf("migadu username missing")
	}

	body, err := json.Marshal(migaduCreateForwardingRequest{Address: address})
	if err != nil {
		return fmt.Errorf("migadu forwarding encode: %w", err)
	}

	u := migaduAPIBaseURL + "/domains/" + migaduEmailDomain + "/mailboxes/" + localPart + "/forwardings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("migadu forwarding build: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.SetBasicAuth(strings.TrimSpace(creds.Username), strings.TrimSpace(creds.APIToken))

	client := newHTTPClient()
	//nolint:gosec // Request target is the fixed Migadu HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("migadu forwarding: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		return nil
	}

	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("migadu forwarding: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(msg)))
}

func defaultTelnyxUpdateMessagingProfile(ctx context.Context, webhookURL string) error {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return fmt.Errorf("telnyx webhookURL is required")
	}

	creds, err := telnyxCredsLoader(ctx, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(creds.APIKey) == "" {
		return fmt.Errorf("telnyx api key missing")
	}
	if strings.TrimSpace(creds.MessagingProfileID) == "" {
		return fmt.Errorf("telnyx messaging_profile_id missing")
	}

	reqBody, err := json.Marshal(telnyxUpdateMessagingProfileRequest{WebhookURL: webhookURL})
	if err != nil {
		return fmt.Errorf("telnyx messaging profile encode: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPatch,
		telnyxAPIBaseURL+"/messaging_profiles/"+strings.TrimSpace(creds.MessagingProfileID),
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return fmt.Errorf("telnyx messaging profile build: %w", err)
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+strings.TrimSpace(creds.APIKey))

	client := newHTTPClient()
	//nolint:gosec // Request target is the fixed Telnyx HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telnyx messaging profile update: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		return nil
	}
	return fmt.Errorf("telnyx messaging profile update: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
}

func emptyDefault(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func die(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, msg+"\n", args...)
	os.Exit(1)
}
