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
	Version                int                            `json:"version"`
	App                    string                         `json:"app"`
	BaseDomain             string                         `json:"base_domain"`
	AccountID              string                         `json:"account_id"`
	Region                 string                         `json:"region"`
	ManagedDeployArtifacts *managedDeployArtifactsReceipt `json:"managed_deploy_artifacts,omitempty"`
	HostedZone             struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"hosted_zone"`
}

type lesserBodyReceipt struct {
	Version                int                            `json:"version"`
	Stage                  string                         `json:"stage"`
	BaseDomain             string                         `json:"base_domain"`
	LesserBodyVersion      string                         `json:"lesser_body_version"`
	ManagedDeployArtifacts *managedDeployArtifactsReceipt `json:"managed_deploy_artifacts,omitempty"`
}

type mcpWiringReceipt struct {
	Version                int                            `json:"version"`
	Stage                  string                         `json:"stage"`
	BaseDomain             string                         `json:"base_domain"`
	LesserBodyVersion      string                         `json:"lesser_body_version"`
	McpURL                 string                         `json:"mcp_url"`
	McpLambdaARN           string                         `json:"mcp_lambda_arn"`
	ManagedDeployArtifacts *managedDeployArtifactsReceipt `json:"managed_deploy_artifacts,omitempty"`
}

type managedDeployArtifactsReceipt struct {
	Mode                string                             `json:"mode"`
	ChecksumsPath       string                             `json:"checksums_path,omitempty"`
	ReleaseManifestPath string                             `json:"release_manifest_path,omitempty"`
	Release             managedDeployReleaseReceipt        `json:"release"`
	DeployArtifact      managedDeployArtifactDetailReceipt `json:"deploy_artifact"`
}

type managedDeployReleaseReceipt struct {
	Name                   string `json:"name,omitempty"`
	Version                string `json:"version,omitempty"`
	GitSHA                 string `json:"git_sha,omitempty"`
	SourceCheckoutRequired *bool  `json:"source_checkout_required,omitempty"`
	NPMInstallRequired     *bool  `json:"npm_install_required,omitempty"`
}

type managedDeployArtifactDetailReceipt struct {
	Kind         string   `json:"kind,omitempty"`
	Path         string   `json:"path,omitempty"`
	ManifestPath string   `json:"manifest_path,omitempty"`
	ScriptPath   string   `json:"script_path,omitempty"`
	TemplatePath string   `json:"template_path,omitempty"`
	Files        []string `json:"files,omitempty"`
	PreparedAt   string   `json:"prepared_at,omitempty"`
}

func (s *Server) receiptS3Key(job *models.ProvisionJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/%s/state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}

func (s *Server) bodyReceiptS3Key(job *models.ProvisionJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/%s/body-state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}

func (s *Server) mcpReceiptS3Key(job *models.ProvisionJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/provisioning/%s/%s/mcp-state.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
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

func (s *Server) loadBodyReceiptFromS3(ctx context.Context, bucket string, key string) (string, *lesserBodyReceipt, error) {
	raw, err := s.loadS3ObjectString(ctx, bucket, key)
	if err != nil {
		return "", nil, err
	}

	var parsed lesserBodyReceipt
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw, nil, err
	}
	return raw, &parsed, nil
}

func (s *Server) loadMCPReceiptFromS3(ctx context.Context, bucket string, key string) (string, *mcpWiringReceipt, error) {
	raw, err := s.loadS3ObjectString(ctx, bucket, key)
	if err != nil {
		return "", nil, err
	}

	var parsed mcpWiringReceipt
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw, nil, err
	}
	return raw, &parsed, nil
}
