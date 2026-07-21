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

const iamServiceName = "iam"

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
	rawPolicy := managedInstanceRoleTrustPolicy(out)
	if rawPolicy == "" {
		return fmt.Errorf("managed instance role trust policy is empty")
	}
	managementPrincipalARN, err := s.resolveManagementRootPrincipalARN(runnerRoleARN)
	if err != nil {
		return err
	}

	// Derive a tenant-scoped external ID to prevent cross-tenant assume-role.
	// A compromised CodeBuild runner for tenant A cannot assume tenant B's role
	// because tenant B's trust policy requires an external ID the runner does not know.
	externalID := deployRunnerExternalID(slug)

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(rawPolicy, runnerRoleARN, externalID, managementPrincipalARN)
	if err != nil {
		return err
	}
	verifiedPolicy := rawPolicy
	if changed {
		verifiedPolicy, err = updateAndReadBackAssumeRoleTrustPolicy(ctx, childIAM, roleName, updated)
		if err != nil {
			return err
		}
	}

	if err := verifyDeployRunnerAssumeRoleTrustPolicy(verifiedPolicy, runnerRoleARN, externalID, managementPrincipalARN); err != nil {
		return fmt.Errorf("verify managed instance role trust policy: %w", err)
	}
	return nil
}

func updateAndReadBackAssumeRoleTrustPolicy(ctx context.Context, childIAM iamAPI, roleName string, policy string) (string, error) {
	if _, err := childIAM.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyDocument: aws.String(policy),
	}); err != nil {
		return "", fmt.Errorf("update managed instance role trust policy: %w", err)
	}

	verified, err := childIAM.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		return "", fmt.Errorf("verify managed instance role trust policy: %w", err)
	}
	verifiedPolicy := managedInstanceRoleTrustPolicy(verified)
	if verifiedPolicy == "" {
		return "", fmt.Errorf("verify managed instance role trust policy: policy is empty")
	}
	return verifiedPolicy, nil
}

func managedInstanceRoleTrustPolicy(out *iam.GetRoleOutput) string {
	if out == nil || out.Role == nil {
		return ""
	}
	return strings.TrimSpace(aws.ToString(out.Role.AssumeRolePolicyDocument))
}

func (s *Server) resolveManagementRootPrincipalARN(runnerRoleARN string) (string, error) {
	managementIdentityARN := strings.TrimSpace(runnerRoleARN)
	if s != nil && strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN) != "" {
		managementIdentityARN = strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN)
		if err := validateIAMRoleARN(managementIdentityARN); err != nil {
			return "", fmt.Errorf("invalid managed org vending role arn: %w", err)
		}
	}
	return managementRootPrincipalARN(managementIdentityARN)
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
	if parsed.Service != iamServiceName || parsed.AccountID == "" || !strings.HasPrefix(parsed.Resource, "role/") {
		return fmt.Errorf("expected iam role arn")
	}
	return nil
}

func validateIAMRootARN(value string) error {
	parsed, err := arn.Parse(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid management root principal arn: %w", err)
	}
	if parsed.Service != iamServiceName || parsed.AccountID == "" || parsed.Resource != "root" {
		return fmt.Errorf("invalid management root principal arn: expected iam account root arn")
	}
	return nil
}

func ensureAssumeRolePolicyAllowsPrincipal(rawPolicy string, principalARN string, externalID string, managementPrincipalARN string) (string, bool, error) {
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

	if rootErr := validateIAMRootARN(managementPrincipalARN); rootErr != nil {
		return "", false, rootErr
	}

	updatedStatements, changed, err := applyExternalIDCondition(statements, principalARN, externalID, managementPrincipalARN)
	if err != nil {
		return "", false, err
	}
	if !changed {
		return policyJSON, false, nil
	}

	doc["Statement"] = updatedStatements
	updated, err := json.Marshal(doc)
	if err != nil {
		return "", false, fmt.Errorf("marshal managed instance role trust policy: %w", err)
	}
	return string(updated), true, nil
}

func verifyDeployRunnerAssumeRoleTrustPolicy(rawPolicy string, principalARN string, externalID string, managementPrincipalARN string) error {
	policyJSON, err := decodeAssumeRolePolicyDocument(rawPolicy)
	if err != nil {
		return err
	}
	var doc map[string]any
	if unmarshalErr := json.Unmarshal([]byte(policyJSON), &doc); unmarshalErr != nil {
		return fmt.Errorf("parse managed instance role trust policy: %w", unmarshalErr)
	}
	statements, err := normalizedPolicyStatements(doc["Statement"])
	if err != nil {
		return err
	}

	runnerAllowFound := false
	runnerDenyFound := false
	managementTrustFound := false
	for _, statement := range statements {
		if assumeRoleStatementAllowsExactPrincipal(statement, principalARN) && assumeRoleStatementHasExternalID(statement, externalID) {
			runnerAllowFound = true
		}
		if assumeRoleStatementDeniesExternalIDMismatch(statement, principalARN, externalID) {
			runnerDenyFound = true
		}
		if assumeRoleStatementAllowsExactPrincipal(statement, managementPrincipalARN) && !conditionMentionsExternalID(statement) {
			managementTrustFound = true
		}
	}
	if !runnerAllowFound {
		return fmt.Errorf("exact deploy runner ExternalId allow is missing")
	}
	if !runnerDenyFound {
		return fmt.Errorf("exact deploy runner ExternalId mismatch deny is missing")
	}
	if !managementTrustFound {
		return fmt.Errorf("management root trust is missing")
	}
	return nil
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

// applyExternalIDCondition ensures the statements list includes an explicit
// statement that allows the deploy-runner principal with the required
// external-ID condition. If a broad/wildcard statement is hardened, it is not
// collapsed to runner-only trust: direct Host runtime assume-role access is
// preserved through the management-account root principal, and a deny guard
// keeps that management trust from becoming an unconditioned deploy-runner
// bypass.
func applyExternalIDCondition(statements []any, principalARN string, externalID string, managementPrincipalARN string) ([]any, bool, error) {
	if strings.TrimSpace(managementPrincipalARN) == "" {
		return nil, false, fmt.Errorf("management principal arn is required")
	}

	transform := newExternalIDConditionTransform(principalARN, externalID, managementPrincipalARN, len(statements))
	for _, statement := range statements {
		if err := transform.processStatement(statement); err != nil {
			return nil, false, err
		}
	}
	updatedStatements, changed := transform.finish()
	if !changed {
		return statements, false, nil
	}
	return updatedStatements, true, nil
}

type externalIDConditionTransform struct {
	principalARN           string
	externalID             string
	managementPrincipalARN string

	runnerAllowFound     bool
	runnerDenyFound      bool
	managementTrustFound bool
	changed              bool
	filtered             []any
}

func newExternalIDConditionTransform(principalARN string, externalID string, managementPrincipalARN string, statementCount int) *externalIDConditionTransform {
	return &externalIDConditionTransform{
		principalARN:           principalARN,
		externalID:             externalID,
		managementPrincipalARN: managementPrincipalARN,
		runnerDenyFound:        externalID == "",
		filtered:               make([]any, 0, statementCount+3),
	}
}

func (t *externalIDConditionTransform) processStatement(statement any) error {
	if t.externalID != "" && assumeRoleStatementDeniesExternalIDMismatch(statement, t.principalARN, t.externalID) {
		t.runnerDenyFound = true
	}
	if !assumeRoleStatementAllowsPrincipal(statement, t.principalARN) {
		t.keepStatement(statement)
		return nil
	}

	broadPrincipal := assumeRoleStatementHasWildcardPrincipal(statement)
	if !broadPrincipal && !assumeRoleStatementHasExactPrincipal(statement, t.principalARN) {
		return fmt.Errorf("refusing to rewrite deploy runner trust shared with other principals")
	}
	if t.externalID == "" || (assumeRoleStatementHasExternalID(statement, t.externalID) && assumeRoleStatementHasExactPrincipal(statement, t.principalARN) && !broadPrincipal) {
		t.keepRunnerStatement(statement)
		return nil
	}

	if broadPrincipal || assumeRoleStatementAllowsExplicitPrincipal(statement, t.managementPrincipalARN) {
		if err := t.addManagementStatement(statement); err != nil {
			return err
		}
	}
	t.changed = true
	return nil
}

func (t *externalIDConditionTransform) keepStatement(statement any) {
	t.filtered = append(t.filtered, statement)
	if assumeRoleStatementAllowsExplicitPrincipal(statement, t.managementPrincipalARN) {
		t.managementTrustFound = true
	}
}

func (t *externalIDConditionTransform) keepRunnerStatement(statement any) {
	t.filtered = append(t.filtered, statement)
	t.runnerAllowFound = true
}

func (t *externalIDConditionTransform) addManagementStatement(statement any) error {
	if t.managementTrustFound {
		return nil
	}
	managementStatement, err := directManagementTrustStatementFrom(statement, t.managementPrincipalARN)
	if err != nil {
		return err
	}
	t.filtered = append(t.filtered, managementStatement)
	t.managementTrustFound = true
	return nil
}

func (t *externalIDConditionTransform) finish() ([]any, bool) {
	if !t.runnerAllowFound {
		t.filtered = append(t.filtered, deployRunnerAssumeRoleStatement(t.principalARN, t.externalID))
		t.changed = true
	}
	if t.externalID != "" && !t.runnerDenyFound {
		t.filtered = append(t.filtered, deployRunnerExternalIDDenyStatement(t.principalARN, t.externalID))
		t.changed = true
	}
	return t.filtered, t.changed
}

func deployRunnerAssumeRoleStatement(principalARN string, externalID string) map[string]any {
	statement := map[string]any{
		"Effect": "Allow",
		"Principal": map[string]any{
			"AWS": principalARN,
		},
		"Action": "sts:AssumeRole",
	}
	if externalID != "" {
		statement["Condition"] = map[string]any{
			"StringEquals": map[string]any{
				"sts:ExternalId": externalID,
			},
		}
	}
	return statement
}

func deployRunnerExternalIDDenyStatement(principalARN string, externalID string) map[string]any {
	return map[string]any{
		"Effect": "Deny",
		"Principal": map[string]any{
			"AWS": principalARN,
		},
		"Action": "sts:AssumeRole",
		"Condition": map[string]any{
			"StringNotEquals": map[string]any{
				"sts:ExternalId": externalID,
			},
		},
	}
}

func directManagementTrustStatementFrom(statement any, managementPrincipalARN string) (map[string]any, error) {
	if conditionMentionsExternalID(statement) {
		return nil, fmt.Errorf("refusing to synthesize direct management trust from ExternalId-conditioned broad trust; restore explicit Host runtime management trust before hardening deploy-runner trust")
	}

	managementStatement := map[string]any{
		"Effect": "Allow",
		"Principal": map[string]any{
			"AWS": managementPrincipalARN,
		},
		"Action": "sts:AssumeRole",
	}

	stmt, ok := statement.(map[string]any)
	if !ok {
		return managementStatement, nil
	}
	if condition, ok := stmt["Condition"]; ok {
		clonedCondition, err := clonePolicyJSONValue(condition)
		if err != nil {
			return nil, fmt.Errorf("clone management trust condition: %w", err)
		}
		managementStatement["Condition"] = clonedCondition
	}
	return managementStatement, nil
}

func clonePolicyJSONValue(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var cloned any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func managementRootPrincipalARN(principalARN string) (string, error) {
	parsed, err := arn.Parse(strings.TrimSpace(principalARN))
	if err != nil {
		return "", fmt.Errorf("derive management principal from deploy runner arn: %w", err)
	}
	if parsed.Service != "iam" || parsed.AccountID == "" {
		return "", fmt.Errorf("derive management principal from deploy runner arn: expected iam role arn")
	}
	return fmt.Sprintf("arn:aws:iam::%s:root", parsed.AccountID), nil
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

func assumeRoleStatementAllowsExactPrincipal(statement any, principalARN string) bool {
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
	return principalExactlyARN(stmt["Principal"], principalARN)
}

func assumeRoleStatementHasExactPrincipal(statement any, principalARN string) bool {
	stmt, ok := statement.(map[string]any)
	return ok && principalExactlyARN(stmt["Principal"], principalARN)
}

func assumeRoleStatementAllowsExplicitPrincipal(statement any, principalARN string) bool {
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
	return principalNamesARN(stmt["Principal"], principalARN)
}

func assumeRoleStatementDeniesExternalIDMismatch(statement any, principalARN string, externalID string) bool {
	stmt, ok := statement.(map[string]any)
	if !ok {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(stmt["Effect"])), "Deny") {
		return false
	}
	if !policyValueContains(stmt["Action"], "sts:AssumeRole") && !policyValueContains(stmt["Action"], "sts:*") && !policyValueContains(stmt["Action"], "*") {
		return false
	}
	if !principalExactlyARN(stmt["Principal"], principalARN) {
		return false
	}
	condition, ok := stmt["Condition"].(map[string]any)
	if !ok {
		return false
	}
	stringNotEquals, ok := condition["StringNotEquals"].(map[string]any)
	if !ok {
		return false
	}
	got, ok := stringNotEquals["sts:ExternalId"].(string)
	return ok && strings.TrimSpace(got) == strings.TrimSpace(externalID)
}

func assumeRoleStatementHasWildcardPrincipal(statement any) bool {
	stmt, ok := statement.(map[string]any)
	if !ok {
		return false
	}
	return principalHasWildcard(stmt["Principal"])
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

func principalExactlyARN(value any, principalARN string) bool {
	principalARN = strings.TrimSpace(principalARN)
	switch v := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(v), principalARN)
	case map[string]any:
		if len(v) != 1 {
			return false
		}
		awsValue, ok := v["AWS"]
		return ok && policyValueExactly(awsValue, principalARN)
	default:
		return false
	}
}

func policyValueExactly(value any, want string) bool {
	switch v := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(want))
	case []any:
		return len(v) == 1 && policyValueExactly(v[0], want)
	case []string:
		return len(v) == 1 && policyValueExactly(v[0], want)
	default:
		return false
	}
}

func principalNamesARN(value any, principalARN string) bool {
	if policyValueContains(value, principalARN) {
		return true
	}
	principal, ok := value.(map[string]any)
	if !ok {
		return false
	}
	return policyValueContains(principal["AWS"], principalARN)
}

func principalHasWildcard(value any) bool {
	if policyValueContains(value, "*") {
		return true
	}
	principal, ok := value.(map[string]any)
	if !ok {
		return false
	}
	return policyValueContains(principal["AWS"], "*")
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

func conditionMentionsExternalID(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if strings.EqualFold(strings.TrimSpace(key), "sts:ExternalId") {
				return true
			}
			if conditionMentionsExternalID(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if conditionMentionsExternalID(item) {
				return true
			}
		}
	case []string:
		return false
	}
	return false
}
