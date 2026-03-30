package provisionworker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const deployRunnerModeLesser = "lesser"

var errDeployRunnerNotFound = errors.New("deploy runner not found")

var (
	operatorVisibleFailureWhitespaceRE = regexp.MustCompile(`\s+`)
	codebuildFailureReasonRE           = regexp.MustCompile(`(?i)\breason:\s*(.+)$`)
	codebuildExitStatusRE              = regexp.MustCompile(`(?i)\bexit status \d+\b`)
	operatorVisibleFailureReplacer     = strings.NewReplacer(
		"--", "- -",
		"/*", "/ *",
		"*/", "* /",
		"<script", "< script",
		"</script", "< /script",
		"eval(", "eval (",
		"expression(", "expression (",
		"import(", "import (",
		"require(", "require (",
		"javascript:", "javascript :",
		"vbscript:", "vbscript :",
		"onload=", "onload =",
		"onerror=", "onerror =",
		"onclick=", "onclick =",
	)
)

func codebuildBuildID(out *codebuild.StartBuildOutput) (string, error) {
	if out == nil || out.Build == nil {
		return "", fmt.Errorf("codebuild StartBuild returned empty build")
	}
	if out.Build.Id != nil && strings.TrimSpace(*out.Build.Id) != "" {
		return strings.TrimSpace(*out.Build.Id), nil
	}
	if out.Build.Arn != nil && strings.TrimSpace(*out.Build.Arn) != "" {
		return strings.TrimSpace(*out.Build.Arn), nil
	}
	return "", fmt.Errorf("codebuild StartBuild returned empty build id")
}

func (s *Server) startDeployRunner(ctx context.Context, job *models.ProvisionJob) (string, error) {
	return s.startDeployRunnerWithMode(ctx, job, deployRunnerModeLesser, s.receiptS3Key(job))
}

func (s *Server) startDeployRunnerWithMode(ctx context.Context, job *models.ProvisionJob, mode string, receiptKey string) (string, error) {
	if s == nil || s.cb == nil {
		return "", fmt.Errorf("codebuild client not initialized")
	}
	if err := s.validateDeployRunnerJob(job); err != nil {
		return "", err
	}
	projectName, err := s.provisionRunnerProjectName()
	if err != nil {
		return "", err
	}

	inst, err := s.loadInstance(ctx, strings.TrimSpace(job.InstanceSlug))
	if err != nil {
		return "", err
	}
	if inst == nil {
		return "", fmt.Errorf("instance not found")
	}

	lesserHostURL := strings.TrimSpace(inst.LesserHostBaseURL)
	if lesserHostURL == "" {
		lesserHostURL = strings.TrimSpace(s.publicBaseURL())
	}
	lesserHostAttestationsURL := strings.TrimSpace(inst.LesserHostAttestationsURL)
	if lesserHostAttestationsURL == "" {
		lesserHostAttestationsURL = lesserHostURL
	}
	instanceKeySecretArn := strings.TrimSpace(inst.LesserHostInstanceKeySecretARN)
	if instanceKeySecretArn == "" {
		return "", fmt.Errorf("instance key secret arn is missing")
	}
	if lesserHostURL == "" {
		return "", fmt.Errorf("lesser host base url is missing")
	}

	bootstrapKey := s.bootstrapS3Key(job)
	stage := s.deployRunnerStage(job)
	env := s.buildDeployRunnerEnv(job, stage, receiptKey, bootstrapKey)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = deployRunnerModeLesser
	}
	env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("RUN_MODE"), Value: aws.String(mode)})
	tipEnabled := effectiveTipEnabled(inst.TipEnabled)
	env = append(env,
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_URL"), Value: aws.String(lesserHostURL)},
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_ATTESTATIONS_URL"), Value: aws.String(lesserHostAttestationsURL)},
		cbtypes.EnvironmentVariable{Name: aws.String("LESSER_HOST_INSTANCE_KEY_ARN"), Value: aws.String(instanceKeySecretArn)},
		cbtypes.EnvironmentVariable{Name: aws.String("TRANSLATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveTranslationEnabled(inst.TranslationEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("TIP_ENABLED"), Value: aws.String(fmt.Sprintf("%t", tipEnabled))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAIEnabled(inst.LesserAIEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_MODERATION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAIModerationEnabled(inst.LesserAIModerationEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_NSFW_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAINsfwDetectionEnabled(inst.LesserAINsfwDetectionEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_SPAM_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAISpamDetectionEnabled(inst.LesserAISpamDetectionEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_PII_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAIPiiDetectionEnabled(inst.LesserAIPiiDetectionEnabled)))},
		cbtypes.EnvironmentVariable{Name: aws.String("AI_CONTENT_DETECTION_ENABLED"), Value: aws.String(fmt.Sprintf("%t", effectiveLesserAIContentDetectionEnabled(inst.LesserAIContentDetectionEnabled)))},
	)
	if tipEnabled {
		env = append(env,
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CHAIN_ID"), Value: aws.String(fmt.Sprintf("%d", inst.TipChainID))},
			cbtypes.EnvironmentVariable{Name: aws.String("TIP_CONTRACT_ADDRESS"), Value: aws.String(strings.TrimSpace(inst.TipContractAddress))},
		)
	}

	idempotencyToken := codebuildIdempotencyToken(
		projectName,
		stage,
		strings.TrimSpace(job.InstanceSlug),
		strings.TrimSpace(job.ID),
		mode,
		strings.TrimSpace(receiptKey),
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

func (s *Server) getDeployRunnerStatus(ctx context.Context, runID string) (string, string, error) {
	info, err := s.getDeployRunnerInfo(ctx, runID)
	if err != nil {
		return "", "", err
	}
	return info.Status, info.DeepLink, nil
}

func (s *Server) getDeployRunnerInfo(ctx context.Context, runID string) (deployRunnerInfo, error) {
	if s == nil || s.cb == nil {
		return deployRunnerInfo{}, fmt.Errorf("codebuild client not initialized")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return deployRunnerInfo{}, fmt.Errorf("runID is required")
	}

	out, err := s.cb.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{
		Ids: []string{runID},
	})
	if err != nil {
		return deployRunnerInfo{}, err
	}
	if out == nil || len(out.Builds) == 0 {
		return deployRunnerInfo{}, errDeployRunnerNotFound
	}
	build := out.Builds[0]
	return deployRunnerInfo{
		Status:        normalizeCodebuildStatus(build.BuildStatus),
		DeepLink:      codebuildBuildDeepLink(build),
		CurrentPhase:  strings.TrimSpace(aws.ToString(build.CurrentPhase)),
		FailureDetail: codebuildFailureDetail(build),
	}, nil
}

func codebuildBuildDeepLink(build cbtypes.Build) string {
	if build.Logs == nil || build.Logs.DeepLink == nil {
		return ""
	}
	return strings.TrimSpace(*build.Logs.DeepLink)
}

func normalizeCodebuildStatus(st cbtypes.StatusType) string {
	switch st {
	case cbtypes.StatusTypeInProgress:
		return codebuildStatusInProgress
	case cbtypes.StatusTypeSucceeded:
		return codebuildStatusSucceeded
	case cbtypes.StatusTypeFailed:
		return codebuildStatusFailed
	case cbtypes.StatusTypeFault:
		return codebuildStatusFault
	case cbtypes.StatusTypeStopped:
		return codebuildStatusStopped
	case cbtypes.StatusTypeTimedOut:
		return codebuildStatusTimedOut
	default:
		status := strings.TrimSpace(string(st))
		if status == "" {
			return codebuildStatusUnknown
		}
		return status
	}
}

func codebuildFailureDetail(build cbtypes.Build) string {
	for _, phase := range build.Phases {
		if strings.TrimSpace(string(phase.PhaseStatus)) != string(cbtypes.StatusTypeFailed) {
			continue
		}
		if phase.Contexts == nil {
			continue
		}
		for _, ctx := range phase.Contexts {
			msg := strings.TrimSpace(aws.ToString(ctx.Message))
			if msg != "" {
				return msg
			}
		}
	}
	return ""
}

func ensureTrailingDot(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

func normalizeHostedZoneID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "/hostedzone/")
	id = strings.TrimPrefix(id, "hostedzone/")
	return id
}

func dedupeSortedStrings(in []string) []string {
	out := make([]string, 0, len(in))
	var last string
	for i, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if i == 0 || s != last {
			out = append(out, s)
			last = s
		}
	}
	return out
}

func compactErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "unknown error"
	}
	const maxLen = 350
	if len(msg) > maxLen {
		msg = msg[:maxLen] + "…"
	}
	return msg
}

func sanitizeOperatorVisibleFailureDetail(raw string) string {
	raw = sanitizeOperatorVisibleFailureSnippet(raw, 220)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "error while executing command:") || strings.Contains(lower, "command_execution_error") {
		if reason := extractCodebuildCommandFailureReason(raw); reason != "" {
			return "command execution failed (" + reason + ")"
		}
		return "command execution failed"
	}
	return raw
}

func extractCodebuildCommandFailureReason(raw string) string {
	if match := codebuildFailureReasonRE.FindStringSubmatch(raw); len(match) == 2 {
		return sanitizeOperatorVisibleFailureSnippet(match[1], 120)
	}
	if match := codebuildExitStatusRE.FindString(raw); strings.TrimSpace(match) != "" {
		return strings.TrimSpace(match)
	}
	return ""
}

func sanitizeOperatorVisibleFailureSnippet(raw string, maxLen int) string {
	raw = normalizeOperatorVisibleFailureWhitespace(raw)
	if raw == "" {
		return ""
	}
	raw = operatorVisibleFailureReplacer.Replace(raw)
	raw = strings.Trim(raw, " .;:")
	if maxLen > 0 && len(raw) > maxLen {
		raw = raw[:maxLen] + "…"
	}
	return raw
}

func normalizeOperatorVisibleFailureWhitespace(raw string) string {
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if unicode.IsControl(r) {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return operatorVisibleFailureWhitespaceRE.ReplaceAllString(strings.TrimSpace(b.String()), " ")
}

func jitteredBackoff(attempt int64, minDelay time.Duration, maxDelay time.Duration) time.Duration {
	if attempt <= 0 {
		return minDelay
	}
	delay := minDelay
	for i := int64(1); i < attempt; i++ {
		delay *= 2
		if delay >= maxDelay {
			delay = maxDelay
			break
		}
	}
	// Cheap jitter: add up to 10% based on attempt parity.
	jitter := time.Duration(int64(delay) / 10)
	if attempt%2 == 0 {
		delay += jitter
	}
	if delay < minDelay {
		return minDelay
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func sqsQueueNameFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
