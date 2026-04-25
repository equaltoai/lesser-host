// Package commmailbox contains shared bounded content-storage helpers for the
// host-authoritative soul comm mailbox.
package commmailbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	// ContentKeyPrefix is the lifecycle-bound S3 prefix used by CDK.
	ContentKeyPrefix = "mailbox/v1/"
	// ContentStorageS3 marks mailbox content pointers backed by S3.
	ContentStorageS3 = "s3"
)

// ContentInput is the body payload stored for one canonical mailbox delivery.
type ContentInput struct {
	DeliveryID      string
	InstanceSlug    string
	AgentID         string
	MessageID       string
	Direction       string
	ChannelType     string
	Body            string
	ContentMimeType string
}

// ContentPointer identifies a lifecycle-bound encrypted content object.
type ContentPointer struct {
	Storage     string
	Bucket      string
	Key         string
	SHA256      string
	Bytes       int64
	ContentType string
}

// ContentOutput is explicit mailbox body content fetched from bounded storage.
type ContentOutput struct {
	Body        []byte
	ContentType string
	SHA256      string
	Bytes       int64
}

// ContentStore writes and reads bounded mailbox content.
type ContentStore interface {
	PutContent(ctx context.Context, input ContentInput) (ContentPointer, error)
	GetContent(ctx context.Context, pointer ContentPointer, maxBytes int64) (ContentOutput, error)
}

type s3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3Store stores mailbox content in the dedicated CDK-managed mailbox bucket.
type S3Store struct {
	bucket string

	once   sync.Once
	client s3API
	err    error
}

// NewS3Store constructs an S3-backed content store.
func NewS3Store(bucket string) *S3Store {
	return &S3Store{bucket: strings.TrimSpace(bucket)}
}

// NewS3StoreWithClient constructs an S3-backed content store with an injected client.
func NewS3StoreWithClient(bucket string, client s3API) *S3Store {
	st := NewS3Store(bucket)
	if client != nil {
		st.once.Do(func() {})
		st.client = client
	}
	return st
}

// PutContent writes content to the configured mailbox bucket and returns a
// pointer + digest. The bucket enforces encryption at rest and lifecycle expiry.
func (s *S3Store) PutContent(ctx context.Context, input ContentInput) (ContentPointer, error) {
	if s == nil {
		return ContentPointer{}, fmt.Errorf("mailbox content store is nil")
	}
	bucket := strings.TrimSpace(s.bucket)
	if bucket == "" {
		return ContentPointer{}, fmt.Errorf("mailbox content bucket is not configured")
	}
	if strings.TrimSpace(input.DeliveryID) == "" {
		return ContentPointer{}, fmt.Errorf("deliveryId is required")
	}
	if strings.TrimSpace(input.AgentID) == "" {
		return ContentPointer{}, fmt.Errorf("agentId is required")
	}
	body := []byte(input.Body)
	if len(bytes.TrimSpace(body)) == 0 {
		return ContentPointer{}, fmt.Errorf("content body is required")
	}

	client, err := s.s3Client(ctx)
	if err != nil {
		return ContentPointer{}, err
	}

	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	contentType := strings.TrimSpace(input.ContentMimeType)
	if contentType == "" {
		contentType = DefaultContentType(input.ChannelType)
	}
	key := ContentKey(input.InstanceSlug, input.AgentID, input.DeliveryID)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
		Metadata: map[string]string{
			"delivery-id":   strings.TrimSpace(input.DeliveryID),
			"agent-id":      strings.ToLower(strings.TrimSpace(input.AgentID)),
			"instance-slug": strings.ToLower(strings.TrimSpace(input.InstanceSlug)),
			"message-id":    strings.TrimSpace(input.MessageID),
			"direction":     strings.ToLower(strings.TrimSpace(input.Direction)),
			"channel-type":  strings.ToLower(strings.TrimSpace(input.ChannelType)),
			"sha256":        digest,
		},
	})
	if err != nil {
		return ContentPointer{}, err
	}

	return ContentPointer{
		Storage:     ContentStorageS3,
		Bucket:      bucket,
		Key:         key,
		SHA256:      digest,
		Bytes:       int64(len(body)),
		ContentType: contentType,
	}, nil
}

// GetContent reads a bounded mailbox content object and verifies its digest when
// the mailbox row carries one.
func (s *S3Store) GetContent(ctx context.Context, pointer ContentPointer, maxBytes int64) (ContentOutput, error) {
	if s == nil {
		return ContentOutput{}, fmt.Errorf("mailbox content store is nil")
	}
	bucket := strings.TrimSpace(s.bucket)
	if bucket == "" {
		return ContentOutput{}, fmt.Errorf("mailbox content bucket is not configured")
	}
	if strings.TrimSpace(pointer.Bucket) != "" && strings.TrimSpace(pointer.Bucket) != bucket {
		return ContentOutput{}, fmt.Errorf("content pointer bucket mismatch")
	}
	key := strings.TrimSpace(pointer.Key)
	if key == "" {
		return ContentOutput{}, fmt.Errorf("content key is required")
	}
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024
	}

	client, err := s.s3Client(ctx)
	if err != nil {
		return ContentOutput{}, err
	}
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return ContentOutput{}, err
	}
	defer out.Body.Close()

	body, err := io.ReadAll(io.LimitReader(out.Body, maxBytes+1))
	if err != nil {
		return ContentOutput{}, err
	}
	if int64(len(body)) > maxBytes {
		return ContentOutput{}, fmt.Errorf("content exceeds maxBytes")
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	if want := strings.ToLower(strings.TrimSpace(pointer.SHA256)); want != "" && want != digest {
		return ContentOutput{}, fmt.Errorf("content sha256 mismatch")
	}
	contentType := strings.TrimSpace(pointer.ContentType)
	if out.ContentType != nil && strings.TrimSpace(*out.ContentType) != "" {
		contentType = strings.TrimSpace(*out.ContentType)
	}
	return ContentOutput{Body: body, ContentType: contentType, SHA256: digest, Bytes: int64(len(body))}, nil
}

func (s *S3Store) s3Client(ctx context.Context) (s3API, error) {
	if s == nil {
		return nil, fmt.Errorf("mailbox content store is nil")
	}
	s.once.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			s.err = err
			return
		}
		s.client = s3.NewFromConfig(cfg)
	})
	if s.err != nil {
		return nil, s.err
	}
	if s.client == nil {
		return nil, fmt.Errorf("s3 client not initialized")
	}
	return s.client, nil
}

// ContentKey returns the S3 object key for a mailbox delivery body.
func ContentKey(instanceSlug string, agentID string, deliveryID string) string {
	return ContentKeyPrefix +
		"instances/" + safeSegment(instanceSlug) +
		"/agents/" + safeSegment(agentID) +
		"/deliveries/" + safeSegment(deliveryID) +
		"/content"
}

// DefaultContentType returns the content type used when a provider does not supply one.
func DefaultContentType(channelType string) string {
	switch strings.ToLower(strings.TrimSpace(channelType)) {
	case "email", "sms", "voice":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func safeSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:])[:24]
	}
	return b.String()
}
