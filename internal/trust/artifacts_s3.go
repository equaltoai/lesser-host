package trust

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type artifactStore struct {
	bucket string

	once   sync.Once
	client *s3.Client
	err    error
}

func newArtifactStore(bucket string) *artifactStore {
	return &artifactStore{bucket: strings.TrimSpace(bucket)}
}

func (a *artifactStore) s3Client(ctx context.Context) (*s3.Client, error) {
	if a == nil {
		return nil, fmt.Errorf("artifact store is nil")
	}
	a.once.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			a.err = err
			return
		}
		a.client = s3.NewFromConfig(cfg)
	})
	if a.err != nil {
		return nil, a.err
	}
	if a.client == nil {
		return nil, fmt.Errorf("s3 client not initialized")
	}
	return a.client, nil
}

func (a *artifactStore) putObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error {
	if a == nil {
		return fmt.Errorf("artifact store is nil")
	}
	bucket := strings.TrimSpace(a.bucket)
	if bucket == "" {
		return fmt.Errorf("artifact bucket name is not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("object key is required")
	}

	client, err := a.s3Client(ctx)
	if err != nil {
		return err
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	}
	if strings.TrimSpace(contentType) != "" {
		input.ContentType = aws.String(strings.TrimSpace(contentType))
	}
	if strings.TrimSpace(cacheControl) != "" {
		input.CacheControl = aws.String(strings.TrimSpace(cacheControl))
	}

	_, err = client.PutObject(ctx, input)
	return err
}

func (a *artifactStore) getObject(ctx context.Context, key string, maxBytes int64) ([]byte, string, string, error) {
	if a == nil {
		return nil, "", "", fmt.Errorf("artifact store is nil")
	}
	bucket := strings.TrimSpace(a.bucket)
	if bucket == "" {
		return nil, "", "", fmt.Errorf("artifact bucket name is not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, "", "", fmt.Errorf("object key is required")
	}

	client, err := a.s3Client(ctx)
	if err != nil {
		return nil, "", "", err
	}

	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, "", "", nsk
		}
		return nil, "", "", err
	}
	defer out.Body.Close()

	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	lr := &io.LimitedReader{R: out.Body, N: maxBytes + 1}
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, "", "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, "", "", fmt.Errorf("object too large")
	}

	contentType := strings.TrimSpace(aws.ToString(out.ContentType))
	etag := strings.TrimSpace(aws.ToString(out.ETag))
	return body, contentType, etag, nil
}
