package provisionworker

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func walletAddressFromUsername(username string) string {
	username = strings.ToLower(strings.TrimSpace(username))
	if !strings.HasPrefix(username, "wallet-") {
		return ""
	}
	hexPart := strings.TrimSpace(strings.TrimPrefix(username, "wallet-"))
	if hexPart == "" {
		return ""
	}
	return "0x" + hexPart
}

type updateDeployRunnerInputs struct {
	accountID                 string
	roleName                  string
	region                    string
	baseDomain                string
	lesserVersion             string
	instanceKeySecretArn      string
	soulBindingSecretArn      string
	adminWallet               string
	stage                     string
	receiptKey                string
	bootstrapKey              string
	lesserHostURL             string
	lesserHostAttestationsURL string
}

func (s *Server) resolveUpdateDeployRunnerInputs(job *models.UpdateJob, inst *models.Instance) (updateDeployRunnerInputs, error) {
	if s == nil {
		return updateDeployRunnerInputs{}, fmt.Errorf("server is nil")
	}
	if job == nil {
		return updateDeployRunnerInputs{}, fmt.Errorf("job is nil")
	}
	if inst == nil {
		return updateDeployRunnerInputs{}, fmt.Errorf("instance is nil")
	}

	inputs := updateDeployRunnerInputsFromJob(job)
	if err := validateUpdateDeployRunnerRequiredInputs(inputs); err != nil {
		return updateDeployRunnerInputs{}, err
	}
	if err := validateUpdateDeployRunnerTenantBoundary(job, inst, inputs); err != nil {
		return updateDeployRunnerInputs{}, err
	}
	if err := validateUpdateDeployRunnerRole(s, inputs); err != nil {
		return updateDeployRunnerInputs{}, err
	}
	return s.populateUpdateDeployRunnerDerivedInputs(job, inst, inputs)
}

func updateDeployRunnerInputsFromJob(job *models.UpdateJob) updateDeployRunnerInputs {
	return updateDeployRunnerInputs{
		accountID:            strings.TrimSpace(job.AccountID),
		roleName:             strings.TrimSpace(job.AccountRoleName),
		region:               strings.TrimSpace(job.Region),
		baseDomain:           strings.TrimSpace(job.BaseDomain),
		lesserVersion:        strings.TrimSpace(job.LesserVersion),
		instanceKeySecretArn: strings.TrimSpace(job.LesserHostInstanceKeySecretARN),
		soulBindingSecretArn: strings.TrimSpace(job.SoulBindingIntegrationSecretARN),
	}
}

func validateUpdateDeployRunnerRequiredInputs(inputs updateDeployRunnerInputs) error {
	if inputs.accountID == "" || inputs.roleName == "" || inputs.region == "" || inputs.baseDomain == "" || inputs.lesserVersion == "" {
		return fmt.Errorf("missing required update job deploy inputs")
	}
	if inputs.instanceKeySecretArn == "" {
		return fmt.Errorf("instance key secret arn is missing")
	}
	return nil
}

func validateUpdateDeployRunnerRole(s *Server, inputs updateDeployRunnerInputs) error {
	if expectedRole := expectedManagedInstanceRoleName(s); expectedRole != "" && inputs.roleName != expectedRole {
		return fmt.Errorf("update deploy runner target role does not match managed instance role")
	}
	return nil
}

func (s *Server) populateUpdateDeployRunnerDerivedInputs(job *models.UpdateJob, inst *models.Instance, inputs updateDeployRunnerInputs) (updateDeployRunnerInputs, error) {
	inputs.adminWallet = walletAddressFromUsername(strings.TrimSpace(inst.Owner))
	if inputs.adminWallet == "" {
		return updateDeployRunnerInputs{}, fmt.Errorf("instance owner is not a wallet username")
	}
	inputs.stage = normalizeManagedLesserStage(strings.TrimSpace(s.cfg.Stage))

	// Update jobs created before the soul-binding automation carry no secret reference;
	// use the one target-stage canonical name so the runner can ensure the secret deterministically.
	if inputs.soulBindingSecretArn == "" {
		inputs.soulBindingSecretArn = s.resolveUpdateSoulBindingSecretRef(job, inst)
	}
	binding := updateManagedInstanceKeyReceiptBinding(job)
	binding.stage = inputs.stage
	if err := validateSoulBindingIntegrationSecretRef(binding, inputs.stage, inputs.soulBindingSecretArn); err != nil {
		return updateDeployRunnerInputs{}, err
	}
	inputs.receiptKey = s.updateReceiptS3Key(job)
	inputs.bootstrapKey = s.updateBootstrapS3Key(strings.TrimSpace(job.InstanceSlug))
	inputs.lesserHostURL = strings.TrimSpace(job.LesserHostBaseURL)
	if inputs.lesserHostURL == "" {
		inputs.lesserHostURL = strings.TrimSpace(s.publicBaseURL())
	}
	inputs.lesserHostAttestationsURL = strings.TrimSpace(job.LesserHostAttestationsURL)
	if inputs.lesserHostAttestationsURL == "" {
		inputs.lesserHostAttestationsURL = inputs.lesserHostURL
	}
	if inputs.lesserHostURL == "" {
		return updateDeployRunnerInputs{}, fmt.Errorf("lesser host base url is missing")
	}

	return inputs, nil
}

func validateUpdateDeployRunnerTenantBoundary(job *models.UpdateJob, inst *models.Instance, inputs updateDeployRunnerInputs) error {
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	if inst == nil {
		return fmt.Errorf("instance is nil")
	}
	slug := strings.TrimSpace(job.InstanceSlug)
	if slug == "" || strings.TrimSpace(inst.Slug) == "" || !strings.EqualFold(slug, strings.TrimSpace(inst.Slug)) {
		return fmt.Errorf("update deploy runner instance slug does not match job")
	}
	if err := validateManagedAWSAccountID(inputs.accountID, "target account id"); err != nil {
		return err
	}
	if hostedAccountID := strings.TrimSpace(inst.HostedAccountID); hostedAccountID == "" || hostedAccountID != inputs.accountID {
		return fmt.Errorf("update deploy runner target account does not match instance metadata")
	}
	if roleName := strings.TrimSpace(inputs.roleName); roleName == "" {
		return fmt.Errorf("target role name is required")
	}
	if hostedRegion := strings.TrimSpace(inst.HostedRegion); hostedRegion == "" || hostedRegion != inputs.region {
		return fmt.Errorf("update deploy runner target region does not match instance metadata")
	}
	if hostedBaseDomain := strings.TrimSpace(inst.HostedBaseDomain); hostedBaseDomain == "" || !strings.EqualFold(hostedBaseDomain, inputs.baseDomain) {
		return fmt.Errorf("update deploy runner base domain does not match instance metadata")
	}
	return nil
}

func (s *Server) buildUpdateDeployRunnerEnv(job *models.UpdateJob, inputs updateDeployRunnerInputs) []cbtypes.EnvironmentVariable {
	if s == nil || job == nil {
		return nil
	}

	tipEnabled := job.TipEnabled
	lesserBodyVersion := strings.TrimSpace(job.LesserBodyVersion)
	env := []cbtypes.EnvironmentVariable{
		{Name: aws.String("JOB_ID"), Value: aws.String(strings.TrimSpace(job.ID))},
		{Name: aws.String("APP_SLUG"), Value: aws.String(strings.TrimSpace(job.InstanceSlug))},
		{Name: aws.String("STAGE"), Value: aws.String(inputs.stage)},
		{Name: aws.String("ADMIN_USERNAME"), Value: aws.String(strings.TrimSpace(job.InstanceSlug))},
		{Name: aws.String("ADMIN_WALLET_ADDRESS"), Value: aws.String(inputs.adminWallet)},
		{Name: aws.String("BASE_DOMAIN"), Value: aws.String(inputs.baseDomain)},
		{Name: aws.String("TARGET_ACCOUNT_ID"), Value: aws.String(inputs.accountID)},
		{Name: aws.String("TARGET_ROLE_NAME"), Value: aws.String(inputs.roleName)},
		{Name: aws.String("TARGET_REGION"), Value: aws.String(inputs.region)},
		{Name: aws.String("DEPLOY_EXTERNAL_ID"), Value: aws.String(deployRunnerExternalID(strings.TrimSpace(job.InstanceSlug)))},
		{Name: aws.String("LESSER_VERSION"), Value: aws.String(inputs.lesserVersion)},
		{Name: aws.String("ARTIFACT_BUCKET"), Value: aws.String(strings.TrimSpace(s.cfg.ArtifactBucketName))},
		{Name: aws.String("RECEIPT_S3_KEY"), Value: aws.String(inputs.receiptKey)},
		{Name: aws.String("BOOTSTRAP_S3_KEY"), Value: aws.String(inputs.bootstrapKey)},
		{Name: aws.String("BODY_FAILURE_S3_KEY"), Value: aws.String(s.updateBodyFailureS3Key(job))},
		{Name: aws.String("BODY_TEMPLATE_CERT_S3_KEY"), Value: aws.String(s.updateBodyTemplateCertificationS3Key(job))},
		{Name: aws.String("GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubOwner))},
		{Name: aws.String("GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserGitHubRepo))},
		{Name: aws.String("LESSER_BODY_GITHUB_OWNER"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserBodyGitHubOwner))},
		{Name: aws.String("LESSER_BODY_GITHUB_REPO"), Value: aws.String(strings.TrimSpace(s.cfg.ManagedLesserBodyGitHubRepo))},

		{Name: aws.String("LESSER_HOST_URL"), Value: aws.String(inputs.lesserHostURL)},
		{Name: aws.String("LESSER_HOST_ATTESTATIONS_URL"), Value: aws.String(inputs.lesserHostAttestationsURL)},
		{Name: aws.String("LESSER_HOST_INSTANCE_KEY_ARN"), Value: aws.String(inputs.instanceKeySecretArn)},
		{Name: aws.String("LESSER_HOST_INSTANCE_KEY_SECRET_ID"), Value: aws.String(inputs.instanceKeySecretArn)},
		{Name: aws.String("LESSER_HOST_INSTANCE_KEY_ROTATE"), Value: aws.String(fmt.Sprintf("%t", shouldRotateUpdateInstanceKey(job)))},
		{Name: aws.String("SOUL_BINDING_INTEGRATION_KEY_ARN"), Value: aws.String(inputs.soulBindingSecretArn)},
		{Name: aws.String("TRANSLATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.TranslationEnabled))},
		{Name: aws.String("TIP_ENABLED"), Value: aws.String(fmt.Sprintf("%t", tipEnabled))},
		{Name: aws.String("AI_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AIEnabled))},
		{Name: aws.String("AI_MODERATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AIModerationEnabled))},
		{Name: aws.String("AI_NSFW_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AINsfwDetectionEnabled))},
		{Name: aws.String("AI_SPAM_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AISpamDetectionEnabled))},
		{Name: aws.String("AI_PII_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AIPiiDetectionEnabled))},
		{Name: aws.String("AI_CONTENT_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", job.AIContentDetectionEnabled))},
	}
	if lesserBodyVersion != "" {
		env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("LESSER_BODY_VERSION"), Value: aws.String(lesserBodyVersion)})
	}
	if tipEnabled {
		env = append(env,
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CHAIN_ID"), Value: aws.String(fmt.Sprintf("%d", job.TipChainID))},
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CONTRACT_ADDRESS"), Value: aws.String(strings.TrimSpace(job.TipContractAddress))},
		)
	}

	return env
}

func (s *Server) startUpdateDeployRunnerWithMode(ctx context.Context, job *models.UpdateJob, inst *models.Instance, mode string, receiptKey string) (string, error) {
	if s == nil || s.cb == nil {
		return "", fmt.Errorf("codebuild client not initialized")
	}
	if job == nil {
		return "", fmt.Errorf("job is nil")
	}
	if inst == nil {
		return "", fmt.Errorf("instance is nil")
	}

	projectName, err := s.provisionRunnerProjectName()
	if err != nil {
		return "", err
	}

	inputs, err := s.resolveUpdateDeployRunnerInputs(job, inst)
	if err != nil {
		return "", err
	}
	if tipErr := validateManagedTipDeployConfig(job.TipEnabled, job.TipChainID, job.TipContractAddress); tipErr != nil {
		return "", tipErr
	}
	if strings.TrimSpace(receiptKey) != "" {
		inputs.receiptKey = strings.TrimSpace(receiptKey)
	}
	env := s.buildUpdateDeployRunnerEnv(job, inputs)

	mode = normalizeDeployRunnerMode(mode)
	if phaseErr := validateDeployRunnerLesserBodyPhaseVersion(mode, job.LesserBodyVersion); phaseErr != nil {
		return "", phaseErr
	}
	env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("RUN_MODE"), Value: aws.String(mode)})
	if bodyEnabled, ok := updateDeployRunnerBodyEnabledForMode(mode, inst); ok {
		env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("BODY_ENABLED"), Value: aws.String(bodyEnabled)})
	}
	env = appendDeployRunnerInstancePlaneEnv(env, mode)
	if mode == deployRunnerModeLesserBody && job.BodyTemplateCertify {
		env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("BODY_TEMPLATE_CERTIFY"), Value: aws.String(envBoolTrue)})
	}
	trustErr := s.ensureDeployRunnerAssumeRoleTrust(
		ctx,
		inputs.accountID,
		inputs.roleName,
		inputs.region,
		strings.TrimSpace(job.InstanceSlug),
		strings.TrimSpace(job.ID),
	)
	if trustErr != nil {
		return "", fmt.Errorf("deploy runner trust bootstrap failed: %s", compactErr(trustErr))
	}

	idempotencyToken := codebuildIdempotencyToken(
		projectName,
		inputs.stage,
		strings.TrimSpace(job.InstanceSlug),
		strings.TrimSpace(job.ID),
		mode,
		strings.TrimSpace(inputs.receiptKey),
	)
	startIn := &codebuild.StartBuildInput{
		ProjectName:                  aws.String(projectName),
		EnvironmentVariablesOverride: env,
	}
	if idempotencyToken != "" {
		startIn.IdempotencyToken = aws.String(idempotencyToken)
	}

	out, err := s.cb.StartBuild(ctx, startIn)
	if err != nil {
		return "", err
	}
	return codebuildBuildID(out)
}

func (s *Server) startUpdateDeployRunner(ctx context.Context, job *models.UpdateJob, inst *models.Instance) (string, error) {
	return s.startUpdateDeployRunnerWithMode(ctx, job, inst, deployRunnerModeLesser, "")
}

func updateDeployRunnerBodyEnabledForMode(mode string, inst *models.Instance) (string, bool) {
	switch normalizeDeployRunnerMode(mode) {
	case deployRunnerModeLesser:
		enabled := false
		if inst != nil && effectiveBodyEnabled(inst.BodyEnabled) && strings.TrimSpace(inst.LesserBodyVersion) != "" && !inst.McpWiredAt.IsZero() {
			enabled = true
		}
		if enabled {
			return envBoolTrue, true
		}
		return envBoolFalse, true
	case deployRunnerModeLesserMCP:
		return envBoolTrue, true
	default:
		return "", false
	}
}
