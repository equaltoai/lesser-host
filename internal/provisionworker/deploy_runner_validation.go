package provisionworker

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type provisionDeployRunnerInstanceInputs struct {
	lesserHostURL             string
	lesserHostAttestationsURL string
	instanceKeySecretArn      string
	translationEnabled        bool
	tipEnabled                bool
	tipChainID                int64
	tipContractAddress        string
	aiEnabled                 bool
	aiModerationEnabled       bool
	aiNsfwDetectionEnabled    bool
	aiSpamDetectionEnabled    bool
	aiPiiDetectionEnabled     bool
	aiContentDetectionEnabled bool
}

func (s *Server) resolveProvisionDeployRunnerInstanceInputs(ctx context.Context, job *models.ProvisionJob) (provisionDeployRunnerInstanceInputs, error) {
	if s == nil {
		return provisionDeployRunnerInstanceInputs{}, fmt.Errorf("server is nil")
	}
	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return provisionDeployRunnerInstanceInputs{}, err
	}
	if inst == nil {
		return provisionDeployRunnerInstanceInputs{}, fmt.Errorf("instance not found")
	}
	if validationErr := s.validateDeployRunnerJobForInstance(job, inst); validationErr != nil {
		return provisionDeployRunnerInstanceInputs{}, validationErr
	}

	inputs := provisionDeployRunnerInstanceInputs{
		lesserHostURL:             strings.TrimSpace(inst.LesserHostBaseURL),
		lesserHostAttestationsURL: strings.TrimSpace(inst.LesserHostAttestationsURL),
		instanceKeySecretArn:      strings.TrimSpace(inst.LesserHostInstanceKeySecretARN),
		translationEnabled:        effectiveTranslationEnabled(inst.TranslationEnabled),
		tipEnabled:                effectiveTipEnabled(inst.TipEnabled),
		tipChainID:                inst.TipChainID,
		tipContractAddress:        strings.TrimSpace(inst.TipContractAddress),
		aiEnabled:                 effectiveLesserAIEnabled(inst.LesserAIEnabled),
		aiModerationEnabled:       effectiveLesserAIModerationEnabled(inst.LesserAIModerationEnabled),
		aiNsfwDetectionEnabled:    effectiveLesserAINsfwDetectionEnabled(inst.LesserAINsfwDetectionEnabled),
		aiSpamDetectionEnabled:    effectiveLesserAISpamDetectionEnabled(inst.LesserAISpamDetectionEnabled),
		aiPiiDetectionEnabled:     effectiveLesserAIPiiDetectionEnabled(inst.LesserAIPiiDetectionEnabled),
		aiContentDetectionEnabled: effectiveLesserAIContentDetectionEnabled(inst.LesserAIContentDetectionEnabled),
	}
	if inputs.lesserHostURL == "" {
		inputs.lesserHostURL = strings.TrimSpace(s.publicBaseURL())
	}
	if inputs.lesserHostAttestationsURL == "" {
		inputs.lesserHostAttestationsURL = inputs.lesserHostURL
	}
	if inputs.lesserHostURL == "" {
		return provisionDeployRunnerInstanceInputs{}, fmt.Errorf("lesser host base url is missing")
	}
	if tipErr := validateManagedTipDeployConfig(inputs.tipEnabled, inputs.tipChainID, inputs.tipContractAddress); tipErr != nil {
		return provisionDeployRunnerInstanceInputs{}, tipErr
	}
	return inputs, nil
}

func appendProvisionDeployRunnerInstanceEnv(env []cbtypes.EnvironmentVariable, inputs provisionDeployRunnerInstanceInputs) []cbtypes.EnvironmentVariable {
	env = append(env,
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_URL"), Value: aws.String(inputs.lesserHostURL)},
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_ATTESTATIONS_URL"), Value: aws.String(inputs.lesserHostAttestationsURL)},
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_INSTANCE_KEY_ARN"), Value: aws.String(inputs.instanceKeySecretArn)},
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_INSTANCE_KEY_SECRET_ID"), Value: aws.String(inputs.instanceKeySecretArn)},
		cbtypes.EnvironmentVariable{Name: aws.String("TRANSLATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", inputs.translationEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("TIP_ENABLED"), Value: aws.String(fmt.Sprintf("%t", inputs.tipEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_ENABLED"), Value: aws.String(fmt.Sprintf("%t", inputs.aiEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_MODERATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", inputs.aiModerationEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_NSFW_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", inputs.aiNsfwDetectionEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_SPAM_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", inputs.aiSpamDetectionEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_PII_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", inputs.aiPiiDetectionEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_CONTENT_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", inputs.aiContentDetectionEnabled))},
	)
	if inputs.tipEnabled {
		env = append(env,
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CHAIN_ID"), Value: aws.String(fmt.Sprintf("%d", inputs.tipChainID))},
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CONTRACT_ADDRESS"), Value: aws.String(inputs.tipContractAddress)},
		)
	}
	return env
}

func (s *Server) validateDeployRunnerJobForInstance(job *models.ProvisionJob, inst *models.Instance) error {
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	if inst == nil {
		return fmt.Errorf("instance is nil")
	}
	if err := validateDeployRunnerSlug(job, inst); err != nil {
		return err
	}
	if err := validateDeployRunnerAccount(job, inst); err != nil {
		return err
	}
	if err := validateDeployRunnerRole(s, job); err != nil {
		return err
	}
	if err := validateDeployRunnerRegion(job, inst); err != nil {
		return err
	}
	if err := validateDeployRunnerBaseDomain(job, inst); err != nil {
		return err
	}
	if strings.TrimSpace(job.LesserVersion) == "" {
		return fmt.Errorf("lesser version is required")
	}
	return nil
}

func validateDeployRunnerSlug(job *models.ProvisionJob, inst *models.Instance) error {
	slug := strings.TrimSpace(job.InstanceSlug)
	if slug == "" || strings.TrimSpace(inst.Slug) == "" || !strings.EqualFold(slug, strings.TrimSpace(inst.Slug)) {
		return fmt.Errorf("deploy runner instance slug does not match job")
	}
	return nil
}

func validateDeployRunnerAccount(job *models.ProvisionJob, inst *models.Instance) error {
	accountID := strings.TrimSpace(job.AccountID)
	if err := validateManagedAWSAccountID(accountID, "target account id"); err != nil {
		return err
	}
	if hostedAccountID := strings.TrimSpace(inst.HostedAccountID); hostedAccountID != "" && hostedAccountID != accountID {
		return fmt.Errorf("deploy runner target account does not match instance metadata")
	}
	return nil
}

func validateDeployRunnerRole(s *Server, job *models.ProvisionJob) error {
	roleName := strings.TrimSpace(job.AccountRoleName)
	if roleName == "" {
		return fmt.Errorf("target role name is required")
	}
	if expectedRole := expectedManagedInstanceRoleName(s); expectedRole != "" && roleName != expectedRole {
		return fmt.Errorf("deploy runner target role does not match managed instance role")
	}
	return nil
}

func validateDeployRunnerRegion(job *models.ProvisionJob, inst *models.Instance) error {
	region := strings.TrimSpace(job.Region)
	if region == "" {
		return fmt.Errorf("target region is required")
	}
	if hostedRegion := strings.TrimSpace(inst.HostedRegion); hostedRegion != "" && hostedRegion != region {
		return fmt.Errorf("deploy runner target region does not match instance metadata")
	}
	return nil
}

func validateDeployRunnerBaseDomain(job *models.ProvisionJob, inst *models.Instance) error {
	baseDomain := strings.TrimSpace(job.BaseDomain)
	if baseDomain == "" {
		return fmt.Errorf("base domain is required")
	}
	if hostedBaseDomain := strings.TrimSpace(inst.HostedBaseDomain); hostedBaseDomain != "" && !strings.EqualFold(hostedBaseDomain, baseDomain) {
		return fmt.Errorf("deploy runner base domain does not match instance metadata")
	}
	return nil
}

func expectedManagedInstanceRoleName(s *Server) string {
	if s == nil {
		return defaultManagedInstanceRoleName
	}
	roleName := strings.TrimSpace(s.cfg.ManagedInstanceRoleName)
	if roleName == "" {
		return defaultManagedInstanceRoleName
	}
	return roleName
}

func validateManagedAWSAccountID(value string, label string) error {
	value = strings.TrimSpace(value)
	label = strings.TrimSpace(label)
	if label == "" {
		label = "aws account id"
	}
	if !awsAccountIDRE.MatchString(value) {
		return fmt.Errorf("%s must be a 12-digit AWS account id", label)
	}
	return nil
}

func validateManagedTipDeployConfig(enabled bool, chainID int64, contractAddress string) error {
	if !enabled {
		return nil
	}
	if chainID <= 0 {
		return fmt.Errorf("tip chain id is required when tips are enabled")
	}
	contractAddress = strings.TrimSpace(contractAddress)
	if !evmAddressRE.MatchString(contractAddress) {
		return fmt.Errorf("tip contract address must be a 20-byte EVM address when tips are enabled")
	}
	return nil
}
