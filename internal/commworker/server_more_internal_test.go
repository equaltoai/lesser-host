package commworker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type hookedStore struct {
	*fakeStore

	lookupAgentByEmailFn       func(context.Context, string) (string, bool, error)
	lookupAgentByPhoneFn       func(context.Context, string) (string, bool, error)
	getSoulAgentIdentityFn     func(context.Context, string) (*models.SoulAgentIdentity, bool, error)
	getSoulAgentChannelFn      func(context.Context, string, string) (*models.SoulAgentChannel, bool, error)
	getSoulAgentContactPrefsFn func(context.Context, string) (*models.SoulAgentContactPreferences, bool, error)
	listRecentCommActivitiesFn func(context.Context, string, int) ([]*models.SoulAgentCommActivity, error)
	getDomainFn                func(context.Context, string) (*models.Domain, bool, error)
	getInstanceFn              func(context.Context, string) (*models.Instance, bool, error)
}

func (h *hookedStore) LookupAgentByEmail(ctx context.Context, email string) (string, bool, error) {
	if h.lookupAgentByEmailFn != nil {
		return h.lookupAgentByEmailFn(ctx, email)
	}
	if h.fakeStore != nil {
		return h.fakeStore.LookupAgentByEmail(ctx, email)
	}
	return "", false, nil
}

func (h *hookedStore) LookupAgentByPhone(ctx context.Context, phone string) (string, bool, error) {
	if h.lookupAgentByPhoneFn != nil {
		return h.lookupAgentByPhoneFn(ctx, phone)
	}
	if h.fakeStore != nil {
		return h.fakeStore.LookupAgentByPhone(ctx, phone)
	}
	return "", false, nil
}

func (h *hookedStore) GetSoulAgentIdentity(ctx context.Context, agentID string) (*models.SoulAgentIdentity, bool, error) {
	if h.getSoulAgentIdentityFn != nil {
		return h.getSoulAgentIdentityFn(ctx, agentID)
	}
	if h.fakeStore != nil {
		return h.fakeStore.GetSoulAgentIdentity(ctx, agentID)
	}
	return nil, false, nil
}

func (h *hookedStore) GetSoulAgentChannel(ctx context.Context, agentID string, channelType string) (*models.SoulAgentChannel, bool, error) {
	if h.getSoulAgentChannelFn != nil {
		return h.getSoulAgentChannelFn(ctx, agentID, channelType)
	}
	if h.fakeStore != nil {
		return h.fakeStore.GetSoulAgentChannel(ctx, agentID, channelType)
	}
	return nil, false, nil
}

func (h *hookedStore) GetSoulAgentContactPreferences(ctx context.Context, agentID string) (*models.SoulAgentContactPreferences, bool, error) {
	if h.getSoulAgentContactPrefsFn != nil {
		return h.getSoulAgentContactPrefsFn(ctx, agentID)
	}
	if h.fakeStore != nil {
		return h.fakeStore.GetSoulAgentContactPreferences(ctx, agentID)
	}
	return nil, false, nil
}

func (h *hookedStore) ListRecentCommActivities(ctx context.Context, agentID string, limit int) ([]*models.SoulAgentCommActivity, error) {
	if h.listRecentCommActivitiesFn != nil {
		return h.listRecentCommActivitiesFn(ctx, agentID, limit)
	}
	if h.fakeStore != nil {
		return h.fakeStore.ListRecentCommActivities(ctx, agentID, limit)
	}
	return nil, nil
}

func (h *hookedStore) GetDomain(ctx context.Context, domain string) (*models.Domain, bool, error) {
	if h.getDomainFn != nil {
		return h.getDomainFn(ctx, domain)
	}
	if h.fakeStore != nil {
		return h.fakeStore.GetDomain(ctx, domain)
	}
	return nil, false, nil
}

func (h *hookedStore) GetInstance(ctx context.Context, slug string) (*models.Instance, bool, error) {
	if h.getInstanceFn != nil {
		return h.getInstanceFn(ctx, slug)
	}
	if h.fakeStore != nil {
		return h.fakeStore.GetInstance(ctx, slug)
	}
	return nil, false, nil
}

type stubSTS struct {
	assume func(context.Context, *sts.AssumeRoleInput) (*sts.AssumeRoleOutput, error)
}

func (s stubSTS) AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	if s.assume == nil {
		return nil, errors.New("missing assume stub")
	}
	return s.assume(ctx, params)
}

type smtpTranscript struct {
	mailFrom string
	rcpts    []string
	data     string
}

func newInboundEmailMessage(now time.Time, to string) QueueMessage {
	return QueueMessage{
		Kind: QueueMessageKindInbound,
		Notification: InboundNotification{
			Type:       "communication:inbound",
			Channel:    "email",
			From:       InboundParty{Address: "sender@example.com"},
			To:         &InboundParty{Address: to},
			Subject:    "Hello",
			Body:       "Test",
			ReceivedAt: now.Format(time.RFC3339Nano),
			MessageID:  "msg-email-1",
		},
	}
}

func newInboundSMSMessage(now time.Time, to string, from string) QueueMessage {
	return QueueMessage{
		Kind: QueueMessageKindInbound,
		Notification: InboundNotification{
			Type:       "communication:inbound",
			Channel:    "sms",
			From:       InboundParty{Number: from},
			To:         &InboundParty{Number: to},
			Body:       "SMS body",
			ReceivedAt: now.Format(time.RFC3339Nano),
			MessageID:  "msg-sms-1",
		},
	}
}

func newActiveInboundStore(now time.Time, agentID string, channel string, identifier string) *fakeStore {
	channelType := channelRecordType(channel)
	fs := &fakeStore{
		identities: map[string]*models.SoulAgentIdentity{
			agentID: {
				AgentID:         agentID,
				Domain:          "demo.greater.website",
				LifecycleStatus: models.SoulAgentStatusActive,
				Status:          models.SoulAgentStatusActive,
			},
		},
		channels: map[string]*models.SoulAgentChannel{
			agentID + "#" + channelType: {
				AgentID:       agentID,
				ChannelType:   channelType,
				Identifier:    identifier,
				Status:        models.SoulChannelStatusActive,
				Verified:      true,
				ProvisionedAt: now.Add(-time.Hour),
			},
		},
		domains: map[string]*models.Domain{
			"demo.greater.website": {Domain: "demo.greater.website", InstanceSlug: "demo"},
		},
		instances: map[string]*models.Instance{
			"demo": {Slug: "demo", HostedBaseDomain: "demo.greater.website", LesserHostInstanceKeySecretARN: "arn:secret"},
		},
	}
	switch channel {
	case inboundChannelEmail:
		fs.emailIndex = map[string]string{strings.ToLower(strings.TrimSpace(identifier)): agentID}
	case inboundChannelSMS, inboundChannelVoice:
		fs.phoneIndex = map[string]string{normalizePhone(identifier): agentID}
	}
	return fs
}

func startSMTPTestServer(t *testing.T, rcptCode int, rcptMsg string) (string, *smtpTranscript, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp: %v", err)
	}

	transcript := &smtpTranscript{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		writeSMTPLine(conn, "220 localhost ESMTP")

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			done := handleSMTPLine(reader, conn, transcript, line, rcptCode, rcptMsg)
			if done {
				return
			}
		}
	}()

	cleanup := func() {
		_ = ln.Close()
		<-done
	}
	return ln.Addr().String(), transcript, cleanup
}

func writeSMTPLine(conn net.Conn, line string) {
	_, _ = fmt.Fprintf(conn, "%s\r\n", line)
}

func readSMTPData(reader *bufio.Reader) (string, bool) {
	var data strings.Builder
	for {
		part, err := reader.ReadString('\n')
		if err != nil {
			return "", false
		}
		if part == ".\r\n" {
			return data.String(), true
		}
		data.WriteString(part)
	}
}

func handleSMTPLine(reader *bufio.Reader, conn net.Conn, transcript *smtpTranscript, line string, rcptCode int, rcptMsg string) bool {
	line = strings.TrimRight(line, "\r\n")
	upper := strings.ToUpper(line)

	switch {
	case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
		_, _ = fmt.Fprint(conn, "250-localhost\r\n250 OK\r\n")
	case strings.HasPrefix(upper, "MAIL FROM:"):
		transcript.mailFrom = line
		writeSMTPLine(conn, "250 2.1.0 Ok")
	case strings.HasPrefix(upper, "RCPT TO:"):
		transcript.rcpts = append(transcript.rcpts, line)
		if rcptCode != 0 && rcptCode != 250 {
			writeSMTPLine(conn, fmt.Sprintf("%d %s", rcptCode, rcptMsg))
			return true
		}
		writeSMTPLine(conn, "250 2.1.5 Ok")
	case upper == "DATA":
		writeSMTPLine(conn, "354 End data with <CR><LF>.<CR><LF>")
		data, ok := readSMTPData(reader)
		if !ok {
			return true
		}
		transcript.data = data
		writeSMTPLine(conn, "250 2.0.0 Ok")
	case upper == "QUIT":
		writeSMTPLine(conn, "221 2.0.0 Bye")
		return true
	default:
		writeSMTPLine(conn, "250 Ok")
	}

	return false
}

func TestNewServerDefaultsAndRegister(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	s := NewServer(config.Config{}, store, nil, nil)
	if s == nil {
		t.Fatal("expected server")
	}
	if s.store != store || s.now == nil || s.fetchInstanceKeyPlaintext == nil || s.deliverNotification == nil || s.ssmGetParameter == nil || s.migaduSendSMTP == nil {
		t.Fatalf("expected default dependencies to be populated: %#v", s)
	}

	var nilServer *Server
	nilServer.Register(apptheory.New())
	s.Register(nil)

	app := apptheory.New()
	s.Register(app)
	if got := reflect.ValueOf(app).Elem().FieldByName("sqsRoutes").Len(); got != 0 {
		t.Fatalf("expected no sqs routes without queue url, got %d", got)
	}

	appWithQueue := apptheory.New()
	NewServer(
		config.Config{CommQueueURL: "https://sqs.us-east-1.amazonaws.com/123456789012/comm-queue"},
		store,
		nil,
		nil,
	).Register(appWithQueue)
	if got := reflect.ValueOf(appWithQueue).Elem().FieldByName("sqsRoutes").Len(); got != 1 {
		t.Fatalf("expected 1 sqs route, got %d", got)
	}
}

func TestHandleCommQueueMessage_Branches(t *testing.T) {
	t.Parallel()

	t.Run("nil event context returns error", func(t *testing.T) {
		s := NewServer(config.Config{}, &fakeStore{}, nil, nil)
		if err := s.handleCommQueueMessage(nil, events.SQSMessage{}); err == nil || err.Error() != "event context is nil" {
			t.Fatalf("expected nil context error, got %v", err)
		}
	})

	t.Run("invalid inbound notification is dropped", func(t *testing.T) {
		s := NewServer(config.Config{}, &fakeStore{}, nil, nil)
		ctx := &apptheory.EventContext{RequestID: "req-invalid"}
		body, err := json.Marshal(QueueMessage{
			Kind: QueueMessageKindInbound,
			Notification: InboundNotification{
				Type:       "communication:inbound",
				Channel:    "email",
				From:       InboundParty{Address: "sender@example.com"},
				Body:       "missing to and subject",
				ReceivedAt: "2026-03-05T12:00:00Z",
				MessageID:  "msg-invalid",
			},
		})
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		if err := s.handleCommQueueMessage(ctx, events.SQSMessage{Body: string(body)}); err != nil {
			t.Fatalf("expected invalid notification to be dropped, got %v", err)
		}
	})

	t.Run("valid inbound delegates into processing", func(t *testing.T) {
		s := NewServer(config.Config{}, &fakeStore{}, nil, nil)
		ctx := &apptheory.EventContext{RequestID: "req-valid"}
		body, err := json.Marshal(newInboundEmailMessage(time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC), "agent@example.com"))
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		if err := s.handleCommQueueMessage(ctx, events.SQSMessage{Body: string(body)}); err != nil {
			t.Fatalf("expected valid message to be handled, got %v", err)
		}
	})
}

func TestResolveRecipientAndResolveAgentInstance_Branches(t *testing.T) {
	t.Parallel()

	if _, _, err := (*Server)(nil).resolveRecipient(context.Background(), "email", &InboundParty{Address: "a@example.com"}); err == nil {
		t.Fatal("expected nil server recipient error")
	}
	if _, _, err := (&Server{store: &fakeStore{}}).resolveAgentInstance(context.Background(), nil); err != nil {
		t.Fatalf("expected nil identity to miss without error, got %v", err)
	}
	if _, _, err := (*Server)(nil).resolveAgentInstance(context.Background(), &models.SoulAgentIdentity{}); err == nil {
		t.Fatal("expected nil server instance error")
	}

	store := &hookedStore{
		fakeStore: &fakeStore{},
		lookupAgentByEmailFn: func(context.Context, string) (string, bool, error) {
			return "", false, errors.New("lookup failed")
		},
	}
	s := &Server{store: store}
	if _, _, err := s.resolveRecipient(context.Background(), "email", &InboundParty{}); err != nil {
		t.Fatalf("expected blank address miss, got %v", err)
	}
	if _, ok, err := s.resolveRecipient(context.Background(), "fax", &InboundParty{Address: "a@example.com"}); err != nil || ok {
		t.Fatalf("expected unsupported channel miss, got ok=%v err=%v", ok, err)
	}
	if _, _, err := s.resolveRecipient(context.Background(), "email", &InboundParty{Address: "a@example.com"}); err == nil || err.Error() != "lookup failed" {
		t.Fatalf("expected lookup error, got %v", err)
	}

	store = &hookedStore{
		fakeStore: &fakeStore{},
		getDomainFn: func(context.Context, string) (*models.Domain, bool, error) {
			return nil, false, errors.New("domain failed")
		},
	}
	s = &Server{store: store}
	if _, _, err := s.resolveAgentInstance(context.Background(), &models.SoulAgentIdentity{Domain: "example.com"}); err == nil || err.Error() != "domain failed" {
		t.Fatalf("expected domain error, got %v", err)
	}

	store = &hookedStore{
		fakeStore: &fakeStore{
			domains: map[string]*models.Domain{"example.com": {Domain: "example.com", InstanceSlug: "demo"}},
		},
		getInstanceFn: func(context.Context, string) (*models.Instance, bool, error) {
			return nil, false, errors.New("instance failed")
		},
	}
	s = &Server{store: store}
	if _, _, err := s.resolveAgentInstance(context.Background(), &models.SoulAgentIdentity{Domain: "example.com"}); err == nil || err.Error() != "instance failed" {
		t.Fatalf("expected instance error, got %v", err)
	}
}

func TestMaybeAnnotateSenderSoul_Branches(t *testing.T) {
	t.Parallel()

	(*Server)(nil).maybeAnnotateSenderSoul(context.Background(), nil)

	s := &Server{store: &hookedStore{
		fakeStore: &fakeStore{},
		lookupAgentByEmailFn: func(context.Context, string) (string, bool, error) {
			return "", false, errors.New("lookup failed")
		},
	}}
	notif := &InboundNotification{Channel: "email", From: InboundParty{Address: "sender@example.com"}}
	s.maybeAnnotateSenderSoul(context.Background(), notif)
	if notif.From.SoulAgentID != nil {
		t.Fatalf("expected failed lookup to leave sender unannotated: %#v", notif.From.SoulAgentID)
	}

	existing := "0xalready"
	notif = &InboundNotification{Channel: "email", From: InboundParty{Address: "sender@example.com", SoulAgentID: &existing}}
	s.maybeAnnotateSenderSoul(context.Background(), notif)
	if notif.From.SoulAgentID == nil || *notif.From.SoulAgentID != existing {
		t.Fatalf("expected existing annotation to be preserved: %#v", notif.From.SoulAgentID)
	}

	s = &Server{store: &hookedStore{
		fakeStore: &fakeStore{},
		lookupAgentByPhoneFn: func(context.Context, string) (string, bool, error) {
			return "0xphone-sender", true, nil
		},
	}}
	notif = &InboundNotification{Channel: "sms", From: InboundParty{Number: " +1 (555) 123-4567 "}}
	s.maybeAnnotateSenderSoul(context.Background(), notif)
	if notif.From.SoulAgentID == nil || *notif.From.SoulAgentID != "0xphone-sender" {
		t.Fatalf("expected phone sender annotation, got %#v", notif.From.SoulAgentID)
	}

	notif = &InboundNotification{Channel: "voice", From: InboundParty{}}
	s.maybeAnnotateSenderSoul(context.Background(), notif)
	if notif.From.SoulAgentID != nil {
		t.Fatalf("expected empty voice sender to remain nil, got %#v", notif.From.SoulAgentID)
	}
}

func TestProcessInbound_BouncesInactiveAgent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	agentID := commStoreTestAgentID
	to := commTestAgentEmail
	fs := newActiveInboundStore(now, agentID, inboundChannelEmail, to)
	fs.identities[agentID].LifecycleStatus = ""
	fs.identities[agentID].Status = "suspended"
	fs.channels[agentID+"#email"].SecretRef = "/pw"

	bounced := false
	s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
	s.now = func() time.Time { return now }
	s.ssmGetParameter = func(context.Context, string) (string, error) { return commTestSMTPPassword, nil }
	s.migaduSendSMTP = func(_ context.Context, username string, password string, from string, recipients []string, data []byte) error {
		bounced = true
		if username != to || password != commTestSMTPPassword || from != to || len(recipients) != 1 || recipients[0] != "sender@example.com" {
			t.Fatalf("unexpected bounce args")
		}
		if !strings.Contains(string(data), "Reason: suspended") {
			t.Fatalf("expected suspension reason in bounce body: %q", string(data))
		}
		return nil
	}
	s.deliverNotification = func(context.Context, string, string, InboundNotification) error {
		t.Fatal("deliver should not be called for inactive agent")
		return nil
	}

	if err := s.processInbound(context.Background(), "req-bounce", newInboundEmailMessage(now, to)); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !bounced {
		t.Fatal("expected bounce email")
	}
	if len(fs.activities[agentID]) != 1 || fs.activities[agentID][0].Action != "bounce" || fs.activities[agentID][0].PreferenceRespected == nil || *fs.activities[agentID][0].PreferenceRespected {
		t.Fatalf("unexpected recorded bounce activity: %#v", fs.activities[agentID])
	}
}

func TestProcessInbound_IgnoresMismatchedAndInactiveChannels(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	agentID := commStoreTestAgentID
	to := commTestAgentEmail

	fsMismatch := newActiveInboundStore(now, agentID, inboundChannelEmail, "other@example.com")
	s := NewServer(config.Config{Stage: "lab"}, fsMismatch, nil, nil)
	s.deliverNotification = func(context.Context, string, string, InboundNotification) error {
		t.Fatal("deliver should not be called for mismatched channel")
		return nil
	}
	if err := s.processInbound(context.Background(), "req-mismatch", newInboundEmailMessage(now, to)); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	fsInactive := newActiveInboundStore(now, agentID, inboundChannelEmail, to)
	fsInactive.channels[agentID+"#email"].Verified = false
	s = NewServer(config.Config{Stage: "lab"}, fsInactive, nil, nil)
	s.deliverNotification = func(context.Context, string, string, InboundNotification) error {
		t.Fatal("deliver should not be called for inactive channel")
		return nil
	}
	if err := s.processInbound(context.Background(), "req-inactive-channel", newInboundEmailMessage(now, to)); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestProcessInbound_SMSAnnotatesSenderBeforeDelivery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	agentID := commStoreTestAgentID
	to := "+15551234567"
	from := "+15557654321"
	fs := newActiveInboundStore(now, agentID, inboundChannelSMS, to)
	fs.phoneIndex[normalizePhone(from)] = commTestSenderSoulID

	var delivered InboundNotification
	var deliverURL string
	s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
	s.now = func() time.Time { return now }
	s.fetchInstanceKeyPlaintext = func(context.Context, *models.Instance) (string, error) { return testInstanceAPIKey, nil }
	s.deliverNotification = func(_ context.Context, url string, apiKey string, notif InboundNotification) error {
		deliverURL = url
		if apiKey != testInstanceAPIKey {
			t.Fatalf("unexpected api key: %q", apiKey)
		}
		delivered = notif
		return nil
	}

	if err := s.processInbound(context.Background(), "req-sms", newInboundSMSMessage(now, " +1 (555) 123-4567 ", " +1 (555) 765-4321 ")); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if deliverURL != "https://api.dev.demo.greater.website/api/v1/notifications/deliver" {
		t.Fatalf("unexpected deliver url: %q", deliverURL)
	}
	if delivered.From.SoulAgentID == nil || *delivered.From.SoulAgentID != commTestSenderSoulID {
		t.Fatalf("expected sender annotation, got %#v", delivered.From.SoulAgentID)
	}
	if len(fs.activities[agentID]) != 1 || fs.activities[agentID][0].Action != "receive" {
		t.Fatalf("expected recorded receive activity, got %#v", fs.activities[agentID])
	}
}

func TestProcessInbound_ErrorBranches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	to := commTestAgentEmail

	tests := []struct {
		name string
		srv  func() *Server
		want string
	}{
		{
			name: "recipient lookup",
			srv: func() *Server {
				return NewServer(config.Config{}, &hookedStore{
					fakeStore: &fakeStore{},
					lookupAgentByEmailFn: func(context.Context, string) (string, bool, error) {
						return "", false, errors.New("lookup failed")
					},
				}, nil, nil)
			},
			want: "lookup failed",
		},
		{
			name: "preferences lookup",
			srv: func() *Server {
				fs := newActiveInboundStore(now, commStoreTestAgentID, inboundChannelEmail, to)
				return NewServer(config.Config{}, &hookedStore{
					fakeStore: fs,
					getSoulAgentContactPrefsFn: func(context.Context, string) (*models.SoulAgentContactPreferences, bool, error) {
						return nil, false, errors.New("prefs failed")
					},
				}, nil, nil)
			},
			want: "prefs failed",
		},
		{
			name: "fetch instance key",
			srv: func() *Server {
				fs := newActiveInboundStore(now, commStoreTestAgentID, inboundChannelEmail, to)
				s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
				s.fetchInstanceKeyPlaintext = func(context.Context, *models.Instance) (string, error) {
					return "", errors.New("key fetch failed")
				}
				return s
			},
			want: "key fetch failed",
		},
		{
			name: "empty delivery url",
			srv: func() *Server {
				fs := newActiveInboundStore(now, commStoreTestAgentID, inboundChannelEmail, to)
				fs.instances["demo"].HostedBaseDomain = ""
				s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
				s.fetchInstanceKeyPlaintext = func(context.Context, *models.Instance) (string, error) { return "lhk", nil }
				return s
			},
			want: "instance delivery url is empty",
		},
		{
			name: "deliver notification",
			srv: func() *Server {
				fs := newActiveInboundStore(now, commStoreTestAgentID, inboundChannelEmail, to)
				s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
				s.fetchInstanceKeyPlaintext = func(context.Context, *models.Instance) (string, error) { return "lhk", nil }
				s.deliverNotification = func(context.Context, string, string, InboundNotification) error {
					return errors.New("deliver failed")
				}
				return s
			},
			want: "deliver failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.srv()
			err := s.processInbound(context.Background(), "req-error", newInboundEmailMessage(now, to))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestDefaultFetchInstanceKeyPlaintext_AssumeRoleBranches(t *testing.T) {
	t.Parallel()

	secrets := stubSecretsManager{
		get: func(context.Context, *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return nil, errors.New("secret fetch should not be reached")
		},
	}

	t.Run("assume role error includes truncated session name", func(t *testing.T) {
		called := false
		s := &Server{
			cfg: config.Config{
				Stage:                   "lab",
				ManagedInstanceRoleName: "managed-instance-role",
			},
			secrets: secrets,
			sts: stubSTS{
				assume: func(_ context.Context, in *sts.AssumeRoleInput) (*sts.AssumeRoleOutput, error) {
					called = true
					if aws.ToString(in.RoleArn) != "arn:aws:iam::123456789012:role/managed-instance-role" {
						t.Fatalf("unexpected role arn: %q", aws.ToString(in.RoleArn))
					}
					if got := aws.ToString(in.RoleSessionName); len(got) > 64 || !strings.HasPrefix(got, "lesser-host-lab-comm-") {
						t.Fatalf("unexpected session name: %q", got)
					}
					if aws.ToInt32(in.DurationSeconds) != 900 {
						t.Fatalf("unexpected duration: %d", aws.ToInt32(in.DurationSeconds))
					}
					return nil, errors.New("assume failed")
				},
			},
		}
		inst := &models.Instance{
			Slug:                           strings.Repeat("slug", 20),
			HostedAccountID:                "123456789012",
			HostedRegion:                   "us-west-2",
			LesserHostInstanceKeySecretARN: "arn:secret",
		}
		_, err := s.defaultFetchInstanceKeyPlaintext(context.Background(), inst)
		if !called {
			t.Fatal("expected assume role to be called")
		}
		if err == nil || !strings.Contains(err.Error(), "assume instance role: assume failed") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty assumed credentials are rejected", func(t *testing.T) {
		s := &Server{
			cfg: config.Config{
				Stage:                   "lab",
				ManagedInstanceRoleName: "managed-instance-role",
			},
			secrets: secrets,
			sts: stubSTS{
				assume: func(context.Context, *sts.AssumeRoleInput) (*sts.AssumeRoleOutput, error) {
					return &sts.AssumeRoleOutput{}, nil
				},
			},
		}
		inst := &models.Instance{
			Slug:                           "demo",
			HostedAccountID:                "123456789012",
			LesserHostInstanceKeySecretARN: "arn:secret",
		}
		_, err := s.defaultFetchInstanceKeyPlaintext(context.Background(), inst)
		if err == nil || !strings.Contains(err.Error(), "assume role returned empty credentials") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestDefaultMigaduSendSMTPWithAddr_SMTPFlow(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		addr, transcript, cleanup := startSMTPTestServer(t, 250, "")
		defer cleanup()

		err := defaultMigaduSendSMTPWithAddr(
			context.Background(),
			"user",
			"pass",
			"from@example.com",
			[]string{"to@example.com"},
			[]byte("hello smtp"),
			addr,
		)
		if err != nil {
			t.Fatalf("unexpected smtp error: %v", err)
		}
		if transcript.mailFrom != "MAIL FROM:<from@example.com>" {
			t.Fatalf("unexpected MAIL FROM: %q", transcript.mailFrom)
		}
		if len(transcript.rcpts) != 1 || transcript.rcpts[0] != "RCPT TO:<to@example.com>" {
			t.Fatalf("unexpected RCPT TO sequence: %#v", transcript.rcpts)
		}
		if !strings.Contains(transcript.data, "hello smtp") {
			t.Fatalf("expected smtp data payload, got %q", transcript.data)
		}
	})

	t.Run("recipient rejection surfaces as rcpt error", func(t *testing.T) {
		addr, _, cleanup := startSMTPTestServer(t, 550, "No such user")
		defer cleanup()

		err := defaultMigaduSendSMTPWithAddr(
			context.Background(),
			"user",
			"pass",
			"from@example.com",
			[]string{"blocked@example.com"},
			[]byte("hello smtp"),
			addr,
		)
		if err == nil || !strings.Contains(err.Error(), `smtp rcpt "blocked@example.com"`) {
			t.Fatalf("unexpected smtp rcpt error: %v", err)
		}
	})
}
