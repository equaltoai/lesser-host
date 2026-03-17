package commworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	commTestAgentEmail   = "agent@example.com"
	commTestSMTPPassword = "smtp-pass"
	commTestSenderSoulID = "0xsender"
)

type stubSecretsManager struct {
	get func(context.Context, *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error)
}

func (s stubSecretsManager) GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if s.get == nil {
		return nil, errors.New("missing get stub")
	}
	return s.get(ctx, in)
}

func TestQueueMessageValidation(t *testing.T) {
	t.Parallel()

	if err := (*QueueMessage)(nil).Validate(); err == nil {
		t.Fatalf("expected nil message error")
	}
	if err := (&QueueMessage{Kind: "other"}).Validate(); err == nil {
		t.Fatalf("expected unsupported kind error")
	}
	if err := (&InboundNotification{}).Validate(); err == nil {
		t.Fatalf("expected invalid notification error")
	}
	if err := (&InboundParty{}).Validate(); err == nil {
		t.Fatalf("expected invalid party error")
	}

	msg := QueueMessage{
		Kind: QueueMessageKindInbound,
		Notification: InboundNotification{
			Type:       "communication:inbound",
			Channel:    "email",
			From:       InboundParty{Address: "alice@example.com"},
			To:         &InboundParty{Address: commTestAgentEmail},
			Subject:    "Hello",
			Body:       "World",
			ReceivedAt: "2026-03-05T12:00:00Z",
			MessageID:  "msg-1",
		},
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	msg.Notification.Subject = ""
	if err := msg.Validate(); err == nil {
		t.Fatalf("expected subject required error")
	}
}

func TestCommWorkerPureHelpers(t *testing.T) {
	t.Parallel()

	testDefaultContactPreferences(t)
	testInboundRateLimits(t)
	testAvailabilityHelpers(t)
	testTimeAndPhoneHelpers(t)
	testStageAndQueueHelpers(t)
}

func testDefaultContactPreferences(t *testing.T) {
	t.Helper()

	prefs := defaultContactPreferences(" AGENT ", "voice")
	if prefs.AgentID != "agent" || prefs.Preferred != "sms" {
		t.Fatalf("unexpected default prefs: %#v", prefs)
	}
}

func testInboundRateLimits(t *testing.T) {
	t.Helper()

	hour, day := inboundRateLimits(nil, "email")
	if hour != 50 || day != 500 {
		t.Fatalf("unexpected default email rate limits: %d %d", hour, day)
	}

	hour, day = inboundRateLimits(&models.SoulAgentContactPreferences{
		RateLimits: map[string]any{
			"sms": map[string]any{
				"maxInboundPerHour": json.Number("7"),
				"maxInboundPerDay":  float64(30),
			},
		},
	}, "sms")
	if hour != 7 || day != 30 {
		t.Fatalf("unexpected custom rate limits: %d %d", hour, day)
	}
	if _, ok := asInt("bad"); ok {
		t.Fatalf("expected asInt failure")
	}
}

func testAvailabilityHelpers(t *testing.T) {
	t.Helper()

	now := time.Date(2026, 3, 2, 8, 30, 0, 0, time.UTC)
	available, next := availabilityDecision(now, &models.SoulAgentContactPreferences{
		AvailabilitySchedule: "business-hours",
		AvailabilityTimezone: "UTC",
	})
	if available || next.UTC().Format(time.RFC3339) != "2026-03-02T09:00:00Z" {
		t.Fatalf("unexpected availability decision: %v %s", available, next.UTC().Format(time.RFC3339))
	}
	available, _ = availabilityDecision(now, &models.SoulAgentContactPreferences{AvailabilitySchedule: "always"})
	if !available {
		t.Fatalf("expected always schedule to be available")
	}

	windows := []models.SoulContactAvailabilityWindow{
		{Days: []string{"mon"}, StartTime: "08:00", EndTime: "09:00"},
		{Days: []string{"mon"}, StartTime: "22:00", EndTime: "02:00"},
	}
	if !inAvailabilityWindow(now, windows[:1]) {
		t.Fatalf("expected in-window match")
	}
	if !inAvailabilityWindow(time.Date(2026, 3, 2, 23, 30, 0, 0, time.UTC), windows[1:]) {
		t.Fatalf("expected overnight window match")
	}
	if got := nextAvailabilityStart(now, []models.SoulContactAvailabilityWindow{{Days: []string{"mon"}, StartTime: "09:15", EndTime: "17:00"}}); got.UTC().Format(time.RFC3339) != "2026-03-02T09:15:00Z" {
		t.Fatalf("unexpected next start: %s", got.UTC().Format(time.RFC3339))
	}
}

func testTimeAndPhoneHelpers(t *testing.T) {
	t.Helper()

	fallbackNow := time.Date(2026, 3, 2, 8, 30, 0, 0, time.UTC)
	if weekdayAbbrev(time.Tuesday) != "tue" || weekdayAbbrev(time.Sunday) != "sun" {
		t.Fatalf("unexpected weekday abbreviations")
	}
	if !dayInWindow(" mon ", []string{"sun", "mon"}) {
		t.Fatalf("expected matching day")
	}
	if mins, ok := parseHHMMMinutes("09:45"); !ok || mins != 585 {
		t.Fatalf("unexpected hhmm parse: %d %v", mins, ok)
	}
	if _, ok := parseHHMMMinutes("25:00"); ok {
		t.Fatalf("expected invalid hhmm")
	}
	if n, err := parseSmallInt("17"); err != nil || n != 17 {
		t.Fatalf("unexpected small int parse: %d %v", n, err)
	}
	if _, err := parseSmallInt("1x"); err == nil {
		t.Fatalf("expected small int error")
	}
	if got := parseRFC3339Time("bad", fallbackNow); !got.Equal(fallbackNow.UTC()) {
		t.Fatalf("expected fallback time")
	}
	if got := normalizePhone(" +1 (555) 123-4567 "); got != "+15551234567" {
		t.Fatalf("unexpected normalized phone: %q", got)
	}
}

func testStageAndQueueHelpers(t *testing.T) {
	t.Helper()

	if instanceStageForControlPlane("prod") != "live" || instanceStageForControlPlane("stage") != "staging" || instanceStageForControlPlane("lab") != "dev" {
		t.Fatalf("unexpected stage mapping")
	}
	if got := instanceStageDomain("prod", "Example.com."); got != "example.com" {
		t.Fatalf("unexpected stage domain: %q", got)
	}
	if got := instanceNotificationsDeliverURL("lab", "demo.example.com"); got != "https://api.dev.demo.example.com/api/v1/notifications/deliver" {
		t.Fatalf("unexpected deliver url: %q", got)
	}
	if got := sqsQueueNameFromURL("https://sqs.us-east-1.amazonaws.com/123/queue-name"); got != "queue-name" {
		t.Fatalf("unexpected queue name: %q", got)
	}
	if got := sqsQueueNameFromURL("://bad"); got != "" {
		t.Fatalf("expected empty queue name for bad url")
	}
}

func TestCommWorkerStoreAndNotificationHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	fs := newCommWorkerStoreHelperStore(now)
	s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
	s.now = func() time.Time { return now }

	testResolveRecipientAndChannelMatching(t, s)
	testResolveAgentInstanceAndInboundCounts(t, s, now)
	testRecordAndQueueInbound(t, s, fs, now)
	testMaybeAnnotateSenderSoulHelper(t, s)
}

func newCommWorkerStoreHelperStore(now time.Time) *fakeStore {
	return &fakeStore{
		emailIndex: map[string]string{"alice@example.com": "0xabc"},
		phoneIndex: map[string]string{"+15551234567": "0xdef"},
		domains:    map[string]*models.Domain{"example.com": {Domain: "example.com", InstanceSlug: "inst-1"}},
		instances:  map[string]*models.Instance{"inst-1": {Slug: "inst-1", HostedBaseDomain: "demo.example.com"}},
		activities: map[string][]*models.SoulAgentCommActivity{
			"0xabc": {
				nil,
				{AgentID: "0xabc", Direction: models.SoulCommDirectionInbound, ChannelType: "email", Action: "receive", Timestamp: now.Add(-10 * time.Minute)},
				{AgentID: "0xabc", Direction: models.SoulCommDirectionOutbound, ChannelType: "email", Action: "receive", Timestamp: now.Add(-10 * time.Minute)},
				{AgentID: "0xabc", Direction: models.SoulCommDirectionInbound, ChannelType: "sms", Action: "receive", Timestamp: now.Add(-10 * time.Minute)},
				{AgentID: "0xabc", Direction: models.SoulCommDirectionInbound, ChannelType: "email", Action: "drop", Timestamp: now.Add(-10 * time.Minute)},
			},
		},
		channels: map[string]*models.SoulAgentChannel{
			"0xabc#email": {AgentID: "0xabc", ChannelType: "email", Identifier: commTestAgentEmail, SecretRef: "/pw"},
		},
	}
}

func testResolveRecipientAndChannelMatching(t *testing.T, s *Server) {
	t.Helper()

	agentID, ok, err := s.resolveRecipient(context.Background(), "email", &InboundParty{Address: "alice@example.com"})
	if err != nil || !ok || agentID != "0xabc" {
		t.Fatalf("unexpected email recipient lookup: %q %v %v", agentID, ok, err)
	}
	agentID, ok, err = s.resolveRecipient(context.Background(), "sms", &InboundParty{Number: "+1 (555) 123-4567"})
	if err != nil || !ok || agentID != "0xdef" {
		t.Fatalf("unexpected phone recipient lookup: %q %v %v", agentID, ok, err)
	}
	if _, missingOK, missingErr := s.resolveRecipient(context.Background(), "email", nil); missingErr != nil || missingOK {
		t.Fatalf("expected nil recipient to miss")
	}

	if !s.channelMatchesNotification(&models.SoulAgentChannel{Identifier: commTestAgentEmail}, "email", &InboundParty{Address: "AGENT@example.com"}) {
		t.Fatalf("expected email match")
	}
	if !s.channelMatchesNotification(&models.SoulAgentChannel{Identifier: "+15551234567"}, "sms", &InboundParty{Number: "+1 (555) 123-4567"}) {
		t.Fatalf("expected phone match")
	}
	if s.channelMatchesNotification(nil, "sms", &InboundParty{Number: "1"}) {
		t.Fatalf("expected nil channel to miss")
	}
}

func testResolveAgentInstanceAndInboundCounts(t *testing.T, s *Server, now time.Time) {
	t.Helper()

	inst, ok, err := s.resolveAgentInstance(context.Background(), &models.SoulAgentIdentity{Domain: "example.com"})
	if err != nil || !ok || inst == nil || inst.Slug != "inst-1" {
		t.Fatalf("unexpected instance resolution: %#v %v %v", inst, ok, err)
	}
	aliasServer := &Server{
		cfg: config.Config{Stage: "lab"},
		store: &fakeStore{
			domains: map[string]*models.Domain{
				"simulacrum.greater.website": {
					Domain:             "simulacrum.greater.website",
					InstanceSlug:       "simulacrum",
					Status:             models.DomainStatusVerified,
					Type:               models.DomainTypePrimary,
					VerificationMethod: "managed",
				},
			},
			instances: map[string]*models.Instance{
				"simulacrum": {Slug: "simulacrum", HostedBaseDomain: "simulacrum.greater.website"},
			},
		},
	}
	aliasInst, aliasOK, aliasErr := aliasServer.resolveAgentInstance(context.Background(), &models.SoulAgentIdentity{Domain: "dev.simulacrum.greater.website"})
	if aliasErr != nil || !aliasOK || aliasInst == nil || aliasInst.Slug != "simulacrum" {
		t.Fatalf("unexpected managed alias resolution: %#v %v %v", aliasInst, aliasOK, aliasErr)
	}
	if _, instanceOK, instanceErr := s.resolveAgentInstance(context.Background(), &models.SoulAgentIdentity{}); instanceErr != nil || instanceOK {
		t.Fatalf("expected empty domain to miss")
	}

	count, err := s.countInboundReceivesSince(context.Background(), "0xabc", "email", now.Add(-time.Hour), 10)
	if err != nil || count != 1 {
		t.Fatalf("unexpected count: %d %v", count, err)
	}
	if _, err := s.countInboundReceivesSince(context.Background(), "", "email", now, 10); err == nil {
		t.Fatalf("expected missing agent/channel error")
	}
}

func testRecordAndQueueInbound(t *testing.T, s *Server, fs *fakeStore, now time.Time) {
	t.Helper()

	notif := InboundNotification{
		Channel:    "email",
		From:       InboundParty{Address: "alice@example.com"},
		ReceivedAt: "2026-03-05T12:00:00Z",
		MessageID:  "msg-1",
		InReplyTo:  ptrString("<parent>"),
	}
	if err := s.recordInboundActivity(context.Background(), "0xAbC", "Email", notif, "receive", true); err != nil {
		t.Fatalf("recordInboundActivity: %v", err)
	}
	if len(fs.activities["0xabc"]) == 0 {
		t.Fatalf("expected recorded activity")
	}

	notif.From.SoulAgentID = ptrString(commTestSenderSoulID)
	if err := s.queueInbound(context.Background(), "0xAbC", "Email", notif, now.Add(time.Hour)); err != nil {
		t.Fatalf("queueInbound: %v", err)
	}
	if len(fs.queued) != 1 || fs.queued[0].FromSoulAgentID != commTestSenderSoulID {
		t.Fatalf("unexpected queued item: %#v", fs.queued)
	}
}

func testMaybeAnnotateSenderSoulHelper(t *testing.T, s *Server) {
	t.Helper()

	annotate := InboundNotification{Channel: "email", From: InboundParty{Address: "alice@example.com"}}
	s.maybeAnnotateSenderSoul(context.Background(), &annotate)
	if annotate.From.SoulAgentID == nil || *annotate.From.SoulAgentID != "0xabc" {
		t.Fatalf("expected annotated sender soul: %#v", annotate.From.SoulAgentID)
	}
}

func TestCommWorkerSecretAndDeliveryHelpers(t *testing.T) {
	t.Parallel()

	testUnwrapSecretsManagerSecretString(t)
	testGetSecretsManagerSecretPlaintext(t)
	testDefaultFetchInstanceKeyPlaintext(t)
	testDeliveryAndBounceHelpers(t)
}

func testUnwrapSecretsManagerSecretString(t *testing.T) {
	t.Helper()

	if _, err := unwrapSecretsManagerSecretString(""); err == nil {
		t.Fatalf("expected empty secret error")
	}
	if _, err := unwrapSecretsManagerSecretString(`{"not_secret":"x"}`); err == nil {
		t.Fatalf("expected missing secret key error")
	}
	if got, err := unwrapSecretsManagerSecretString(`{"secret":" abc "}`); err != nil || got != "abc" {
		t.Fatalf("unexpected unwrapped secret: %q %v", got, err)
	}
	if got, err := unwrapSecretsManagerSecretString(" plain "); err != nil || got != "plain" {
		t.Fatalf("unexpected plain secret: %q %v", got, err)
	}
}

func testGetSecretsManagerSecretPlaintext(t *testing.T) {
	t.Helper()

	if _, err := getSecretsManagerSecretPlaintext(context.Background(), nil, ""); err == nil {
		t.Fatalf("expected missing secret arn error")
	}
	if _, err := getSecretsManagerSecretPlaintext(context.Background(), nil, "arn"); err == nil {
		t.Fatalf("expected missing client error")
	}

	sm := stubSecretsManager{
		get: func(_ context.Context, in *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			if got := aws.ToString(in.SecretId); got != "arn:secret" {
				t.Fatalf("unexpected secret id: %q", got)
			}
			return &secretsmanager.GetSecretValueOutput{SecretBinary: []byte(`{"secret":" child "}`)}, nil
		},
	}
	if got, err := getSecretsManagerSecretPlaintext(context.Background(), sm, "arn:secret"); err != nil || got != "child" {
		t.Fatalf("unexpected secret plaintext: %q %v", got, err)
	}
}

func testDefaultFetchInstanceKeyPlaintext(t *testing.T) {
	t.Helper()

	sm := stubSecretsManager{
		get: func(_ context.Context, in *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			if got := aws.ToString(in.SecretId); got != "arn:secret" {
				t.Fatalf("unexpected secret id: %q", got)
			}
			return &secretsmanager.GetSecretValueOutput{SecretBinary: []byte(`{"secret":" child "}`)}, nil
		},
	}

	srv := &Server{cfg: config.Config{Stage: "lab"}, secrets: sm}
	if _, err := (*Server)(nil).defaultFetchInstanceKeyPlaintext(context.Background(), &models.Instance{}); err == nil {
		t.Fatalf("expected nil server error")
	}
	if _, err := srv.defaultFetchInstanceKeyPlaintext(context.Background(), nil); err == nil {
		t.Fatalf("expected nil instance error")
	}
	if _, err := srv.defaultFetchInstanceKeyPlaintext(context.Background(), &models.Instance{}); err == nil {
		t.Fatalf("expected missing secret arn error")
	}
	if _, err := (&Server{}).defaultFetchInstanceKeyPlaintext(context.Background(), &models.Instance{LesserHostInstanceKeySecretARN: "arn"}); err == nil {
		t.Fatalf("expected missing secrets manager error")
	}
	if got, err := srv.defaultFetchInstanceKeyPlaintext(context.Background(), &models.Instance{LesserHostInstanceKeySecretARN: "arn:secret"}); err != nil || got != "child" {
		t.Fatalf("unexpected same-account fetch: %q %v", got, err)
	}

	var logs []string
	srv = NewServer(config.Config{Stage: "lab"}, &fakeStore{}, nil, sm)
	srv.logf = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	if got, err := srv.defaultFetchInstanceKeyPlaintext(context.Background(), &models.Instance{
		Slug:                           "demo",
		HostedAccountID:                "123456789012",
		LesserHostInstanceKeySecretARN: "arn:secret",
	}); err != nil || got != "child" {
		t.Fatalf("unexpected same-account fallback fetch: %q %v", got, err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "falling back to same-account secret access") || !strings.Contains(logs[0], "role_name_present=false") || !strings.Contains(logs[0], "sts_ready=false") {
		t.Fatalf("unexpected fallback log: %#v", logs)
	}
}

func testDeliveryAndBounceHelpers(t *testing.T) {
	t.Helper()

	testDefaultDeliverNotificationHelper(t)
	testBounceFormattingHelpers(t)
	testSMTPValidationHelpers(t)
}

func testDefaultDeliverNotificationHelper(t *testing.T) {
	t.Helper()

	reqCount := 0
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		if got := r.Header.Get("authorization"); got != "Bearer api-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer httpSrv.Close()

	notif := InboundNotification{Type: "communication:inbound", Channel: "email", Body: "hello"}
	if err := defaultDeliverNotification(context.Background(), httpSrv.URL, "api-key", notif); err != nil {
		t.Fatalf("unexpected deliver error: %v", err)
	}
	if reqCount != 1 {
		t.Fatalf("expected one request, got %d", reqCount)
	}
	if err := defaultDeliverNotification(context.Background(), "", "api-key", notif); err == nil {
		t.Fatalf("expected missing args error")
	}

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(" upstream failed "))
	}))
	defer failSrv.Close()
	if err := defaultDeliverNotification(context.Background(), failSrv.URL, "api-key", notif); err == nil || !strings.Contains(err.Error(), `status=502`) {
		t.Fatalf("expected status error, got %v", err)
	}
}

func testBounceFormattingHelpers(t *testing.T) {
	t.Helper()

	if got := soulAgentEmailPasswordSSMParam("", "0xAbC"); got != "/lesser-host/soul/lab/agents/0xabc/channels/email/migadu_password" {
		t.Fatalf("unexpected password param: %q", got)
	}
	body := buildBounceBody("rate_limited", commTestAgentEmail, 5, 10, 5, 10, "msg-1", "req-1")
	if !strings.Contains(body, "maxInboundPerHour") || !strings.Contains(body, "Request ID: req-1") {
		t.Fatalf("unexpected bounce body: %q", body)
	}
	if got := buildBounceMessageID(commTestAgentEmail, "<parent>"); !strings.HasPrefix(got, "<comm-bounce-") {
		t.Fatalf("unexpected bounce message id: %q", got)
	}
	rfc := string(buildPlaintextEmailRFC5322("from@example.com", "to@example.com", "Subject\r\nBad", "Line1\nLine2", "<id>", "<parent>"))
	if !strings.Contains(rfc, "Subject: Subject  Bad\r\n") || !strings.Contains(rfc, "In-Reply-To: <parent>\r\n") || !strings.Contains(rfc, "Line1\r\nLine2\r\n") {
		t.Fatalf("unexpected RFC5322 body: %q", rfc)
	}
}

func testSMTPValidationHelpers(t *testing.T) {
	t.Helper()

	if token, err := newToken(0); err != nil || len(token) != 16 {
		t.Fatalf("unexpected token: %q %v", token, err)
	}
	if err := defaultMigaduSendSMTP(context.Background(), "", "", "", nil, nil); err == nil {
		t.Fatalf("expected smtp input validation error")
	}
}

func TestCommWorkerHandleQueueAndBounce(t *testing.T) {
	t.Parallel()

	s := NewServer(config.Config{}, nil, nil, nil)
	if err := s.handleCommQueueMessage(nil, events.SQSMessage{}); err == nil {
		t.Fatalf("expected nil store error")
	}

	ctx := &apptheory.EventContext{RequestID: "req-1"}
	s = NewServer(config.Config{}, &fakeStore{}, nil, nil)
	if err := s.handleCommQueueMessage(ctx, events.SQSMessage{Body: "{"}); err != nil {
		t.Fatalf("expected invalid json to be dropped, got %v", err)
	}

	body, err := json.Marshal(QueueMessage{Kind: "other"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := s.handleCommQueueMessage(ctx, events.SQSMessage{Body: string(body)}); err != nil {
		t.Fatalf("expected invalid kind to be dropped, got %v", err)
	}

	fs := &fakeStore{
		channels: map[string]*models.SoulAgentChannel{
			"0xabc#email": {AgentID: "0xabc", ChannelType: "email", Identifier: commTestAgentEmail, SecretRef: "/pw"},
		},
	}
	s = NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
	s.ssmGetParameter = func(context.Context, string) (string, error) { return commTestSMTPPassword, nil }
	bounced := false
	s.migaduSendSMTP = func(_ context.Context, username string, password string, from string, recipients []string, data []byte) error {
		bounced = true
		if username != commTestAgentEmail || password != commTestSMTPPassword || from != commTestAgentEmail || len(recipients) != 1 || recipients[0] != "sender@example.com" {
			t.Fatalf("unexpected smtp args")
		}
		if !strings.Contains(string(data), "Message rejected") {
			t.Fatalf("expected bounce email body")
		}
		return nil
	}
	if err := s.maybeBounceEmail(context.Background(), "0xabc", "rate_limited", "email", InboundNotification{
		From:      InboundParty{Address: "sender@example.com"},
		MessageID: "msg-1",
	}, 5, 10, 5, 10); err != nil {
		t.Fatalf("maybeBounceEmail: %v", err)
	}
	if !bounced {
		t.Fatalf("expected bounce send")
	}
	if err := s.maybeBounceEmail(context.Background(), "0xabc", "rate_limited", "sms", InboundNotification{}, 0, 0, 0, 0); err != nil {
		t.Fatalf("expected sms bounce skip to be nil, got %v", err)
	}
}

func TestHandleCommQueueMessage_LogsDropReasons(t *testing.T) {
	var logs []string
	logf := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	s := NewServer(config.Config{}, &fakeStore{}, nil, nil)
	s.logf = logf
	ctx := &apptheory.EventContext{RequestID: "req-drop-log"}

	if err := s.handleCommQueueMessage(ctx, events.SQSMessage{MessageId: "sqs-invalid-json", Body: "{"}); err != nil {
		t.Fatalf("expected invalid json to be dropped, got %v", err)
	}

	otherBody, err := json.Marshal(QueueMessage{Kind: "other"})
	if err != nil {
		t.Fatalf("marshal unsupported kind: %v", err)
	}
	if err := s.handleCommQueueMessage(ctx, events.SQSMessage{MessageId: "sqs-unsupported", Body: string(otherBody)}); err != nil {
		t.Fatalf("expected unsupported kind to be dropped, got %v", err)
	}

	invalidBody, err := json.Marshal(QueueMessage{
		Kind: QueueMessageKindInbound,
		Notification: InboundNotification{
			Type:       "communication:inbound",
			Channel:    "email",
			From:       InboundParty{Address: "sender@example.com"},
			Body:       "missing required fields",
			ReceivedAt: "2026-03-05T12:00:00Z",
			MessageID:  "msg-invalid",
		},
	})
	if err != nil {
		t.Fatalf("marshal invalid payload: %v", err)
	}
	if err := s.handleCommQueueMessage(ctx, events.SQSMessage{MessageId: "sqs-invalid-payload", Body: string(invalidBody)}); err != nil {
		t.Fatalf("expected invalid payload to be dropped, got %v", err)
	}

	if len(logs) != 3 {
		t.Fatalf("expected 3 log lines, got %d: %#v", len(logs), logs)
	}
	if !strings.Contains(logs[0], "reason=invalid_json") || !strings.Contains(logs[0], "request=req-drop-log") || !strings.Contains(logs[0], "sqs_message=sqs-invalid-json") {
		t.Fatalf("unexpected invalid json log: %q", logs[0])
	}
	if !strings.Contains(logs[1], "reason=unsupported_kind") || !strings.Contains(logs[1], "kind=other") || !strings.Contains(logs[1], "sqs_message=sqs-unsupported") {
		t.Fatalf("unexpected unsupported kind log: %q", logs[1])
	}
	if !strings.Contains(logs[2], "reason=invalid_payload") || !strings.Contains(logs[2], "channel=email") || !strings.Contains(logs[2], "message=msg-invalid") {
		t.Fatalf("unexpected invalid payload log: %q", logs[2])
	}
}

func ptrString(v string) *string { return &v }
