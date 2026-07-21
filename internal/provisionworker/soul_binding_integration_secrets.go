package provisionworker

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// The soul-binding integration secret is the shared Ptah-to-Lesser credential: the deploy
// runner ensures one Secrets Manager secret per managed instance and injects the same ARN
// into Lesser (SOUL_BINDING_INTEGRATION_KEY_ARN) and lesser-body
// (LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN). Host only ever handles the ARN and the
// sha256 key id; the bearer value stays inside the managed instance account.

const soulBindingIntegrationSecretARNPrefix = "arn:aws:secretsmanager:"

func soulBindingIntegrationSecretName(targetDeploymentStage, slug string) string {
	stage := normalizeManagedLesserStage(targetDeploymentStage)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/soul-binding-integration", stage, slug)
}

type soulBindingIntegrationReceipt struct {
	Version      int    `json:"version"`
	Source       string `json:"source,omitempty"`
	SecretARN    string `json:"secret_arn,omitempty"`
	KeyID        string `json:"key_id,omitempty"`
	InstanceSlug string `json:"instance_slug,omitempty"`
	Stage        string `json:"stage,omitempty"`
	VerifiedAt   string `json:"verified_at,omitempty"`
}

func soulBindingIntegrationReceiptFromJSON(raw string) (*soulBindingIntegrationReceipt, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var parsed struct {
		SoulBindingIntegration *soulBindingIntegrationReceipt `json:"soul_binding_integration,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, err
	}
	return parsed.SoulBindingIntegration, nil
}

func validateSoulBindingIntegrationReceiptKeyID(keyID string) error {
	keyID = strings.TrimSpace(keyID)
	if len(keyID) != 64 {
		return fmt.Errorf("soul binding integration receipt has invalid key id")
	}
	if _, err := hex.DecodeString(keyID); err != nil {
		return fmt.Errorf("soul binding integration receipt has invalid key id")
	}
	return nil
}

func validateSoulBindingIntegrationSecretRef(binding managedInstanceKeyReceiptBinding, targetDeploymentStage string, secretRef string) error {
	canonicalName := soulBindingIntegrationSecretName(targetDeploymentStage, binding.slug)
	secretRef = strings.TrimSpace(secretRef)
	if canonicalName == "" || secretRef == "" {
		return fmt.Errorf("soul binding integration secret reference is invalid")
	}
	if secretRef == canonicalName {
		return nil
	}
	if !strings.HasPrefix(secretRef, soulBindingIntegrationSecretARNPrefix) {
		return fmt.Errorf("soul binding integration secret name does not match canonical target stage and slug")
	}

	parsed, err := arn.Parse(secretRef)
	if err != nil || parsed.Service != "secretsmanager" || strings.TrimSpace(parsed.Region) == "" || strings.TrimSpace(parsed.AccountID) == "" || !strings.HasPrefix(parsed.Resource, "secret:") {
		return fmt.Errorf("soul binding integration secret reference is invalid")
	}
	if want := strings.TrimSpace(binding.accountID); want != "" && parsed.AccountID != want {
		return fmt.Errorf("soul binding integration secret ARN account does not match %s", binding.kind)
	}
	if want := strings.TrimSpace(binding.region); want != "" && parsed.Region != want {
		return fmt.Errorf("soul binding integration secret ARN region does not match %s", binding.kind)
	}
	if !secretsManagerARNResourceMatchesName(parsed.Resource, canonicalName) {
		return fmt.Errorf("soul binding integration secret ARN name does not match canonical target stage and slug")
	}
	return nil
}

func secretsManagerARNResourceMatchesName(resource string, canonicalName string) bool {
	secretName := strings.TrimPrefix(strings.TrimSpace(resource), "secret:")
	canonicalName = strings.TrimSpace(canonicalName)
	if secretName == canonicalName {
		return true
	}
	prefix := canonicalName + "-"
	if !strings.HasPrefix(secretName, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(secretName, prefix)
	if len(suffix) != 6 {
		return false
	}
	for _, r := range suffix {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validateSoulBindingIntegrationReceiptSecretARN(binding managedInstanceKeyReceiptBinding, targetDeploymentStage string, secretARN string) error {
	secretARN = strings.TrimSpace(secretARN)
	if secretARN == "" {
		return fmt.Errorf("soul binding integration receipt secret ARN is invalid")
	}
	if err := validateSoulBindingIntegrationSecretRef(binding, targetDeploymentStage, secretARN); err != nil {
		return fmt.Errorf("soul binding integration receipt secret ARN validation failed: %w", err)
	}
	return nil
}

func (s *Server) validateSoulBindingIntegrationReceiptForBinding(binding managedInstanceKeyReceiptBinding, receipt *soulBindingIntegrationReceipt) error {
	if s == nil {
		return fmt.Errorf("server is nil")
	}
	if strings.TrimSpace(binding.kind) == "" {
		binding.kind = "job"
	}
	if strings.TrimSpace(binding.slug) == "" {
		return fmt.Errorf("job is nil")
	}
	if receipt == nil {
		return fmt.Errorf("soul binding integration proof missing from receipt")
	}
	if receipt.Version != 1 {
		return fmt.Errorf("unsupported soul binding integration receipt version")
	}
	if strings.TrimSpace(receipt.Source) != managedInstanceKeyReceiptSourceDeployRunner {
		return fmt.Errorf("soul binding integration receipt source is invalid")
	}
	slug := strings.ToLower(strings.TrimSpace(binding.slug))
	if slug == "" || strings.ToLower(strings.TrimSpace(receipt.InstanceSlug)) != slug {
		return fmt.Errorf("soul binding integration receipt slug does not match %s", binding.kind)
	}
	if strings.TrimSpace(receipt.Stage) == "" {
		return fmt.Errorf("soul binding integration receipt stage is missing")
	}
	expectedStage := normalizeManagedLesserStage(s.cfg.Stage)
	if strings.TrimSpace(binding.stage) != "" {
		expectedStage = normalizeManagedLesserStage(binding.stage)
	}
	if got := strings.ToLower(strings.TrimSpace(receipt.Stage)); got != expectedStage {
		return fmt.Errorf("soul binding integration receipt stage does not match control plane stage")
	}
	if err := validateSoulBindingIntegrationReceiptSecretARN(binding, expectedStage, receipt.SecretARN); err != nil {
		return err
	}
	return validateSoulBindingIntegrationReceiptKeyID(receipt.KeyID)
}

func (s *Server) applyProvisionSoulBindingIntegrationReceipt(job *models.ProvisionJob, receipt *soulBindingIntegrationReceipt) (string, error) {
	if receipt == nil {
		return "", fmt.Errorf("soul binding integration proof missing from receipt")
	}
	if err := s.validateSoulBindingIntegrationReceiptForBinding(provisionManagedInstanceKeyReceiptBinding(job), receipt); err != nil {
		return "", err
	}
	return strings.TrimSpace(receipt.SecretARN), nil
}

func (s *Server) applyUpdateSoulBindingIntegrationReceiptJSON(job *models.UpdateJob, receiptJSON string) error {
	receipt, err := soulBindingIntegrationReceiptFromJSON(receiptJSON)
	if err != nil {
		return err
	}
	if receipt == nil {
		return nil
	}
	if err := s.validateSoulBindingIntegrationReceiptForBinding(updateManagedInstanceKeyReceiptBinding(job), receipt); err != nil {
		return err
	}
	if job != nil {
		job.SoulBindingIntegrationSecretARN = strings.TrimSpace(receipt.SecretARN)
	}
	return nil
}

func setSoulBindingIntegrationInstanceARN(ub core.UpdateBuilder, secretARN string) {
	secretARN = strings.TrimSpace(secretARN)
	if ub == nil || !strings.HasPrefix(secretARN, soulBindingIntegrationSecretARNPrefix) {
		return
	}
	ub.Set("SoulBindingIntegrationSecretARN", secretARN)
}

func (s *Server) resolveUpdateSoulBindingSecretRef(job *models.UpdateJob, inst *models.Instance) string {
	if s == nil || job == nil || inst == nil {
		return ""
	}
	targetStage := normalizeManagedLesserStage(s.cfg.Stage)
	ref := strings.TrimSpace(inst.SoulBindingIntegrationSecretARN)
	if ref == "" {
		ref = soulBindingIntegrationSecretName(targetStage, strings.TrimSpace(job.InstanceSlug))
	}
	binding := managedInstanceKeyReceiptBinding{
		kind:      "managed instance",
		slug:      strings.TrimSpace(job.InstanceSlug),
		accountID: strings.TrimSpace(inst.HostedAccountID),
		region:    strings.TrimSpace(inst.HostedRegion),
		stage:     targetStage,
	}
	if err := validateSoulBindingIntegrationSecretRef(binding, targetStage, ref); err != nil {
		return ""
	}
	return ref
}
