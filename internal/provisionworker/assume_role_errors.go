package provisionworker

import (
	"errors"
	"strings"

	"github.com/aws/smithy-go"
)

var (
	errAssumeRoleNotReady     = errors.New("assume role not ready")
	errAssumeRoleAccessDenied = errors.New("assume role access denied")
)

type assumeRoleFailureKind string

const (
	assumeRoleFailureReadiness    assumeRoleFailureKind = "readiness"
	assumeRoleFailureAccessDenied assumeRoleFailureKind = "access_denied"

	assumeRoleRemediationReadiness     = "role_propagation_or_account_readiness"
	assumeRoleRemediationAccessDenied  = "target_role_trust_or_org_scp"
	assumeRoleUnknownPrincipalARN      = "unknown"
	assumeRoleDiagnosticMessageMaxLen  = 500
	assumeRoleDiagnosticTokenMaxLength = 220
)

type assumeRoleError struct {
	kind               assumeRoleFailureKind
	awsCode            string
	awsMessage         string
	callerPrincipalARN string
	targetRoleARN      string
	remediationClass   string
	cause              error
}

func (e *assumeRoleError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{"sts AssumeRole " + string(e.kind)}
	if e.awsCode != "" {
		parts = append(parts, "aws_code="+e.awsCode)
	}
	if e.awsMessage != "" {
		parts = append(parts, "aws_message="+e.awsMessage)
	}
	if e.kind == assumeRoleFailureAccessDenied {
		caller := strings.TrimSpace(e.callerPrincipalARN)
		if caller == "" {
			caller = assumeRoleUnknownPrincipalARN
		}
		parts = append(parts, "caller_principal_arn="+caller)
	}
	if e.targetRoleARN != "" {
		parts = append(parts, "target_role_arn="+e.targetRoleARN)
	}
	if e.remediationClass != "" {
		parts = append(parts, "remediation_class="+e.remediationClass)
	}
	return strings.Join(parts, "; ")
}

func (e *assumeRoleError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *assumeRoleError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case errAssumeRoleNotReady:
		return e.kind == assumeRoleFailureReadiness
	case errAssumeRoleAccessDenied:
		return e.kind == assumeRoleFailureAccessDenied
	default:
		return false
	}
}

func isRetryableAssumeRoleErr(err error) bool {
	kind, _ := classifyAssumeRoleFailure(err)
	return kind == assumeRoleFailureReadiness || kind == assumeRoleFailureAccessDenied
}

func isBoundedAssumeRoleReadinessErr(err error) bool {
	return errors.Is(err, errAssumeRoleNotReady) || errors.Is(err, errAssumeRoleAccessDenied)
}

func newAssumeRoleError(err error, targetRoleARN string) error {
	kind, remediation := classifyAssumeRoleFailure(err)
	if kind == "" {
		return err
	}
	message := assumeRoleAWSMessage(err)
	return &assumeRoleError{
		kind:               kind,
		awsCode:            assumeRoleAWSCode(err),
		awsMessage:         message,
		callerPrincipalARN: extractAssumeRoleCallerPrincipalARN(message),
		targetRoleARN:      sanitizeAssumeRoleDiagnosticToken(targetRoleARN, assumeRoleDiagnosticTokenMaxLength),
		remediationClass:   remediation,
		cause:              err,
	}
}

func classifyAssumeRoleFailure(err error) (assumeRoleFailureKind, string) {
	if err == nil {
		return "", ""
	}
	code := strings.ToLower(strings.TrimSpace(assumeRoleAWSCode(err)))
	msg := strings.ToLower(assumeRoleAWSMessage(err))
	if code == "nosuchentity" ||
		code == "invalidclienttokenid" ||
		strings.Contains(msg, "nosuchentity") ||
		strings.Contains(msg, "no such entity") ||
		strings.Contains(msg, "could not be found") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "invalidclienttokenid") {
		return assumeRoleFailureReadiness, assumeRoleRemediationReadiness
	}
	if code == "accessdenied" ||
		code == "accessdeniedexception" ||
		strings.Contains(msg, "accessdenied") ||
		strings.Contains(msg, "access denied") ||
		strings.Contains(msg, "is not authorized") {
		return assumeRoleFailureAccessDenied, assumeRoleRemediationAccessDenied
	}
	return "", ""
}

func assumeRoleAWSCode(err error) string {
	if err == nil {
		return ""
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return sanitizeAssumeRoleDiagnosticToken(apiErr.ErrorCode(), assumeRoleDiagnosticTokenMaxLength)
	}
	msg := err.Error()
	for _, candidate := range []string{"AccessDeniedException", "AccessDenied", "NoSuchEntity", "InvalidClientTokenId"} {
		if strings.Contains(msg, candidate) {
			return candidate
		}
	}
	return ""
}

func assumeRoleAWSMessage(err error) string {
	if err == nil {
		return ""
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return sanitizeAssumeRoleDiagnosticToken(apiErr.ErrorMessage(), assumeRoleDiagnosticMessageMaxLen)
	}
	return sanitizeAssumeRoleDiagnosticToken(err.Error(), assumeRoleDiagnosticMessageMaxLen)
}

func sanitizeAssumeRoleDiagnosticToken(value string, maxLen int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	if maxLen > 0 && len(value) > maxLen {
		value = value[:maxLen] + "…"
	}
	return value
}

func extractAssumeRoleCallerPrincipalARN(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	lower := strings.ToLower(message)
	if idx := strings.Index(lower, "user: arn:aws:"); idx >= 0 {
		return extractFirstAWSARN(message[idx+len("user: "):])
	}
	if idx := strings.Index(lower, "principal: arn:aws:"); idx >= 0 {
		return extractFirstAWSARN(message[idx+len("principal: "):])
	}
	if idx := strings.Index(lower, "caller_principal_arn=arn:aws:"); idx >= 0 {
		return extractFirstAWSARN(message[idx+len("caller_principal_arn="):])
	}
	return ""
}

func extractFirstAWSARN(value string) string {
	idx := strings.Index(value, "arn:aws:")
	if idx < 0 {
		return ""
	}
	value = value[idx:]
	end := len(value)
	for i, r := range value {
		if r <= ' ' || strings.ContainsRune(",;\"'()[]{}", r) {
			end = i
			break
		}
	}
	return strings.TrimRight(value[:end], ".")
}
