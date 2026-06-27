package provisionworker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
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
	ManagedInstanceKey     *managedInstanceKeyReceipt     `json:"managed_instance_key,omitempty"`
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
	ManagedInstanceKey     *managedInstanceKeyReceipt     `json:"managed_instance_key,omitempty"`
}

type mcpWiringReceipt struct {
	Version                int                            `json:"version"`
	Stage                  string                         `json:"stage"`
	BaseDomain             string                         `json:"base_domain"`
	LesserBodyVersion      string                         `json:"lesser_body_version"`
	McpURL                 string                         `json:"mcp_url"`
	McpLambdaARN           string                         `json:"mcp_lambda_arn"`
	ManagedDeployArtifacts *managedDeployArtifactsReceipt `json:"managed_deploy_artifacts,omitempty"`
	ManagedInstanceKey     *managedInstanceKeyReceipt     `json:"managed_instance_key,omitempty"`
}

type managedLesserBodyTemplateArtifact struct {
	Version           int    `json:"version"`
	Status            string `json:"status"`
	LesserBodyVersion string `json:"lesser_body_version,omitempty"`
	TemplatePath      string `json:"template_path,omitempty"`
	StackName         string `json:"stack_name,omitempty"`
	VerificationMode  string `json:"verification_mode,omitempty"`
	Detail            string `json:"detail,omitempty"`
	VerifiedAt        string `json:"verified_at,omitempty"`
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

const managedInstanceKeyReceiptSourceDeployRunner = "deploy-runner-managed-profile"

type managedInstanceKeyReceipt struct {
	Version      int    `json:"version"`
	Source       string `json:"source,omitempty"`
	SecretARN    string `json:"secret_arn,omitempty"`
	KeyID        string `json:"key_id,omitempty"`
	InstanceSlug string `json:"instance_slug,omitempty"`
	Stage        string `json:"stage,omitempty"`
	Rotated      bool   `json:"rotated,omitempty"`
	VerifiedAt   string `json:"verified_at,omitempty"`
}

func managedInstanceKeyReceiptFromJSON(raw string) (*managedInstanceKeyReceipt, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var parsed struct {
		ManagedInstanceKey *managedInstanceKeyReceipt `json:"managed_instance_key,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, err
	}
	return parsed.ManagedInstanceKey, nil
}

func validateManagedInstanceKeyReceiptKeyID(keyID string) error {
	keyID = strings.TrimSpace(keyID)
	if len(keyID) != 64 {
		return fmt.Errorf("managed instance key receipt has invalid key id")
	}
	if _, err := hex.DecodeString(keyID); err != nil {
		return fmt.Errorf("managed instance key receipt has invalid key id")
	}
	return nil
}

func validateManagedInstanceKeyReceiptSecretARN(job *models.UpdateJob, secretARN string) error {
	secretARN = strings.TrimSpace(secretARN)
	if secretARN == "" {
		return fmt.Errorf("managed instance key receipt secret ARN is invalid")
	}
	parsed, err := arn.Parse(secretARN)
	if err != nil || parsed.Service != "secretsmanager" || strings.TrimSpace(parsed.Region) == "" || strings.TrimSpace(parsed.AccountID) == "" || !strings.HasPrefix(parsed.Resource, "secret:") {
		return fmt.Errorf("managed instance key receipt secret ARN is invalid")
	}
	if want := strings.TrimSpace(job.AccountID); want != "" && parsed.AccountID != want {
		return fmt.Errorf("managed instance key receipt secret ARN account does not match update job")
	}
	if want := strings.TrimSpace(job.Region); want != "" && parsed.Region != want {
		return fmt.Errorf("managed instance key receipt secret ARN region does not match update job")
	}
	return nil
}

func (s *Server) validateManagedInstanceKeyReceipt(job *models.UpdateJob, receipt *managedInstanceKeyReceipt) error {
	if s == nil {
		return fmt.Errorf("server is nil")
	}
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	if receipt == nil {
		return fmt.Errorf("managed instance key proof missing from receipt")
	}
	if receipt.Version != 1 {
		return fmt.Errorf("unsupported managed instance key receipt version")
	}
	if strings.TrimSpace(receipt.Source) != managedInstanceKeyReceiptSourceDeployRunner {
		return fmt.Errorf("managed instance key receipt source is invalid")
	}
	slug := strings.ToLower(strings.TrimSpace(job.InstanceSlug))
	if slug == "" || strings.ToLower(strings.TrimSpace(receipt.InstanceSlug)) != slug {
		return fmt.Errorf("managed instance key receipt slug does not match update job")
	}
	if strings.TrimSpace(receipt.Stage) == "" {
		return fmt.Errorf("managed instance key receipt stage is missing")
	}
	if got, want := managedInstanceKeySecretStage(receipt.Stage), managedInstanceKeySecretStage(s.cfg.Stage); got != want {
		return fmt.Errorf("managed instance key receipt stage does not match control plane stage")
	}
	if err := validateManagedInstanceKeyReceiptSecretARN(job, receipt.SecretARN); err != nil {
		return err
	}
	if err := validateManagedInstanceKeyReceiptKeyID(receipt.KeyID); err != nil {
		return err
	}
	return nil
}

func (s *Server) applyManagedInstanceKeyReceipt(ctx context.Context, job *models.UpdateJob, receipt *managedInstanceKeyReceipt) error {
	if receipt == nil {
		return nil
	}
	if err := s.validateManagedInstanceKeyReceipt(job, receipt); err != nil {
		return err
	}
	keyID := strings.ToLower(strings.TrimSpace(receipt.KeyID))
	if err := s.ensureInstanceKeyRecord(ctx, strings.TrimSpace(job.InstanceSlug), keyID); err != nil {
		return fmt.Errorf("ensure instance key record from receipt: %w", err)
	}
	job.LesserHostInstanceKeySecretARN = strings.TrimSpace(receipt.SecretARN)
	if receipt.Rotated || job.RotateInstanceKey {
		job.RotatedInstanceKeyID = keyID
	}
	return nil
}

func (s *Server) applyManagedInstanceKeyReceiptJSON(ctx context.Context, job *models.UpdateJob, receiptJSON string) error {
	receipt, err := managedInstanceKeyReceiptFromJSON(receiptJSON)
	if err != nil {
		return err
	}
	return s.applyManagedInstanceKeyReceipt(ctx, job, receipt)
}

func (s *Server) ensureUpdateReceiptInstanceKeyRecord(ctx context.Context, job *models.UpdateJob) (*managedInstanceKeyReceipt, error) {
	if job == nil {
		return nil, fmt.Errorf("job is nil")
	}
	receipt, err := managedInstanceKeyReceiptFromJSON(job.ReceiptJSON)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, fmt.Errorf("managed instance key proof missing from receipt")
	}
	if err := s.applyManagedInstanceKeyReceipt(ctx, job, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
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

func (s *Server) loadManagedLesserBodyTemplateArtifactFromS3(ctx context.Context, bucket string, key string) (string, *managedLesserBodyTemplateArtifact, error) {
	raw, err := s.loadS3ObjectString(ctx, bucket, key)
	if err != nil {
		return "", nil, err
	}

	var parsed managedLesserBodyTemplateArtifact
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw, nil, err
	}
	return raw, &parsed, nil
}
