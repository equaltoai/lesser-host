package emailingress

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/config"
)

const (
	ServiceName              = "email-ingress"
	soulCanonicalEmailDomain = "lessersoul.ai"
	bodyMimeTypePlainText    = "text/plain"
	bodyMimeTypeHTML         = "text/html"
	maxInboundEmailBytes     = 1024 * 1024
)

type s3API interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type sqsAPI interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type Server struct {
	cfg config.Config

	s3  s3API
	sqs sqsAPI

	now  func() time.Time
	logf func(format string, args ...any)
}

type parsedInboundEmail struct {
	From         commworker.InboundParty
	Subject      string
	Body         string
	BodyMimeType string
	MessageID    string
	InReplyTo    *string
}

func NewServer() *Server {
	cfg := config.Load()
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return &Server{
		cfg: cfg,
		s3:  s3.NewFromConfig(awsCfg),
		sqs: sqs.NewFromConfig(awsCfg),
		now: func() time.Time {
			return time.Now().UTC()
		},
		logf: func(format string, args ...any) {
			logger.Info(fmt.Sprintf(format, args...))
		},
	}
}

func (s *Server) HandleSESEvent(ctx context.Context, event events.SimpleEmailEvent) error {
	if s == nil || s.s3 == nil || s.sqs == nil {
		return fmt.Errorf("server is not configured")
	}
	if strings.TrimSpace(s.cfg.CommQueueURL) == "" {
		return fmt.Errorf("COMM_QUEUE_URL is required")
	}
	if strings.TrimSpace(s.cfg.InboundEmailBucketName) == "" {
		return fmt.Errorf("INBOUND_EMAIL_BUCKET_NAME is required")
	}
	inboundDomain := strings.ToLower(strings.TrimSpace(s.cfg.SoulEmailInboundDomain))
	if inboundDomain == "" {
		return fmt.Errorf("SOUL_EMAIL_INBOUND_DOMAIN is required")
	}

	for _, record := range event.Records {
		if err := s.handleRecord(ctx, record, inboundDomain); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleRecord(ctx context.Context, record events.SimpleEmailRecord, inboundDomain string) error {
	messageID := strings.TrimSpace(record.SES.Mail.MessageID)
	if messageID == "" {
		return fmt.Errorf("ses message id is required")
	}
	if !sesReceiptVerdictsAccepted(record.SES.Receipt) {
		s.logf("emailingress: rejecting unauthenticated or unsafe SES message %q", safeLogToken(messageID))
		return nil
	}
	rawMessage, err := s.loadRawEmail(ctx, messageID)
	if err != nil {
		return err
	}

	parsed, err := parseRawEmail(rawMessage, record.SES.Mail.Source, record.SES.Mail.CommonHeaders.Subject, messageID)
	if err != nil {
		return err
	}
	senderSoulAttribution := senderSoulAttributionPolicy(record.SES.Mail.Source, record.SES.Receipt, rawMessage)

	receivedAt := record.SES.Mail.Timestamp.UTC()
	if receivedAt.IsZero() {
		receivedAt = s.now()
	}

	for _, destination := range sesEnvelopeRecipients(record) {
		toAddress, ok := normalizeInboundRecipient(destination, inboundDomain)
		if !ok {
			s.logf("emailingress: skipping non-bridge recipient %q", safeLogToken(destination))
			continue
		}
		msg := commworker.QueueMessage{
			Kind:                  commworker.QueueMessageKindInbound,
			Provider:              "migadu",
			SenderSoulAttribution: senderSoulAttribution,
			Notification: commworker.InboundNotification{
				Type:         "communication:inbound",
				Channel:      "email",
				From:         parsed.From,
				To:           &commworker.InboundParty{Address: toAddress},
				Subject:      parsed.Subject,
				Body:         parsed.Body,
				BodyMimeType: parsed.BodyMimeType,
				ReceivedAt:   receivedAt.Format(time.RFC3339Nano),
				MessageID:    parsed.MessageID,
				InReplyTo:    parsed.InReplyTo,
			},
		}
		if err := msg.Validate(); err != nil {
			return fmt.Errorf("validate inbound email %q: %w", toAddress, err)
		}
		if err := s.enqueueCommMessage(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

func sesReceiptVerdictsAccepted(receipt events.SimpleEmailReceipt) bool {
	if !sesVerdictPass(receipt.SpamVerdict) || !sesVerdictPass(receipt.VirusVerdict) {
		return false
	}
	return sesVerdictPass(receipt.SPFVerdict) ||
		sesVerdictPass(receipt.DKIMVerdict) ||
		sesVerdictPass(receipt.DMARCVerdict)
}

func sesVerdictPass(verdict events.SimpleEmailVerdict) bool {
	return strings.EqualFold(strings.TrimSpace(verdict.Status), "PASS")
}

func senderSoulAttributionPolicy(source string, receipt events.SimpleEmailReceipt, rawMessage []byte) *commworker.SenderSoulAttributionPolicy {
	sourceDomain := emailDomain(source)
	if sourceDomain == "" {
		return nil
	}
	// SES evaluates SPF against the SMTP envelope MAIL FROM domain, which is
	// the same identity exposed as Mail.Source.
	if sesVerdictPass(receipt.SPFVerdict) {
		return &commworker.SenderSoulAttributionPolicy{Allowed: true, Method: "spf"}
	}

	// SES evaluates DMARC/DKIM against the RFC 5322 From domain. Require that
	// domain to align with Mail.Source before using those verdicts to authorize
	// Mail.Source-driven soul attribution.
	headerFromDomain := rawHeaderFromDomain(rawMessage)
	if !domainsAlign(headerFromDomain, sourceDomain) {
		return nil
	}
	if sesVerdictPass(receipt.DMARCVerdict) {
		return &commworker.SenderSoulAttributionPolicy{Allowed: true, Method: "dmarc"}
	}
	if sesVerdictPass(receipt.DKIMVerdict) {
		return &commworker.SenderSoulAttributionPolicy{Allowed: true, Method: "dkim"}
	}
	return nil
}

func rawHeaderFromDomain(rawMessage []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(rawMessage))
	if err != nil {
		return ""
	}
	return emailDomain(msg.Header.Get("From"))
}

func emailDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := mail.ParseAddress(raw); err == nil {
		raw = parsed.Address
	}
	return normalizedEmailDomain(raw)
}

func normalizedEmailDomain(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.Trim(value, "<>\"'()[]{}")
	if value == "" {
		return ""
	}
	if at := strings.LastIndex(value, "@"); at >= 0 {
		value = value[at+1:]
	}
	value = strings.Trim(value, ".;,)<>\"'")
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	return value
}

func domainsAlign(authDomain string, sourceDomain string) bool {
	authDomain = normalizedEmailDomain(authDomain)
	sourceDomain = normalizedEmailDomain(sourceDomain)
	if authDomain == "" || sourceDomain == "" {
		return false
	}
	if authDomain == sourceDomain {
		return true
	}
	if !strings.Contains(authDomain, ".") || !strings.Contains(sourceDomain, ".") {
		return false
	}
	return strings.HasSuffix(authDomain, "."+sourceDomain) ||
		strings.HasSuffix(sourceDomain, "."+authDomain)
}

func safeLogToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t':
			return ' '
		default:
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	return strings.NewReplacer("=", ":", "\"", "'", "`", "'").Replace(value)
}

func sesEnvelopeRecipients(record events.SimpleEmailRecord) []string {
	if len(record.SES.Receipt.Recipients) > 0 {
		return record.SES.Receipt.Recipients
	}
	return record.SES.Mail.Destination
}

func (s *Server) loadRawEmail(ctx context.Context, messageID string) ([]byte, error) {
	if s == nil || s.s3 == nil {
		return nil, fmt.Errorf("s3 client is not configured")
	}
	key := inboundEmailObjectKey(strings.TrimSpace(s.cfg.InboundEmailS3Prefix), messageID)
	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(strings.TrimSpace(s.cfg.InboundEmailBucketName)),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("load inbound email %q: %w", key, err)
	}
	defer out.Body.Close()

	body, err := readAllUpTo(out.Body, maxInboundEmailBytes)
	if err != nil {
		return nil, fmt.Errorf("read inbound email %q: %w", key, err)
	}
	return body, nil
}

func readAllUpTo(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(r)
	}
	lr := &io.LimitedReader{R: r, N: maxBytes + 1}
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("payload too large")
	}
	return body, nil
}

func (s *Server) enqueueCommMessage(ctx context.Context, msg commworker.QueueMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = s.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(strings.TrimSpace(s.cfg.CommQueueURL)),
		MessageBody: aws.String(string(body)),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"provider": {DataType: aws.String("String"), StringValue: aws.String("migadu")},
			"channel":  {DataType: aws.String("String"), StringValue: aws.String("email")},
		},
	})
	if err != nil {
		return fmt.Errorf("enqueue inbound email: %w", err)
	}
	return nil
}

func inboundEmailObjectKey(prefix string, messageID string) string {
	prefix = strings.TrimSpace(prefix)
	messageID = strings.TrimSpace(messageID)
	if prefix == "" {
		return messageID
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return path.Clean(prefix + messageID)
}

func normalizeInboundRecipient(address string, inboundDomain string) (string, bool) {
	address = strings.ToLower(strings.TrimSpace(address))
	inboundDomain = strings.ToLower(strings.TrimSpace(inboundDomain))
	localPart, domain, ok := strings.Cut(address, "@")
	if !ok || strings.TrimSpace(localPart) == "" {
		return "", false
	}
	if domain != inboundDomain {
		return "", false
	}
	return localPart + "@" + soulCanonicalEmailDomain, true
}

func parseRawEmail(raw []byte, fallbackSource string, fallbackSubject string, fallbackMessageID string) (parsedInboundEmail, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return parsedInboundEmail{}, fmt.Errorf("parse raw email: %w", err)
	}

	from, err := parseInboundFrom(msg.Header, fallbackSource)
	if err != nil {
		return parsedInboundEmail{}, err
	}

	subject := decodeRFC2047Header(msg.Header.Get("Subject"))
	if strings.TrimSpace(subject) == "" {
		subject = strings.TrimSpace(fallbackSubject)
	}

	body, bodyMimeType, err := extractEmailBody(mail.Header(msg.Header), msg.Body)
	if err != nil {
		return parsedInboundEmail{}, err
	}
	if strings.TrimSpace(body) == "" {
		body = "(empty email body)"
	}

	messageID := strings.TrimSpace(msg.Header.Get("Message-ID"))
	messageID = strings.Trim(messageID, "<>")
	if messageID == "" {
		messageID = strings.TrimSpace(fallbackMessageID)
	}
	inReplyTo := trimEmailHeaderID(msg.Header.Get("In-Reply-To"))

	return parsedInboundEmail{
		From:         from,
		Subject:      subject,
		Body:         body,
		BodyMimeType: bodyMimeType,
		MessageID:    messageID,
		InReplyTo:    inReplyTo,
	}, nil
}

func parseInboundFrom(header mail.Header, fallbackSource string) (commworker.InboundParty, error) {
	rawFrom := strings.TrimSpace(header.Get("From"))
	var headerAddr *mail.Address
	if rawFrom != "" {
		if parsed, err := mail.ParseAddress(rawFrom); err == nil {
			headerAddr = parsed
		}
	}

	rawSource := strings.TrimSpace(fallbackSource)
	if rawSource != "" {
		sourceAddr, err := mail.ParseAddress(rawSource)
		if err != nil {
			return commworker.InboundParty{}, fmt.Errorf("invalid from address: %w", err)
		}
		displayName := strings.TrimSpace(sourceAddr.Name)
		if headerAddr != nil &&
			strings.EqualFold(strings.TrimSpace(headerAddr.Address), strings.TrimSpace(sourceAddr.Address)) &&
			strings.TrimSpace(headerAddr.Name) != "" {
			displayName = strings.TrimSpace(headerAddr.Name)
		}
		return commworker.InboundParty{
			Address:     strings.TrimSpace(sourceAddr.Address),
			DisplayName: displayName,
		}, nil
	}

	if headerAddr == nil {
		if rawFrom == "" {
			return commworker.InboundParty{}, fmt.Errorf("from address is required")
		}
		return commworker.InboundParty{}, fmt.Errorf("invalid from address")
	}
	return commworker.InboundParty{
		Address:     strings.TrimSpace(headerAddr.Address),
		DisplayName: strings.TrimSpace(headerAddr.Name),
	}, nil
}

func extractEmailBody(header mail.Header, body io.Reader) (string, string, error) {
	contentType := strings.TrimSpace(header.Get("Content-Type"))
	if contentType == "" {
		contentType = bodyMimeTypePlainText + "; charset=utf-8"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = bodyMimeTypePlainText
	}
	decodedBody := decodeTransferEncoding(strings.TrimSpace(header.Get("Content-Transfer-Encoding")), body)

	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		boundary := strings.TrimSpace(params["boundary"])
		if boundary == "" {
			return "", "", fmt.Errorf("multipart email missing boundary")
		}
		return extractMultipartEmailBody(boundary, decodedBody)
	}

	payload, err := io.ReadAll(io.LimitReader(decodedBody, 512*1024))
	if err != nil {
		return "", "", fmt.Errorf("read email body: %w", err)
	}
	bodyText := strings.TrimSpace(string(payload))
	if bodyText == "" {
		return "", normalizeBodyMimeType(mediaType), nil
	}
	return bodyText, normalizeBodyMimeType(mediaType), nil
}

func extractMultipartEmailBody(boundary string, body io.Reader) (string, string, error) {
	reader := multipart.NewReader(body, boundary)
	htmlBody := ""
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("read multipart email: %w", err)
		}

		partHeader := mail.Header(textproto.MIMEHeader(part.Header))
		partBody, partMimeType, err := extractEmailBody(partHeader, part)
		if err != nil {
			return "", "", err
		}
		switch partMimeType {
		case bodyMimeTypePlainText:
			if strings.TrimSpace(partBody) != "" {
				return partBody, partMimeType, nil
			}
		case bodyMimeTypeHTML:
			if strings.TrimSpace(partBody) != "" && htmlBody == "" {
				htmlBody = partBody
			}
		}
	}
	if htmlBody != "" {
		return htmlBody, bodyMimeTypeHTML, nil
	}
	return "", bodyMimeTypePlainText, nil
}

func decodeTransferEncoding(encoding string, body io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		return quotedprintable.NewReader(body)
	default:
		return body
	}
}

func normalizeBodyMimeType(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch mediaType {
	case bodyMimeTypeHTML:
		return bodyMimeTypeHTML
	default:
		return bodyMimeTypePlainText
	}
}

func decodeRFC2047Header(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return value
	}
	return strings.TrimSpace(decoded)
}

func trimEmailHeaderID(value string) *string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "<>")
	if value == "" {
		return nil
	}
	return &value
}
