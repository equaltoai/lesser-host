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

func soulBindingIntegrationSecretName(controlPlaneStage, slug string) string {
	stage := managedInstanceKeySecretStage(controlPlaneStage)
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

func validateSoulBindingIntegrationReceiptSecretARN(binding managedInstanceKeyReceiptBinding, secretARN string) error {
	secretARN = strings.TrimSpace(secretARN)
	if secretARN == "" {
		return fmt.Errorf("soul binding integration receipt secret ARN is invalid")
	}
	parsed, err := arn.Parse(secretARN)
	if err != nil || parsed.Service != "secretsmanager" || strings.TrimSpace(parsed.Region) == "" || strings.TrimSpace(parsed.AccountID) == "" || !strings.HasPrefix(parsed.Resource, "secret:") {
		return fmt.Errorf("soul binding integration receipt secret ARN is invalid")
	}
	if want := strings.TrimSpace(binding.accountID); want != "" && parsed.AccountID != want {
		return fmt.Errorf("soul binding integration receipt secret ARN account does not match %s", binding.kind)
	}
	if want := strings.TrimSpace(binding.region); want != "" && parsed.Region != want {
		return fmt.Errorf("soul binding integration receipt secret ARN region does not match %s", binding.kind)
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
	if got, want := managedInstanceKeySecretStage(receipt.Stage), expectedManagedInstanceKeyReceiptStage(s.cfg.Stage); got != want {
		return fmt.Errorf("soul binding integration receipt stage does not match control plane stage")
	}
	if err := validateSoulBindingIntegrationReceiptSecretARN(binding, receipt.SecretARN); err != nil {
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
	ref := strings.TrimSpace(inst.SoulBindingIntegrationSecretARN)
	if ref == "" {
		ref = soulBindingIntegrationSecretName(s.cfg.Stage, strings.TrimSpace(job.InstanceSlug))
	}
	return ref
}
