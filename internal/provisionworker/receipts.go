package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type lesserUpReceipt struct {
	Version    int    `json:"version"`
	App        string `json:"app"`
	BaseDomain string `json:"base_domain"`
	AccountID  string `json:"account_id"`
	Region     string `json:"region"`
	HostedZone struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"hosted_zone"`
}

type soulReceipt struct {
	Version        int    `json:"version"`
	App            string `json:"app"`
	InstanceDomain string `json:"instance_domain"`
	SoulVersion    string `json:"soul_version,omitempty"`
}

func (s *Server) receiptS3Key(job *models.ProvisionJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/%s/state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}

func (s *Server) soulReceiptS3Key(job *models.ProvisionJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/%s/soul-state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}

func (s *Server) bootstrapS3Key(job *models.ProvisionJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/bootstrap.json", strings.TrimSpace(job.InstanceSlug))
}

func (s *Server) loadS3ObjectString(ctx context.Context, bucket string, key string) (string, error) {
	if s == nil || s.s3 == nil {
		return "", fmt.Errorf("s3 client not initialized")
	}
	bucket = strings.TrimSpace(bucket)
	key = strings.TrimSpace(key)
	if bucket == "" || key == "" {
		return "", fmt.Errorf("bucket and key are required")
	}

	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = out.Body.Close() }()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return "", fmt.Errorf("receipt is empty")
	}
	return raw, nil
}

func (s *Server) loadReceiptFromS3(ctx context.Context, bucket string, key string) (string, *lesserUpReceipt, error) {
	raw, err := s.loadS3ObjectString(ctx, bucket, key)
	if err != nil {
		return "", nil, err
	}

	var parsed lesserUpReceipt
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw, nil, err
	}
	if strings.TrimSpace(parsed.BaseDomain) == "" || strings.TrimSpace(parsed.App) == "" {
		return raw, &parsed, fmt.Errorf("receipt is missing required fields")
	}
	return raw, &parsed, nil
}

func (s *Server) loadSoulReceiptFromS3(ctx context.Context, bucket string, key string) (string, *soulReceipt, error) {
	raw, err := s.loadS3ObjectString(ctx, bucket, key)
	if err != nil {
		return "", nil, err
	}

	var parsed soulReceipt
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw, nil, err
	}
	if strings.TrimSpace(parsed.App) == "" || strings.TrimSpace(parsed.InstanceDomain) == "" {
		return raw, &parsed, fmt.Errorf("receipt is missing required fields")
	}
	return raw, &parsed, nil
}
