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

const (
	deployRunnerModeLesser     = "lesser"
	deployRunnerModeLesserBody = "lesser-body"
	envBoolTrue                = "true"
)

var errDeployRunnerNotFound = errors.New("deploy runner not found")

var (
	operatorVisibleFailureWhitespaceRE = regexp.MustCompile(`\s+`)
	codebuildFailureReasonRE           = regexp.MustCompile(`(?i)\breason:\s*(.+)$`)
	codebuildExitStatusRE              = regexp.MustCompile(`(?i)\bexit status \d+\b`)
	awsAccountIDRE                     = regexp.MustCompile(`^[0-9]{12}$`)
	evmAddressRE                       = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
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

func normalizeDeployRunnerMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return deployRunnerModeLesser
	}
	return mode
}

func (s *Server) startDeployRunnerWithMode(ctx context.Context, job *models.ProvisionJob, mode string, receiptKey string) (string, error) {
	if s == nil || s.cb == nil {
		return "", fmt.Errorf("codebuild client not initialized")
	}
	if validationErr := s.validateDeployRunnerJob(job); validationErr != nil {
		return "", validationErr
	}
	projectName, err := s.provisionRunnerProjectName()
	if err != nil {
		return "", err
	}

	runnerInputs, err := s.resolveProvisionDeployRunnerInstanceInputs(ctx, job)
	if err != nil {
		return "", err
	}
	trustErr := s.ensureDeployRunnerAssumeRoleTrust(
		ctx,
		strings.TrimSpace(job.AccountID),
		strings.TrimSpace(job.AccountRoleName),
		strings.TrimSpace(job.Region),
		strings.TrimSpace(job.InstanceSlug),
		strings.TrimSpace(job.ID),
	)
	if trustErr != nil {
		return "", trustErr
	}

	bootstrapKey := s.bootstrapS3Key(job)
	stage := s.deployRunnerStage(job)
	env := s.buildDeployRunnerEnv(job, stage, receiptKey, bootstrapKey)
	mode = normalizeDeployRunnerMode(mode)
	env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("RUN_MODE"), Value: aws.String(mode)})
	if bodyEnabled, ok := provisionDeployRunnerBodyEnabledForMode(mode); ok {
		env = append(env, cbtypes.EnvironmentVariable{Name: aws.String("BODY_ENABLED"), Value: aws.String(bodyEnabled)})
	}
	env = appendProvisionDeployRunnerInstanceEnv(env, runnerInputs)

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

func provisionDeployRunnerBodyEnabledForMode(mode string) (string, bool) {
	switch normalizeDeployRunnerMode(mode) {
	case deployRunnerModeLesser:
		// Managed provisioning deploys Lesser first, then lesser-body, then
		// reruns Lesser in RUN_MODE=lesser-mcp to attach the host-owned MCP
		// route. Lesser's CLI defaults BODY_ENABLED to true, which makes the
		// first Lesser deploy try to resolve the body SSM export before the
		// body stack exists. Keep the first phase explicitly body-free.
		return "false", true
	case deployRunnerModeLesserMCP:
		return envBoolTrue, true
	default:
		return "", false
	}
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
