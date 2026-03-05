package commworker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	hostsecrets "github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	migaduSMTPHost = "smtp.migadu.com"
	migaduSMTPPort = "587"
)

type stsAPI interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

type secretsManagerAPI interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// Server processes inbound communication events from provider webhooks and delivers them to lesser instances.
type Server struct {
	cfg   config.Config
	store commStore

	sts     stsAPI
	secrets secretsManagerAPI

	ssmGetParameter func(ctx context.Context, name string) (string, error)
	migaduSendSMTP  func(ctx context.Context, username string, password string, from string, recipients []string, data []byte) error

	fetchInstanceKeyPlaintext func(ctx context.Context, inst *models.Instance) (string, error)
	deliverNotification       func(ctx context.Context, deliverURL string, apiKey string, notif InboundNotification) error

	now func() time.Time
}

// NewServer constructs a comm-worker server.
func NewServer(cfg config.Config, st commStore, stsClient stsAPI, secretsClient secretsManagerAPI) *Server {
	s := &Server{
		cfg:     cfg,
		store:   st,
		sts:     stsClient,
		secrets: secretsClient,
		now: func() time.Time {
			return time.Now().UTC()
		},
		ssmGetParameter: func(ctx context.Context, name string) (string, error) {
			return hostsecrets.GetSSMParameter(ctx, nil, name)
		},
		migaduSendSMTP:  defaultMigaduSendSMTP,
	}
	s.fetchInstanceKeyPlaintext = s.defaultFetchInstanceKeyPlaintext
	s.deliverNotification = defaultDeliverNotification
	return s
}

// Register registers SQS handlers with the provided app.
func (s *Server) Register(app *apptheory.App) {
	if s == nil || app == nil {
		return
	}

	queueName := sqsQueueNameFromURL(s.cfg.CommQueueURL)
	if queueName != "" {
		app.SQS(queueName, s.handleCommQueueMessage)
	}
}

func (s *Server) handleCommQueueMessage(ctx *apptheory.EventContext, msg events.SQSMessage) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}
	if ctx == nil {
		return fmt.Errorf("event context is nil")
	}

	var job QueueMessage
	if err := json.Unmarshal([]byte(msg.Body), &job); err != nil {
		return nil // drop invalid
	}
	if strings.TrimSpace(job.Kind) != QueueMessageKindInbound {
		return nil
	}
	if err := job.Validate(); err != nil {
		return nil // drop invalid
	}

	return s.processInbound(ctx.Context(), ctx.RequestID, job)
}

func (s *Server) processInbound(ctx context.Context, requestID string, msg QueueMessage) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}

	notif := msg.Notification
	channel := strings.ToLower(strings.TrimSpace(notif.Channel))

	agentID, ok, err := s.resolveRecipient(ctx, channel, notif.To)
	if err != nil {
		return err
	}
	if !ok || agentID == "" {
		return nil
	}

	identity, ok, err := s.store.GetSoulAgentIdentity(ctx, agentID)
	if err != nil {
		return err
	}
	if !ok || identity == nil {
		return nil
	}

	status := strings.TrimSpace(identity.LifecycleStatus)
	if status == "" {
		status = strings.TrimSpace(identity.Status)
	}
	if status != models.SoulAgentStatusActive {
		_ = s.recordInboundActivity(ctx, identity.AgentID, channel, notif, "bounce", false)
		_ = s.maybeBounceEmail(ctx, identity.AgentID, status, channel, notif, 0, 0, 0, 0)
		return nil
	}

	channelType := channelRecordType(channel)
	ch, ok, err := s.store.GetSoulAgentChannel(ctx, agentID, channelType)
	if err != nil {
		return err
	}
	if !ok || ch == nil || !s.channelMatchesNotification(ch, channel, notif.To) {
		return nil
	}
	if ch.ProvisionedAt.IsZero() || !ch.DeprovisionedAt.IsZero() || strings.TrimSpace(ch.Status) != models.SoulChannelStatusActive || !ch.Verified {
		return nil
	}

	prefs, ok, err := s.store.GetSoulAgentContactPreferences(ctx, agentID)
	if err != nil {
		return err
	}
	if !ok || prefs == nil {
		prefs = defaultContactPreferences(agentID, channel)
	}

	now := s.now()
	maxHour, maxDay := inboundRateLimits(prefs, channel)
	hourCount, err := s.countInboundReceivesSince(ctx, agentID, channel, now.Add(-1*time.Hour), 250)
	if err != nil {
		return err
	}
	dayCount, err := s.countInboundReceivesSince(ctx, agentID, channel, now.Add(-24*time.Hour), 500)
	if err != nil {
		return err
	}
	if (maxHour > 0 && hourCount >= maxHour) || (maxDay > 0 && dayCount >= maxDay) {
		_ = s.recordInboundActivity(ctx, agentID, channel, notif, "bounce", false)
		_ = s.maybeBounceEmail(ctx, agentID, "rate_limited", channel, notif, maxHour, maxDay, hourCount, dayCount)
		return nil
	}

	available, nextDelivery := availabilityDecision(now, prefs)
	if !available {
		if err := s.queueInbound(ctx, agentID, channel, notif, nextDelivery); err != nil {
			return err
		}
		_ = s.recordInboundActivity(ctx, agentID, channel, notif, "receive", true)
		return nil
	}

	// Best-effort: annotate soul-to-soul sender identity.
	s.maybeAnnotateSenderSoul(ctx, &notif)

	inst, ok, err := s.resolveAgentInstance(ctx, identity)
	if err != nil {
		return err
	}
	if !ok || inst == nil {
		return nil
	}

	apiKey, err := s.fetchInstanceKeyPlaintext(ctx, inst)
	if err != nil {
		return err
	}

	deliverURL := instanceNotificationsDeliverURL(s.cfg.Stage, strings.TrimSpace(inst.HostedBaseDomain))
	if deliverURL == "" {
		return fmt.Errorf("instance delivery url is empty")
	}
	if err := s.deliverNotification(ctx, deliverURL, apiKey, notif); err != nil {
		return err
	}

	_ = s.recordInboundActivity(ctx, agentID, channel, notif, "receive", true)
	return nil
}

func (s *Server) resolveRecipient(ctx context.Context, channel string, to *InboundParty) (string, bool, error) {
	if s == nil || s.store == nil {
		return "", false, fmt.Errorf("store not initialized")
	}
	if to == nil {
		return "", false, nil
	}

	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "email":
		addr := strings.ToLower(strings.TrimSpace(to.Address))
		if addr == "" {
			return "", false, nil
		}
		return s.store.LookupAgentByEmail(ctx, addr)
	case "sms", "voice":
		num := normalizePhone(to.Number)
		if num == "" {
			return "", false, nil
		}
		return s.store.LookupAgentByPhone(ctx, num)
	default:
		return "", false, nil
	}
}

func channelRecordType(channel string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	switch channel {
	case "sms", "voice":
		return "phone"
	default:
		return channel
	}
}

func (s *Server) channelMatchesNotification(ch *models.SoulAgentChannel, channel string, to *InboundParty) bool {
	if ch == nil || to == nil {
		return false
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	switch channel {
	case "email":
		return strings.EqualFold(strings.TrimSpace(ch.Identifier), strings.ToLower(strings.TrimSpace(to.Address)))
	case "sms", "voice":
		return strings.TrimSpace(ch.Identifier) == normalizePhone(to.Number)
	default:
		return false
	}
}

func (s *Server) resolveAgentInstance(ctx context.Context, identity *models.SoulAgentIdentity) (*models.Instance, bool, error) {
	if s == nil || s.store == nil {
		return nil, false, fmt.Errorf("store not initialized")
	}
	if identity == nil {
		return nil, false, nil
	}

	domain := strings.ToLower(strings.TrimSpace(identity.Domain))
	if domain == "" {
		return nil, false, nil
	}
	d, ok, err := s.store.GetDomain(ctx, domain)
	if err != nil {
		return nil, false, err
	}
	if !ok || d == nil || strings.TrimSpace(d.InstanceSlug) == "" {
		return nil, false, nil
	}

	inst, ok, err := s.store.GetInstance(ctx, strings.TrimSpace(d.InstanceSlug))
	if err != nil {
		return nil, false, err
	}
	return inst, ok && inst != nil, nil
}

func (s *Server) countInboundReceivesSince(ctx context.Context, agentID string, channel string, since time.Time, scanLimit int) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("store not initialized")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	channel = strings.ToLower(strings.TrimSpace(channel))
	if agentID == "" || channel == "" {
		return 0, fmt.Errorf("agent and channel are required")
	}

	items, err := s.store.ListRecentCommActivities(ctx, agentID, scanLimit)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		if item.Timestamp.Before(since) {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.Direction)) != models.SoulCommDirectionInbound {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.ChannelType)) != channel {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.Action)) != "receive" {
			continue
		}
		count++
	}
	return count, nil
}

func (s *Server) recordInboundActivity(ctx context.Context, agentID string, channel string, notif InboundNotification, action string, preferenceRespected bool) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}

	receivedAt := parseRFC3339Time(notif.ReceivedAt, s.now())
	counterparty := strings.TrimSpace(notif.From.Address)
	if counterparty == "" {
		counterparty = strings.TrimSpace(notif.From.Number)
	}

	inReplyTo := ""
	if notif.InReplyTo != nil {
		inReplyTo = strings.TrimSpace(*notif.InReplyTo)
	}

	pref := preferenceRespected
	act := &models.SoulAgentCommActivity{
		AgentID:            strings.ToLower(strings.TrimSpace(agentID)),
		ActivityID:         fmt.Sprintf("%s#%s", strings.TrimSpace(notif.MessageID), strings.ToLower(strings.TrimSpace(action))),
		ChannelType:        strings.ToLower(strings.TrimSpace(channel)),
		Direction:          models.SoulCommDirectionInbound,
		Counterparty:       counterparty,
		Action:             strings.ToLower(strings.TrimSpace(action)),
		MessageID:          strings.TrimSpace(notif.MessageID),
		InReplyTo:          inReplyTo,
		BoundaryCheck:      models.SoulCommBoundaryCheckSkipped,
		PreferenceRespected: &pref,
		Timestamp:          receivedAt,
	}
	_ = act.UpdateKeys()
	return s.store.PutCommActivity(ctx, act)
}

func (s *Server) queueInbound(ctx context.Context, agentID string, channel string, notif InboundNotification, scheduled time.Time) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}

	receivedAt := parseRFC3339Time(notif.ReceivedAt, s.now())
	inReplyTo := ""
	if notif.InReplyTo != nil {
		inReplyTo = strings.TrimSpace(*notif.InReplyTo)
	}

	item := &models.SoulAgentCommQueue{
		AgentID:              strings.ToLower(strings.TrimSpace(agentID)),
		MessageID:            strings.TrimSpace(notif.MessageID),
		ChannelType:          strings.ToLower(strings.TrimSpace(channel)),
		FromAddress:          strings.TrimSpace(notif.From.Address),
		FromNumber:           strings.TrimSpace(notif.From.Number),
		FromDisplayName:      strings.TrimSpace(notif.From.DisplayName),
		Subject:              strings.TrimSpace(notif.Subject),
		Body:                 strings.TrimSpace(notif.Body),
		InReplyTo:            inReplyTo,
		ReceivedAt:           receivedAt,
		ScheduledDeliveryTime: scheduled,
		Status:               models.SoulCommQueueStatusQueued,
	}
	if notif.From.SoulAgentID != nil {
		item.FromSoulAgentID = strings.TrimSpace(*notif.From.SoulAgentID)
	}
	_ = item.UpdateKeys()
	return s.store.PutCommQueue(ctx, item)
}

func defaultContactPreferences(agentID string, channel string) *models.SoulAgentContactPreferences {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	channel = strings.ToLower(strings.TrimSpace(channel))
	preferred := "email"
	if channel == "sms" || channel == "voice" {
		preferred = "sms"
	}
	return &models.SoulAgentContactPreferences{
		AgentID:               agentID,
		Preferred:             preferred,
		AvailabilitySchedule:  "always",
		AvailabilityTimezone:  "UTC",
		AvailabilityWindows:   nil,
		RateLimits:            nil,
		Languages:             []string{"en"},
		ContentTypes:          []string{"text/plain"},
		UpdatedAt:             time.Now().UTC(),
	}
}

func inboundRateLimits(prefs *models.SoulAgentContactPreferences, channel string) (int, int) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	maxHour := 0
	maxDay := 0
	switch channel {
	case "email":
		maxHour, maxDay = 50, 500
	case "sms":
		maxHour, maxDay = 20, 200
	case "voice":
		maxHour, maxDay = 0, 0
	}

	if prefs == nil || len(prefs.RateLimits) == 0 {
		return maxHour, maxDay
	}

	raw, ok := prefs.RateLimits[channel]
	if !ok {
		return maxHour, maxDay
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return maxHour, maxDay
	}
	if v, ok := asInt(m["maxInboundPerHour"]); ok && v > 0 {
		maxHour = v
	}
	if v, ok := asInt(m["maxInboundPerDay"]); ok && v > 0 {
		maxDay = v
	}
	return maxHour, maxDay
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case float32:
		return int(t), true
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func availabilityDecision(now time.Time, prefs *models.SoulAgentContactPreferences) (bool, time.Time) {
	if prefs == nil {
		return true, now
	}

	schedule := strings.ToLower(strings.TrimSpace(prefs.AvailabilitySchedule))
	if schedule == "" || schedule == "always" {
		return true, now
	}

	loc := time.UTC
	if tz := strings.TrimSpace(prefs.AvailabilityTimezone); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	localNow := now.In(loc)

	windows := prefs.AvailabilityWindows
	if schedule == "business-hours" {
		windows = []models.SoulContactAvailabilityWindow{
			{Days: []string{"mon", "tue", "wed", "thu", "fri"}, StartTime: "09:00", EndTime: "17:00"},
		}
	}
	if len(windows) == 0 {
		return true, now
	}

	if inAvailabilityWindow(localNow, windows) {
		return true, now
	}

	next := nextAvailabilityStart(localNow, windows)
	if next.IsZero() {
		return false, now.Add(24 * time.Hour)
	}
	return false, next.UTC()
}

func inAvailabilityWindow(now time.Time, windows []models.SoulContactAvailabilityWindow) bool {
	day := weekdayAbbrev(now.Weekday())
	curMin := now.Hour()*60 + now.Minute()
	for _, w := range windows {
		if !dayInWindow(day, w.Days) {
			continue
		}
		startMin, okStart := parseHHMMMinutes(w.StartTime)
		endMin, okEnd := parseHHMMMinutes(w.EndTime)
		if !okStart || !okEnd {
			continue
		}
		if endMin > startMin {
			if curMin >= startMin && curMin < endMin {
				return true
			}
			continue
		}
		// Overnight window (e.g. 22:00–02:00).
		if curMin >= startMin || curMin < endMin {
			return true
		}
	}
	return false
}

func nextAvailabilityStart(now time.Time, windows []models.SoulContactAvailabilityWindow) time.Time {
	for i := 0; i < 8; i++ {
		d := now.AddDate(0, 0, i)
		day := weekdayAbbrev(d.Weekday())
		for _, w := range windows {
			if !dayInWindow(day, w.Days) {
				continue
			}
			startMin, ok := parseHHMMMinutes(w.StartTime)
			if !ok {
				continue
			}
			start := time.Date(d.Year(), d.Month(), d.Day(), startMin/60, startMin%60, 0, 0, d.Location())
			if start.After(now) {
				return start
			}
		}
	}
	return time.Time{}
}

func weekdayAbbrev(wd time.Weekday) string {
	switch wd {
	case time.Monday:
		return "mon"
	case time.Tuesday:
		return "tue"
	case time.Wednesday:
		return "wed"
	case time.Thursday:
		return "thu"
	case time.Friday:
		return "fri"
	case time.Saturday:
		return "sat"
	default:
		return "sun"
	}
}

func dayInWindow(day string, days []string) bool {
	day = strings.ToLower(strings.TrimSpace(day))
	for _, d := range days {
		if strings.ToLower(strings.TrimSpace(d)) == day {
			return true
		}
	}
	return false
}

func parseHHMMMinutes(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	h, errH := parseSmallInt(parts[0])
	m, errM := parseSmallInt(parts[1])
	if errH != nil || errM != nil {
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func parseSmallInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, fmt.Errorf("invalid")
		}
		n = n*10 + int(raw[i]-'0')
	}
	return n, nil
}

func parseRFC3339Time(raw string, fallback time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback.UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	return fallback.UTC()
}

func normalizePhone(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, "-", "")
	raw = strings.ReplaceAll(raw, "(", "")
	raw = strings.ReplaceAll(raw, ")", "")
	raw = strings.ReplaceAll(raw, ".", "")
	return raw
}

func (s *Server) maybeAnnotateSenderSoul(ctx context.Context, notif *InboundNotification) {
	if s == nil || s.store == nil || notif == nil {
		return
	}
	if notif.From.SoulAgentID != nil && strings.TrimSpace(*notif.From.SoulAgentID) != "" {
		return
	}

	switch strings.ToLower(strings.TrimSpace(notif.Channel)) {
	case "email":
		addr := strings.ToLower(strings.TrimSpace(notif.From.Address))
		if addr == "" {
			return
		}
		id, ok, err := s.store.LookupAgentByEmail(ctx, addr)
		if err == nil && ok && id != "" {
			notif.From.SoulAgentID = &id
		}
	case "sms", "voice":
		num := normalizePhone(notif.From.Number)
		if num == "" {
			return
		}
		id, ok, err := s.store.LookupAgentByPhone(ctx, num)
		if err == nil && ok && id != "" {
			notif.From.SoulAgentID = &id
		}
	}
}

func instanceStageForControlPlane(stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	switch stage {
	case "live", "prod", "production":
		return "live"
	case "staging", "stage":
		return "staging"
	default:
		return "dev"
	}
}

func instanceStageDomain(controlPlaneStage string, baseDomain string) string {
	base := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if base == "" {
		return ""
	}
	stage := instanceStageForControlPlane(controlPlaneStage)
	if stage == "live" {
		return base
	}
	return fmt.Sprintf("%s.%s", stage, base)
}

func instanceNotificationsDeliverURL(controlPlaneStage string, baseDomain string) string {
	stageDomain := instanceStageDomain(controlPlaneStage, baseDomain)
	if stageDomain == "" {
		return ""
	}
	return fmt.Sprintf("https://api.%s/api/v1/notifications/deliver", stageDomain)
}

func (s *Server) defaultFetchInstanceKeyPlaintext(ctx context.Context, inst *models.Instance) (string, error) {
	if s == nil {
		return "", fmt.Errorf("server not initialized")
	}
	if inst == nil {
		return "", fmt.Errorf("instance is nil")
	}
	secretArn := strings.TrimSpace(inst.LesserHostInstanceKeySecretARN)
	if secretArn == "" {
		return "", fmt.Errorf("instance api key secret arn is not configured")
	}
	if s.secrets == nil {
		return "", fmt.Errorf("secrets manager client not initialized")
	}

	accountID := strings.TrimSpace(inst.HostedAccountID)
	roleName := strings.TrimSpace(s.cfg.ManagedInstanceRoleName)
	region := strings.TrimSpace(inst.HostedRegion)
	if region == "" {
		region = strings.TrimSpace(s.cfg.ManagedDefaultRegion)
	}
	if region == "" {
		region = "us-east-1"
	}

	// If the instance account details are missing, fall back to same-account access.
	if accountID == "" || roleName == "" || s.sts == nil {
		return getSecretsManagerSecretPlaintext(ctx, s.secrets, secretArn)
	}

	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)
	sessionName := fmt.Sprintf("lesser-host-%s-comm-%s", strings.TrimSpace(s.cfg.Stage), strings.TrimSpace(inst.Slug))
	if len(sessionName) > 64 {
		sessionName = sessionName[:64]
	}

	assumed, err := s.sts.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String(sessionName),
		DurationSeconds: aws.Int32(900),
	})
	if err != nil {
		return "", fmt.Errorf("assume instance role: %w", err)
	}
	if assumed == nil || assumed.Credentials == nil {
		return "", fmt.Errorf("assume role returned empty credentials")
	}

	creds := credentials.NewStaticCredentialsProvider(
		aws.ToString(assumed.Credentials.AccessKeyId),
		aws.ToString(assumed.Credentials.SecretAccessKey),
		aws.ToString(assumed.Credentials.SessionToken),
	)
	child := secretsmanager.New(secretsmanager.Options{
		Region:      region,
		Credentials: aws.NewCredentialsCache(creds),
	})

	return getSecretsManagerSecretPlaintext(ctx, child, secretArn)
}

func getSecretsManagerSecretPlaintext(ctx context.Context, sm secretsManagerAPI, secretArn string) (string, error) {
	secretArn = strings.TrimSpace(secretArn)
	if secretArn == "" {
		return "", fmt.Errorf("secret arn is required")
	}
	if sm == nil {
		return "", fmt.Errorf("secrets manager client not initialized")
	}

	out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretArn)})
	if err != nil {
		return "", fmt.Errorf("get secret value: %w", err)
	}

	raw := strings.TrimSpace(aws.ToString(out.SecretString))
	if raw == "" && len(out.SecretBinary) > 0 {
		raw = strings.TrimSpace(string(out.SecretBinary))
	}
	plaintext, err := unwrapSecretsManagerSecretString(raw)
	if err != nil {
		return "", fmt.Errorf("parse secret value: %w", err)
	}
	return plaintext, nil
}

func unwrapSecretsManagerSecretString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("secret value is empty")
	}
	if strings.HasPrefix(raw, "{") {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return "", fmt.Errorf("unmarshal secret string: %w", err)
		}
		val := strings.TrimSpace(parsed["secret"])
		if val == "" {
			return "", fmt.Errorf("secret payload missing 'secret' key")
		}
		return val, nil
	}
	return raw, nil
}

func defaultDeliverNotification(ctx context.Context, deliverURL string, apiKey string, notif InboundNotification) error {
	deliverURL = strings.TrimSpace(deliverURL)
	apiKey = strings.TrimSpace(apiKey)
	if deliverURL == "" || apiKey == "" {
		return fmt.Errorf("deliverURL and apiKey are required")
	}

	body, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deliverURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("deliver: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("deliver: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(msg)))
}

func (s *Server) maybeBounceEmail(ctx context.Context, agentID string, reason string, channel string, notif InboundNotification, maxHour int, maxDay int, hourCount int, dayCount int) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}
	if strings.ToLower(strings.TrimSpace(channel)) != "email" {
		return nil
	}

	sender := strings.TrimSpace(notif.From.Address)
	if sender == "" {
		return nil
	}

	ch, ok, err := s.store.GetSoulAgentChannel(ctx, agentID, "email")
	if err != nil || !ok || ch == nil {
		return nil
	}
	fromMailbox := strings.TrimSpace(ch.Identifier)
	passParam := strings.TrimSpace(ch.SecretRef)
	if passParam == "" {
		passParam = soulAgentEmailPasswordSSMParam(s.cfg.Stage, agentID)
	}
	if strings.TrimSpace(passParam) == "" || s.ssmGetParameter == nil || s.migaduSendSMTP == nil {
		return nil
	}
	password, err := s.ssmGetParameter(ctx, passParam)
	if err != nil || strings.TrimSpace(password) == "" {
		return nil
	}

	subject := "Message rejected"
	body := buildBounceBody(reason, fromMailbox, maxHour, maxDay, hourCount, dayCount, notif.MessageID, requestIDFromContext(ctx))
	msgID := buildBounceMessageID(fromMailbox, notif.MessageID)

	data := buildPlaintextEmailRFC5322(fromMailbox, sender, subject, body, msgID, notif.MessageID)
	return s.migaduSendSMTP(ctx, fromMailbox, strings.TrimSpace(password), fromMailbox, []string{sender}, data)
}

func requestIDFromContext(_ context.Context) string { return "" }

func soulAgentEmailPasswordSSMParam(stage string, agentIDHex string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage == "" {
		stage = "lab"
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	// #nosec G101 -- SSM parameter path, not a hardcoded credential.
	return fmt.Sprintf("/lesser-host/soul/%s/agents/%s/channels/email/migadu_password", stage, agentIDHex)
}

func buildBounceBody(reason string, to string, maxHour int, maxDay int, hourCount int, dayCount int, messageID string, requestID string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unavailable"
	}

	var b strings.Builder
	b.WriteString("Your message could not be delivered.\n\n")
	b.WriteString("Recipient: ")
	b.WriteString(strings.TrimSpace(to))
	b.WriteString("\n")
	b.WriteString("Reason: ")
	b.WriteString(reason)
	b.WriteString("\n")
	if maxHour > 0 || maxDay > 0 {
		b.WriteString("\nRate limits:\n")
		if maxHour > 0 {
			b.WriteString(fmt.Sprintf("- maxInboundPerHour: %d (current=%d)\n", maxHour, hourCount))
		}
		if maxDay > 0 {
			b.WriteString(fmt.Sprintf("- maxInboundPerDay: %d (current=%d)\n", maxDay, dayCount))
		}
		b.WriteString("\nPlease try again later.\n")
	}
	if strings.TrimSpace(messageID) != "" {
		b.WriteString("\nMessage ID: ")
		b.WriteString(strings.TrimSpace(messageID))
		b.WriteString("\n")
	}
	if strings.TrimSpace(requestID) != "" {
		b.WriteString("Request ID: ")
		b.WriteString(strings.TrimSpace(requestID))
		b.WriteString("\n")
	}
	return b.String()
}

func buildBounceMessageID(fromMailbox string, inReplyTo string) string {
	token, err := newToken(8)
	if err != nil {
		sum := sha256.Sum256([]byte(strings.TrimSpace(fromMailbox) + "|" + strings.TrimSpace(inReplyTo) + "|" + time.Now().UTC().Format(time.RFC3339Nano)))
		token = hex.EncodeToString(sum[:])[:16]
	}
	return fmt.Sprintf("<comm-bounce-%s@lessersoul.ai>", token)
}

func buildPlaintextEmailRFC5322(from string, to string, subject string, body string, messageID string, inReplyTo string) []byte {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	subject = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(subject), "\r", " "), "\n", " ")
	body = strings.TrimSpace(body)
	if body != "" {
		body = strings.ReplaceAll(body, "\r\n", "\n")
		body = strings.ReplaceAll(body, "\r", "\n")
		body = strings.ReplaceAll(body, "\n", "\r\n")
		if !strings.HasSuffix(body, "\r\n") {
			body += "\r\n"
		}
	}

	var b bytes.Buffer
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	if subject != "" {
		b.WriteString("Subject: " + subject + "\r\n")
	}
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	if strings.TrimSpace(messageID) != "" {
		b.WriteString("Message-ID: " + strings.TrimSpace(messageID) + "\r\n")
	}
	if strings.TrimSpace(inReplyTo) != "" {
		b.WriteString("In-Reply-To: " + strings.TrimSpace(inReplyTo) + "\r\n")
		b.WriteString("References: " + strings.TrimSpace(inReplyTo) + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.Bytes()
}

func newToken(nBytes int) (string, error) {
	if nBytes <= 0 {
		nBytes = 8
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func defaultMigaduSendSMTP(ctx context.Context, username string, password string, from string, recipients []string, data []byte) error {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	from = strings.TrimSpace(from)
	if username == "" || password == "" || from == "" {
		return fmt.Errorf("smtp username/password/from required")
	}
	if len(recipients) == 0 {
		return fmt.Errorf("smtp recipients required")
	}
	if len(data) == 0 {
		return fmt.Errorf("smtp data required")
	}

	addr := net.JoinHostPort(migaduSMTPHost, migaduSMTPPort)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}

	c, err := smtp.NewClient(conn, migaduSMTPHost)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{
			ServerName: migaduSMTPHost,
			MinVersion: tls.VersionTLS12,
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

	if ok, _ := c.Extension("AUTH"); ok {
		auth := smtp.PlainAuth("", username, password, migaduSMTPHost)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, rcpt := range recipients {
		rcpt = strings.TrimSpace(rcpt)
		if rcpt == "" {
			continue
		}
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %q: %w", rcpt, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

func sqsQueueNameFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
