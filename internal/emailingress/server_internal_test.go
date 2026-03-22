package emailingress

import (
	"context"
	"encoding/json"
	"io"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/config"
)

const inboundBridgeAddress = "medic@inbound.lessersoul.ai"

type fakeS3 struct {
	bodyByKey map[string]string
	errByKey  map[string]error
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := aws.ToString(in.Key)
	if err := f.errByKey[key]; err != nil {
		return nil, err
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(f.bodyByKey[key]))}, nil
}

type fakeSQS struct {
	bodies []string
	err    error
}

func (f *fakeSQS) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.bodies = append(f.bodies, aws.ToString(in.MessageBody))
	return &sqs.SendMessageOutput{}, nil
}

func TestNormalizeInboundRecipient(t *testing.T) {
	t.Parallel()

	if got, ok := normalizeInboundRecipient("Agent@Inbound.LesserSoul.ai", "inbound.lessersoul.ai"); !ok || got != "agent@lessersoul.ai" {
		t.Fatalf("unexpected recipient mapping: got=%q ok=%v", got, ok)
	}
	if _, ok := normalizeInboundRecipient("agent@example.com", "inbound.lessersoul.ai"); ok {
		t.Fatal("expected non-bridge recipient to be rejected")
	}
}

func TestNewServer(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	srv := NewServer()
	if srv == nil || srv.s3 == nil || srv.sqs == nil || srv.now == nil || srv.logf == nil {
		t.Fatalf("unexpected server: %#v", srv)
	}
}

func TestInboundEmailObjectKey(t *testing.T) {
	t.Parallel()

	if got := inboundEmailObjectKey(" ses/inbound ", " msg-1 "); got != "ses/inbound/msg-1" {
		t.Fatalf("unexpected object key: %q", got)
	}
	if got := inboundEmailObjectKey("", "msg-2"); got != "msg-2" {
		t.Fatalf("unexpected object key without prefix: %q", got)
	}
}

func TestParseRawEmail_MultipartPrefersPlainText(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: medic@inbound.lessersoul.ai",
		"Subject: =?UTF-8?Q?Hello_=E2=9C=A8?=",
		"Message-ID: <msg-1@example.com>",
		"In-Reply-To: <msg-0@example.com>",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="abc123"`,
		"",
		"--abc123",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Plain body",
		"--abc123",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>HTML body</p>",
		"--abc123--",
		"",
	}, "\r\n")

	parsed, err := parseRawEmail([]byte(raw), "alice@example.com", "", "ses-msg-1")
	if err != nil {
		t.Fatalf("parseRawEmail: %v", err)
	}
	if parsed.From.Address != "alice@example.com" || parsed.From.DisplayName != "Alice" {
		t.Fatalf("unexpected from: %#v", parsed.From)
	}
	if parsed.Subject != "Hello ✨" {
		t.Fatalf("unexpected subject: %q", parsed.Subject)
	}
	if parsed.Body != "Plain body" || parsed.BodyMimeType != bodyMimeTypePlainText {
		t.Fatalf("unexpected body: mime=%q body=%q", parsed.BodyMimeType, parsed.Body)
	}
	if parsed.MessageID != "msg-1@example.com" {
		t.Fatalf("unexpected message id: %q", parsed.MessageID)
	}
	if parsed.InReplyTo == nil || *parsed.InReplyTo != "msg-0@example.com" {
		t.Fatalf("unexpected in-reply-to: %#v", parsed.InReplyTo)
	}
}

func TestParseRawEmail_InvalidRawReturnsError(t *testing.T) {
	t.Parallel()

	_, err := parseRawEmail([]byte("not-an-email"), "fallback@example.com", "", "ses-msg-invalid")
	if err == nil || !strings.Contains(err.Error(), "parse raw email") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestParseRawEmail_MultipartFallsBackToHTML(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: medic@inbound.lessersoul.ai",
		"Subject: Hello",
		"Message-ID: <msg-2@example.com>",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="abc123"`,
		"",
		"--abc123",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>HTML body</p>",
		"--abc123--",
		"",
	}, "\r\n")

	parsed, err := parseRawEmail([]byte(raw), "alice@example.com", "", "ses-msg-2")
	if err != nil {
		t.Fatalf("parseRawEmail: %v", err)
	}
	if parsed.Body != "<p>HTML body</p>" || parsed.BodyMimeType != bodyMimeTypeHTML {
		t.Fatalf("unexpected html fallback body: mime=%q body=%q", parsed.BodyMimeType, parsed.Body)
	}
}

func TestParseRawEmail_UsesFallbacksForMissingHeaders(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"Content-Type: text/plain; charset=utf-8",
		"",
		"",
	}, "\r\n")

	parsed, err := parseRawEmail([]byte(raw), "fallback@example.com", "Fallback Subject", "ses-msg-3")
	if err != nil {
		t.Fatalf("parseRawEmail: %v", err)
	}
	if parsed.From.Address != "fallback@example.com" {
		t.Fatalf("unexpected fallback from: %#v", parsed.From)
	}
	if parsed.Subject != "Fallback Subject" {
		t.Fatalf("unexpected fallback subject: %q", parsed.Subject)
	}
	if parsed.Body != "(empty email body)" || parsed.BodyMimeType != bodyMimeTypePlainText {
		t.Fatalf("unexpected fallback body: mime=%q body=%q", parsed.BodyMimeType, parsed.Body)
	}
	if parsed.MessageID != "ses-msg-3" {
		t.Fatalf("unexpected fallback message id: %q", parsed.MessageID)
	}
	if parsed.InReplyTo != nil {
		t.Fatalf("expected missing in-reply-to, got %#v", parsed.InReplyTo)
	}
}

func TestParseInboundFrom_InvalidFallbackReturnsError(t *testing.T) {
	t.Parallel()

	_, err := parseInboundFrom(mail.Header{"From": []string{"not an address"}}, "still-not-an-address")
	if err == nil || !strings.Contains(err.Error(), "invalid from address") {
		t.Fatalf("expected invalid from error, got %v", err)
	}
}

func TestParseInboundFrom_UsesFallbackWhenHeaderIsMalformed(t *testing.T) {
	t.Parallel()

	from, err := parseInboundFrom(mail.Header{"From": []string{"bad header"}}, "Fallback <fallback@example.com>")
	if err != nil {
		t.Fatalf("parseInboundFrom: %v", err)
	}
	if from.Address != "fallback@example.com" || from.DisplayName != "Fallback" {
		t.Fatalf("unexpected fallback party: %#v", from)
	}
}

func TestHandleSESEvent_RequiresConfiguredBridge(t *testing.T) {
	t.Parallel()

	srv := &Server{
		cfg:  config.Config{},
		s3:   &fakeS3{},
		sqs:  &fakeSQS{},
		now:  time.Now,
		logf: func(string, ...any) {},
	}

	err := srv.HandleSESEvent(context.Background(), events.SimpleEmailEvent{})
	if err == nil || !strings.Contains(err.Error(), "COMM_QUEUE_URL is required") {
		t.Fatalf("expected comm queue configuration error, got %v", err)
	}
}

func TestHandleSESEvent_ValidatesDependenciesAndConfig(t *testing.T) {
	t.Parallel()

	event := events.SimpleEmailEvent{}

	if err := (*Server)(nil).HandleSESEvent(context.Background(), event); err == nil || !strings.Contains(err.Error(), "server is not configured") {
		t.Fatalf("expected nil server error, got %v", err)
	}

	srv := &Server{
		cfg:  config.Config{CommQueueURL: "queue"},
		s3:   &fakeS3{},
		sqs:  &fakeSQS{},
		now:  time.Now,
		logf: func(string, ...any) {},
	}
	if err := srv.HandleSESEvent(context.Background(), event); err == nil || !strings.Contains(err.Error(), "INBOUND_EMAIL_BUCKET_NAME is required") {
		t.Fatalf("expected missing bucket error, got %v", err)
	}

	srv.cfg.InboundEmailBucketName = "bucket"
	if err := srv.HandleSESEvent(context.Background(), event); err == nil || !strings.Contains(err.Error(), "SOUL_EMAIL_INBOUND_DOMAIN is required") {
		t.Fatalf("expected missing domain error, got %v", err)
	}
}

func TestHandleSESEvent_SkipsNonBridgeRecipient(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: medic@example.com",
		"Subject: Hello",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Email body",
		"",
	}, "\r\n")

	s3Client := &fakeS3{
		bodyByKey: map[string]string{
			"ses/inbound/ses-msg-2": raw,
		},
	}
	sqsClient := &fakeSQS{}
	logs := make([]string, 0, 1)
	srv := &Server{
		cfg: config.Config{
			CommQueueURL:           "https://sqs.us-east-1.amazonaws.com/123456789012/lesser-host-lab-comm-queue",
			SoulEmailInboundDomain: "inbound.lessersoul.ai",
			InboundEmailBucketName: "bucket",
			InboundEmailS3Prefix:   "ses/inbound/",
		},
		s3:  s3Client,
		sqs: sqsClient,
		now: time.Now,
		logf: func(format string, args ...any) {
			logs = append(logs, format)
		},
	}

	event := events.SimpleEmailEvent{
		Records: []events.SimpleEmailRecord{
			{
				SES: events.SimpleEmailService{
					Mail: events.SimpleEmailMessage{
						Source:      "alice@example.com",
						MessageID:   "ses-msg-2",
						Destination: []string{"medic@example.com"},
					},
				},
			},
		},
	}

	if err := srv.HandleSESEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleSESEvent: %v", err)
	}
	if len(sqsClient.bodies) != 0 {
		t.Fatalf("expected no queued messages, got %d", len(sqsClient.bodies))
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "skipping non-bridge recipient") {
		t.Fatalf("unexpected skip logs: %#v", logs)
	}
}

func TestHandleSESEvent_PropagatesLoadError(t *testing.T) {
	t.Parallel()

	srv := &Server{
		cfg: config.Config{
			CommQueueURL:           "https://sqs.us-east-1.amazonaws.com/123456789012/lesser-host-lab-comm-queue",
			SoulEmailInboundDomain: "inbound.lessersoul.ai",
			InboundEmailBucketName: "bucket",
			InboundEmailS3Prefix:   "ses/inbound/",
		},
		s3: &fakeS3{
			errByKey: map[string]error{
				"ses/inbound/ses-msg-err": io.EOF,
			},
		},
		sqs:  &fakeSQS{},
		now:  time.Now,
		logf: func(string, ...any) {},
	}

	event := events.SimpleEmailEvent{
		Records: []events.SimpleEmailRecord{
			{
				SES: events.SimpleEmailService{
					Mail: events.SimpleEmailMessage{MessageID: "ses-msg-err"},
				},
			},
		},
	}

	err := srv.HandleSESEvent(context.Background(), event)
	if err == nil || !strings.Contains(err.Error(), `load inbound email "ses/inbound/ses-msg-err"`) {
		t.Fatalf("expected load error, got %v", err)
	}
}

func TestHandleSESEvent_PropagatesQueueError(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: medic@inbound.lessersoul.ai",
		"Subject: Hello",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Email body",
		"",
	}, "\r\n")

	srv := &Server{
		cfg: config.Config{
			CommQueueURL:           "https://sqs.us-east-1.amazonaws.com/123456789012/lesser-host-lab-comm-queue",
			SoulEmailInboundDomain: "inbound.lessersoul.ai",
			InboundEmailBucketName: "bucket",
			InboundEmailS3Prefix:   "ses/inbound/",
		},
		s3: &fakeS3{
			bodyByKey: map[string]string{
				"ses/inbound/ses-msg-queue": raw,
			},
		},
		sqs:  &fakeSQS{err: io.ErrClosedPipe},
		now:  time.Now,
		logf: func(string, ...any) {},
	}

	event := events.SimpleEmailEvent{
		Records: []events.SimpleEmailRecord{
			{
				SES: events.SimpleEmailService{
					Mail: events.SimpleEmailMessage{
						Source:      "alice@example.com",
						MessageID:   "ses-msg-queue",
						Destination: []string{inboundBridgeAddress},
					},
				},
			},
		},
	}

	err := srv.HandleSESEvent(context.Background(), event)
	if err == nil || !strings.Contains(err.Error(), "enqueue inbound email") {
		t.Fatalf("expected enqueue error, got %v", err)
	}
}

func TestHandleSESEvent_UsesFallbackReceivedAtWhenTimestampMissing(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: medic@inbound.lessersoul.ai",
		"Subject: Hello",
		"Message-ID: <msg-fallback@example.com>",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Email body",
		"",
	}, "\r\n")

	s3Client := &fakeS3{
		bodyByKey: map[string]string{
			"ses/inbound/ses-msg-fallback": raw,
		},
	}
	sqsClient := &fakeSQS{}
	fallbackNow := time.Date(2026, 3, 21, 16, 0, 0, 0, time.UTC)
	srv := &Server{
		cfg: config.Config{
			CommQueueURL:           "https://sqs.us-east-1.amazonaws.com/123456789012/lesser-host-lab-comm-queue",
			SoulEmailInboundDomain: "inbound.lessersoul.ai",
			InboundEmailBucketName: "bucket",
			InboundEmailS3Prefix:   "ses/inbound/",
		},
		s3:   s3Client,
		sqs:  sqsClient,
		now:  func() time.Time { return fallbackNow },
		logf: func(string, ...any) {},
	}

	event := events.SimpleEmailEvent{
		Records: []events.SimpleEmailRecord{
			{
				SES: events.SimpleEmailService{
					Mail: events.SimpleEmailMessage{
						Source:      "alice@example.com",
						MessageID:   "ses-msg-fallback",
						Destination: []string{inboundBridgeAddress},
					},
				},
			},
		},
	}

	if err := srv.HandleSESEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleSESEvent: %v", err)
	}

	var msg commworker.QueueMessage
	if len(sqsClient.bodies) != 1 {
		t.Fatalf("expected 1 queued message, got %d", len(sqsClient.bodies))
	}
	if err := json.Unmarshal([]byte(sqsClient.bodies[0]), &msg); err != nil {
		t.Fatalf("unmarshal queued body: %v", err)
	}
	if msg.Notification.ReceivedAt != fallbackNow.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected fallback received_at: %q", msg.Notification.ReceivedAt)
	}
}

func TestDecodeTransferEncodingAndNormalizeMimeType(t *testing.T) {
	t.Parallel()

	reader := decodeTransferEncoding("quoted-printable", strings.NewReader("Hello=20world"))
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(decoded) != "Hello world" {
		t.Fatalf("unexpected decoded payload: %q", string(decoded))
	}
	reader = decodeTransferEncoding("base64", strings.NewReader("SGVsbG8gd29ybGQ="))
	decoded, err = io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll base64: %v", err)
	}
	if string(decoded) != "Hello world" {
		t.Fatalf("unexpected base64 payload: %q", string(decoded))
	}
	if got := normalizeBodyMimeType("application/json"); got != bodyMimeTypePlainText {
		t.Fatalf("unexpected default mime type: %q", got)
	}
}

func TestExtractEmailBody_MultipartMissingBoundary(t *testing.T) {
	t.Parallel()

	_, _, err := extractEmailBody(mail.Header{"Content-Type": []string{"multipart/alternative"}}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "multipart email missing boundary") {
		t.Fatalf("expected missing boundary error, got %v", err)
	}
}

func TestHandleSESEvent_EnqueuesCanonicalEmailNotification(t *testing.T) {
	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: medic@inbound.lessersoul.ai",
		"Subject: Hello",
		"Message-ID: <msg-1@example.com>",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Email body",
		"",
	}, "\r\n")

	s3Client := &fakeS3{
		bodyByKey: map[string]string{
			"ses/inbound/ses-msg-1": raw,
		},
	}
	sqsClient := &fakeSQS{}
	srv := &Server{
		cfg: config.Config{
			CommQueueURL:           "https://sqs.us-east-1.amazonaws.com/123456789012/lesser-host-lab-comm-queue",
			SoulEmailInboundDomain: "inbound.lessersoul.ai",
			InboundEmailBucketName: "bucket",
			InboundEmailS3Prefix:   "ses/inbound/",
		},
		s3:   s3Client,
		sqs:  sqsClient,
		now:  func() time.Time { return time.Date(2026, 3, 21, 15, 4, 5, 0, time.UTC) },
		logf: func(string, ...any) {},
	}

	event := events.SimpleEmailEvent{
		Records: []events.SimpleEmailRecord{
			{
				SES: events.SimpleEmailService{
					Mail: events.SimpleEmailMessage{
						Source:      "alice@example.com",
						MessageID:   "ses-msg-1",
						Timestamp:   time.Date(2026, 3, 21, 14, 0, 0, 0, time.UTC),
						Destination: []string{inboundBridgeAddress},
						CommonHeaders: events.SimpleEmailCommonHeaders{
							Subject: "Hello",
						},
					},
				},
			},
		},
	}

	if err := srv.HandleSESEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleSESEvent: %v", err)
	}
	if len(sqsClient.bodies) != 1 {
		t.Fatalf("expected 1 queued message, got %d", len(sqsClient.bodies))
	}

	var msg commworker.QueueMessage
	if err := json.Unmarshal([]byte(sqsClient.bodies[0]), &msg); err != nil {
		t.Fatalf("unmarshal queued body: %v", err)
	}
	if msg.Provider != "migadu" || msg.Notification.Channel != "email" {
		t.Fatalf("unexpected queued message: %#v", msg)
	}
	if msg.Notification.To == nil || msg.Notification.To.Address != "medic@lessersoul.ai" {
		t.Fatalf("unexpected destination: %#v", msg.Notification.To)
	}
	if msg.Notification.Body != "Email body" || msg.Notification.Subject != "Hello" {
		t.Fatalf("unexpected payload: %#v", msg.Notification)
	}
	if msg.Notification.MessageID != "msg-1@example.com" {
		t.Fatalf("unexpected message id: %q", msg.Notification.MessageID)
	}
}
