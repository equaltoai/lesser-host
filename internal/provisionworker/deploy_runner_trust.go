package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

type iamAPI interface {
	GetRole(ctx context.Context, params *iam.GetRoleInput, optFns ...func(*iam.Options)) (*iam.GetRoleOutput, error)
	UpdateAssumeRolePolicy(ctx context.Context, params *iam.UpdateAssumeRolePolicyInput, optFns ...func(*iam.Options)) (*iam.UpdateAssumeRolePolicyOutput, error)
}

type iamClientFactory func(ctx context.Context, accountID string, roleName string, region string, slug string, jobID string) (iamAPI, error)

func (s *Server) ensureDeployRunnerAssumeRoleTrust(ctx context.Context, accountID string, roleName string, region string, slug string, jobID string) error {
	if s == nil {
		return fmt.Errorf("server not initialized")
	}
	runnerRoleARN := strings.TrimSpace(s.cfg.ManagedProvisionRunnerRoleARN)
	if runnerRoleARN == "" {
		return nil
	}
	if err := validateIAMRoleARN(runnerRoleARN); err != nil {
		return fmt.Errorf("invalid managed provision runner role arn: %w", err)
	}

	accountID = strings.TrimSpace(accountID)
	roleName = strings.TrimSpace(roleName)
	slug = strings.TrimSpace(slug)
	if accountID == "" || roleName == "" {
		return fmt.Errorf("account id and role name are required")
	}

	childIAM, err := s.childIAMClient(ctx, accountID, roleName, region, slug, jobID)
	if err != nil {
		return err
	}

	out, err := childIAM.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		return fmt.Errorf("get managed instance role trust policy: %w", err)
	}
	if out == nil || out.Role == nil || strings.TrimSpace(aws.ToString(out.Role.AssumeRolePolicyDocument)) == "" {
		return fmt.Errorf("managed instance role trust policy is empty")
	}

	// Derive a tenant-scoped external ID to prevent cross-tenant assume-role.
	// A compromised CodeBuild runner for tenant A cannot assume tenant B's role
	// because tenant B's trust policy requires an external ID the runner does not know.
	externalID := deployRunnerExternalID(slug)

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(aws.ToString(out.Role.AssumeRolePolicyDocument), runnerRoleARN, externalID)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	if _, err := childIAM.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyDocument: aws.String(updated),
	}); err != nil {
		return fmt.Errorf("update managed instance role trust policy: %w", err)
	}
	return nil
}

// deployRunnerExternalID returns a tenant-scoped external ID used for sts:ExternalId
// conditioning on the cross-account assume-role trust policy.
func deployRunnerExternalID(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "lesser-host/deploy/unknown"
	}
	return "lesser-host/deploy/" + slug
}

func (s *Server) childIAMClient(ctx context.Context, accountID string, roleName string, region string, slug string, jobID string) (iamAPI, error) {
	if s == nil {
		return nil, fmt.Errorf("server not initialized")
	}
	if s.iamFactory != nil {
		return s.iamFactory(ctx, accountID, roleName, region, slug, jobID)
	}

	assumed, _, err := s.assumeInstanceRole(ctx, accountID, roleName, slug, jobID)
	if err != nil {
		return nil, fmt.Errorf("assume instance role: %w", err)
	}
	if assumed == nil || assumed.Credentials == nil {
		return nil, fmt.Errorf("assume role returned empty credentials")
	}

	region = strings.TrimSpace(region)
	if region == "" {
		region = strings.TrimSpace(s.cfg.ManagedDefaultRegion)
	}
	if region == "" {
		region = defaultManagedAWSRegion
	}

	creds := credentials.NewStaticCredentialsProvider(
		aws.ToString(assumed.Credentials.AccessKeyId),
		aws.ToString(assumed.Credentials.SecretAccessKey),
		aws.ToString(assumed.Credentials.SessionToken),
	)
	return iam.New(iam.Options{
		Region:      region,
		Credentials: aws.NewCredentialsCache(creds),
	}), nil
}

func validateIAMRoleARN(value string) error {
	parsed, err := arn.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if parsed.Service != "iam" || parsed.AccountID == "" || !strings.HasPrefix(parsed.Resource, "role/") {
		return fmt.Errorf("expected iam role arn")
	}
	return nil
}

func ensureAssumeRolePolicyAllowsPrincipal(rawPolicy string, principalARN string, externalID string) (string, bool, error) {
	principalARN = strings.TrimSpace(principalARN)
	if principalARN == "" {
		return "", false, fmt.Errorf("principal arn is required")
	}
	externalID = strings.TrimSpace(externalID)

	policyJSON, err := decodeAssumeRolePolicyDocument(rawPolicy)
	if err != nil {
		return "", false, err
	}

	var doc map[string]any
	if unmarshalErr := json.Unmarshal([]byte(policyJSON), &doc); unmarshalErr != nil {
		return "", false, fmt.Errorf("parse managed instance role trust policy: %w", unmarshalErr)
	}
	if strings.TrimSpace(fmt.Sprint(doc["Version"])) == "" {
		doc["Version"] = "2012-10-17"
	}

	statements, err := normalizedPolicyStatements(doc["Statement"])
	if err != nil {
		return "", false, err
	}

	// Check if a statement already exists that both allows the principal AND
	// carries the expected external-ID condition.
	for _, statement := range statements {
		if assumeRoleStatementAllowsPrincipal(statement, principalARN) {
			if externalID == "" || assumeRoleStatementHasExternalID(statement, externalID) {
				return policyJSON, false, nil
			}
			// Statement allows the principal but is missing the external-ID
			// condition. Replace it with a conditioned statement.
			break
		}
	}

	// Remove any existing statements that allow the principal without the
	// required external-ID condition, then add a properly conditioned statement.
	filtered := make([]any, 0, len(statements)+1)
	for _, statement := range statements {
		if !assumeRoleStatementAllowsPrincipal(statement, principalARN) {
			filtered = append(filtered, statement)
			continue
		}
		if externalID != "" && assumeRoleStatementHasExternalID(statement, externalID) {
			filtered = append(filtered, statement)
			continue
		}
		// Drop this statement — it allows the principal but lacks the
		// required external-ID condition. We will replace it below.
	}

	newStatement := map[string]any{
		"Effect": "Allow",
		"Principal": map[string]any{
			"AWS": principalARN,
		},
		"Action": "sts:AssumeRole",
	}
	if externalID != "" {
		newStatement["Condition"] = map[string]any{
			"StringEquals": map[string]any{
				"sts:ExternalId": externalID,
			},
		}
	}
	statements = append(filtered, newStatement)
	doc["Statement"] = statements

	updated, err := json.Marshal(doc)
	if err != nil {
		return "", false, fmt.Errorf("marshal managed instance role trust policy: %w", err)
	}
	return string(updated), true, nil
}

// assumeRoleStatementHasExternalID checks whether a trust-policy statement
// carries a StringEquals condition on sts:ExternalId matching the supplied
// value.
func assumeRoleStatementHasExternalID(statement any, externalID string) bool {
	stmt, ok := statement.(map[string]any)
	if !ok {
		return false
	}
	condition, ok := stmt["Condition"].(map[string]any)
	if !ok {
		return false
	}
	stringEquals, ok := condition["StringEquals"].(map[string]any)
	if !ok {
		return false
	}
	got, ok := stringEquals["sts:ExternalId"].(string)
	if !ok {
		return false
	}
	return strings.TrimSpace(got) == strings.TrimSpace(externalID)
}

func decodeAssumeRolePolicyDocument(rawPolicy string) (string, error) {
	rawPolicy = strings.TrimSpace(rawPolicy)
	if rawPolicy == "" {
		return "", fmt.Errorf("managed instance role trust policy is empty")
	}
	if strings.HasPrefix(rawPolicy, "{") {
		return rawPolicy, nil
	}
	decoded, err := url.QueryUnescape(rawPolicy)
	if err == nil && strings.HasPrefix(strings.TrimSpace(decoded), "{") {
		return strings.TrimSpace(decoded), nil
	}
	decoded, err = url.PathUnescape(rawPolicy)
	if err == nil && strings.HasPrefix(strings.TrimSpace(decoded), "{") {
		return strings.TrimSpace(decoded), nil
	}
	return "", fmt.Errorf("managed instance role trust policy is not JSON")
}

func normalizedPolicyStatements(value any) ([]any, error) {
	switch v := value.(type) {
	case nil:
		return []any{}, nil
	case []any:
		return v, nil
	case map[string]any:
		return []any{v}, nil
	default:
		return nil, fmt.Errorf("managed instance role trust policy Statement has unexpected shape")
	}
}

func assumeRoleStatementAllowsPrincipal(statement any, principalARN string) bool {
	stmt, ok := statement.(map[string]any)
	if !ok {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(stmt["Effect"])), "Allow") {
		return false
	}
	if !policyValueContains(stmt["Action"], "sts:AssumeRole") && !policyValueContains(stmt["Action"], "sts:*") && !policyValueContains(stmt["Action"], "*") {
		return false
	}
	return principalAllowsARN(stmt["Principal"], principalARN)
}

func principalAllowsARN(value any, principalARN string) bool {
	if policyValueContains(value, "*") || policyValueContains(value, principalARN) {
		return true
	}
	principal, ok := value.(map[string]any)
	if !ok {
		return false
	}
	return policyValueContains(principal["AWS"], principalARN) || policyValueContains(principal["AWS"], "*")
}

func policyValueContains(value any, want string) bool {
	want = strings.TrimSpace(want)
	switch v := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(v), want)
	case []any:
		for _, item := range v {
			if policyValueContains(item, want) {
				return true
			}
		}
	case []string:
		for _, item := range v {
			if strings.EqualFold(strings.TrimSpace(item), want) {
				return true
			}
		}
	}
	return false
}
