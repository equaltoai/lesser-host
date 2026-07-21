package provisionworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
)

const (
	testDeployRunnerRoleARN         = "arn:aws:iam::111122223333:role/lesser-host-lab-ProvisionRunnerProjectRole"
	testManagementRootARN           = "arn:aws:iam::111122223333:root"
	testExternalManagementRootARN   = "arn:aws:iam::902552026581:root"
	testExternalManagementRootTrust = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + testExternalManagementRootARN + `"},"Action":"sts:AssumeRole"}]}`
)

type fakeIAM struct {
	getOut                *iam.GetRoleOutput
	getErr                error
	updErr                error
	keepPolicyAfterUpdate bool

	getInputs    []*iam.GetRoleInput
	updateInputs []*iam.UpdateAssumeRolePolicyInput
}

func (f *fakeIAM) GetRole(_ context.Context, in *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	f.getInputs = append(f.getInputs, in)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getOut, nil
}

func (f *fakeIAM) UpdateAssumeRolePolicy(_ context.Context, in *iam.UpdateAssumeRolePolicyInput, _ ...func(*iam.Options)) (*iam.UpdateAssumeRolePolicyOutput, error) {
	f.updateInputs = append(f.updateInputs, in)
	if f.updErr != nil {
		return nil, f.updErr
	}
	if !f.keepPolicyAfterUpdate && f.getOut != nil && f.getOut.Role != nil {
		f.getOut.Role.AssumeRolePolicyDocument = aws.String(aws.ToString(in.PolicyDocument))
	}
	return &iam.UpdateAssumeRolePolicyOutput{}, nil
}

func TestEnsureAssumeRolePolicyAllowsPrincipalAddsDeployRunner(t *testing.T) {
	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(testExternalManagementRootTrust, testDeployRunnerRoleARN, deployRunnerExternalID("demo"), testExternalManagementRootARN)
	require.NoError(t, err)
	require.True(t, changed)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(updated), &doc))
	statements, err := normalizedPolicyStatements(doc["Statement"])
	require.NoError(t, err)
	// The original root statement is preserved; exact allow and mismatch-deny
	// statements are added for the deploy runner.
	require.Len(t, statements, 3)
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testExternalManagementRootARN, ""))
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("demo")),
		"new statement must carry the tenant-scoped external ID condition")
	require.True(t, hasAssumeRoleExternalIDDenyForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("demo")))
}

func TestEnsureAssumeRolePolicyAllowsPrincipalPreservesManagementRootTrust(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Sid":"DirectHostRuntime","Effect":"Allow","Principal":{"AWS":"` + testManagementRootARN + `"},"Action":"sts:AssumeRole"}]}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, deployRunnerExternalID("demo"), testManagementRootARN)
	require.NoError(t, err)
	require.True(t, changed)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(updated), &doc))
	statements, err := normalizedPolicyStatements(doc["Statement"])
	require.NoError(t, err)
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testManagementRootARN, ""),
		"existing management-root trust must remain so direct Host runtime roles are not stranded")
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("demo")),
		"deploy runner must keep tenant-scoped ExternalId trust")
	require.True(t, hasAssumeRoleExternalIDDenyForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("demo")),
		"management-root trust must not become an unconditioned deploy-runner bypass")
}

func TestEnsureAssumeRolePolicyAllowsPrincipalHardensWildcardWithoutStrandingManagement(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Sid":"LegacyBroadTrust","Effect":"Allow","Principal":"*","Action":"sts:AssumeRole"}]}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, deployRunnerExternalID("theory"), testManagementRootARN)
	require.NoError(t, err)
	require.True(t, changed)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(updated), &doc))
	statements, err := normalizedPolicyStatements(doc["Statement"])
	require.NoError(t, err)
	require.False(t, hasAssumeRoleAllowForPrincipal(statements, "*", ""),
		"legacy wildcard assume-role trust must be hardened instead of preserved")
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testManagementRootARN, ""),
		"hardening broad trust must preserve direct Host runtime access through explicit management trust")
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("theory")),
		"deploy runner trust must remain tenant-scoped by ExternalId")
	require.True(t, hasAssumeRoleExternalIDDenyForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("theory")),
		"deploy runner must not be able to use synthesized management trust without ExternalId")
}

func TestEnsureAssumeRolePolicyAllowsPrincipalHardensWildcardAndClonesNonExternalIDCondition(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Sid":"LegacyBroadTrust","Effect":"Allow","Principal":{"AWS":["*"]},"Action":"*","Condition":{"StringEquals":{"aws:PrincipalOrgID":"o-equaltoai"}}}]}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, deployRunnerExternalID("theory"), testManagementRootARN)
	require.NoError(t, err)
	require.True(t, changed)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(updated), &doc))
	statements, err := normalizedPolicyStatements(doc["Statement"])
	require.NoError(t, err)
	require.False(t, hasAssumeRoleAllowForPrincipal(statements, "*", ""),
		"legacy wildcard assume-role trust must be replaced even when it carries non-ExternalId conditions")

	managementStatement := findAssumeRoleAllowForPrincipal(statements, testManagementRootARN, "")
	require.NotNil(t, managementStatement, "management root trust must be synthesized from the broad trust")
	condition, ok := managementStatement["Condition"].(map[string]any)
	require.True(t, ok, "non-ExternalId conditions should be preserved on synthesized management trust")
	stringEquals, ok := condition["StringEquals"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "o-equaltoai", stringEquals["aws:PrincipalOrgID"])
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("theory")),
		"deploy runner trust must remain tenant-scoped by ExternalId")
	require.True(t, hasAssumeRoleExternalIDDenyForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("theory")),
		"deploy runner must not be able to use synthesized management trust without ExternalId")
}

func TestEnsureAssumeRolePolicyAllowsPrincipalHardensWildcardWithoutDuplicatingExistingManagementTrust(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Sid":"DirectHostRuntime","Effect":"Allow","Principal":{"AWS":"` + testManagementRootARN + `"},"Action":"sts:AssumeRole"},{"Sid":"LegacyBroadTrust","Effect":"Allow","Principal":"*","Action":"sts:AssumeRole"}]}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, deployRunnerExternalID("demo"), testManagementRootARN)
	require.NoError(t, err)
	require.True(t, changed)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(updated), &doc))
	statements, err := normalizedPolicyStatements(doc["Statement"])
	require.NoError(t, err)
	require.False(t, hasAssumeRoleAllowForPrincipal(statements, "*", ""),
		"legacy wildcard assume-role trust must be hardened instead of preserved")
	require.Equal(t, 1, countAssumeRoleAllowsForPrincipal(statements, testManagementRootARN, ""),
		"hardening should reuse the existing direct Host runtime management trust instead of duplicating it")
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("demo")),
		"deploy runner trust must remain tenant-scoped by ExternalId")
	require.True(t, hasAssumeRoleExternalIDDenyForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("demo")),
		"deploy runner must not be able to use management trust without ExternalId")
}

func TestEnsureAssumeRolePolicyAllowsPrincipalRejectsConditionedWildcardTrust(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"lesser-host/deploy/demo"}}}]}`

	_, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, deployRunnerExternalID("demo"), testManagementRootARN)
	require.Error(t, err)
	require.False(t, changed)
	require.Contains(t, err.Error(), "refusing to synthesize direct management trust")
}

func TestEnsureAssumeRolePolicyAllowsPrincipalRejectsNestedConditionedWildcardTrust(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"sts:AssumeRole","Condition":{"ForAnyValue:StringEquals":[{"sts:ExternalId":"lesser-host/deploy/demo"}]}}]}`

	_, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, deployRunnerExternalID("demo"), testManagementRootARN)
	require.Error(t, err)
	require.False(t, changed)
	require.Contains(t, err.Error(), "refusing to synthesize direct management trust")
}

func TestEnsureAssumeRolePolicyAllowsPrincipalRejectsMixedRunnerPrincipal(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["` + testDeployRunnerRoleARN + `","arn:aws:iam::444455556666:role/other"]},"Action":"sts:AssumeRole"}]}`

	_, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, deployRunnerExternalID("demo"), testManagementRootARN)
	require.ErrorContains(t, err, "shared with other principals")
	require.False(t, changed)
}

func TestEnsureAssumeRolePolicyAllowsPrincipalAddsConditionToExistingStatement(t *testing.T) {
	// An existing statement that allows the principal but lacks the
	// external-ID condition must be replaced with a conditioned one.
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + testManagementRootARN + `"},"Action":"sts:AssumeRole"},{"Effect":"Allow","Principal":{"AWS":["` + testDeployRunnerRoleARN + `"]},"Action":["sts:AssumeRole"]}]}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(url.QueryEscape(policy), testDeployRunnerRoleARN, deployRunnerExternalID("demo"), testManagementRootARN)
	require.NoError(t, err)
	require.True(t, changed)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(updated), &doc))
	statements, err := normalizedPolicyStatements(doc["Statement"])
	require.NoError(t, err)
	require.Len(t, statements, 3)
	require.True(t, hasAssumeRoleAllowForPrincipal(statements, testDeployRunnerRoleARN, deployRunnerExternalID("demo")))
	require.True(t, assumeRoleStatementHasExternalID(statements[1], deployRunnerExternalID("demo")),
		"existing statement must be upgraded to include the tenant-scoped external ID condition")
}

func TestEnsureAssumeRolePolicyAllowsPrincipalAddsMissingMismatchDeny(t *testing.T) {
	// The external-ID allow alone is incomplete until the explicit mismatch deny exists.
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + testManagementRootARN + `"},"Action":"sts:AssumeRole"},{"Effect":"Allow","Principal":{"AWS":"` + testDeployRunnerRoleARN + `"},"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"lesser-host/deploy/demo"}}}]}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, deployRunnerExternalID("demo"), testManagementRootARN)
	require.NoError(t, err)
	require.True(t, changed, "a conditioned allow without the mismatch deny is incomplete")
	require.Contains(t, updated, "StringNotEquals")
}

func TestEnsureAssumeRolePolicyAllowsPrincipalNoopsForExistingUnconditionedWhenNoExternalID(t *testing.T) {
	// When no external ID is required, an existing unconditioned statement is left alone.
	policy := `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Principal":{"AWS":["` + testDeployRunnerRoleARN + `"]},"Action":["sts:AssumeRole"]}}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(url.QueryEscape(policy), testDeployRunnerRoleARN, "", testManagementRootARN)
	require.NoError(t, err)
	require.False(t, changed, "when external ID is empty, existing unconditioned statement should not be replaced")
	require.JSONEq(t, policy, updated)
}

func TestEnsureAssumeRolePolicyAllowsPrincipalNoopsWhenDirectRunnerAndDenyTrustAlreadyHardened(t *testing.T) {
	externalID := deployRunnerExternalID("demo")
	policy := `{"Version":"2012-10-17","Statement":[{"Sid":"DirectHostRuntime","Effect":"Allow","Principal":{"AWS":"` + testManagementRootARN + `"},"Action":"sts:AssumeRole"},{"Sid":"DeployRunner","Effect":"Allow","Principal":{"AWS":"` + testDeployRunnerRoleARN + `"},"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"` + externalID + `"}}},{"Sid":"DenyRunnerExternalIDMismatch","Effect":"Deny","Principal":{"AWS":"` + testDeployRunnerRoleARN + `"},"Action":"sts:AssumeRole","Condition":{"StringNotEquals":{"sts:ExternalId":"` + externalID + `"}}}]}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN, externalID, testManagementRootARN)
	require.NoError(t, err)
	require.False(t, changed, "complete hardened trust should not be rewritten or grow duplicate deny statements")
	require.JSONEq(t, policy, updated)
}

func TestEnsureDeployRunnerAssumeRoleTrustUpdatesTenantRole(t *testing.T) {
	t.Parallel()

	fake := &fakeIAM{getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(testExternalManagementRootTrust)}}}

	srv := &Server{
		cfg: config.Config{
			ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN,
			ManagedOrgVendingRoleARN:      "arn:aws:iam::902552026581:role/lesser-host-org-vending",
		},
		iamFactory: func(_ context.Context, accountID string, roleName string, region string, slug string, jobID string) (iamAPI, error) {
			require.Equal(t, "123456789012", accountID)
			require.Equal(t, "OrganizationAccountAccessRole", roleName)
			require.Equal(t, defaultManagedAWSRegion, region)
			require.Equal(t, "simulacrum", slug)
			require.Equal(t, "job1", jobID)
			return fake, nil
		},
	}

	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "simulacrum", "job1")
	require.NoError(t, err)
	require.Len(t, fake.getInputs, 2, "updated trust must be read back before CodeBuild may start")
	require.Equal(t, "OrganizationAccountAccessRole", aws.ToString(fake.getInputs[0].RoleName))
	require.Len(t, fake.updateInputs, 1)
	require.Equal(t, "OrganizationAccountAccessRole", aws.ToString(fake.updateInputs[0].RoleName))
	require.Contains(t, aws.ToString(fake.updateInputs[0].PolicyDocument), testDeployRunnerRoleARN)
	require.NoError(t, verifyDeployRunnerAssumeRoleTrustPolicy(
		aws.ToString(fake.getOut.Role.AssumeRolePolicyDocument),
		testDeployRunnerRoleARN,
		deployRunnerExternalID("simulacrum"),
		testExternalManagementRootARN,
	))

	// A retry verifies the same policy without appending duplicate statements.
	err = srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "simulacrum", "job1")
	require.NoError(t, err)
	require.Len(t, fake.getInputs, 3)
	require.Len(t, fake.updateInputs, 1)
}

func TestEnsureDeployRunnerAssumeRoleTrustPreservesExistingExternalManagementRootWithoutVendingRoleConfig(t *testing.T) {
	t.Parallel()

	fake := &fakeIAM{getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(testExternalManagementRootTrust)}}}
	srv := &Server{
		cfg: config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
		iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) {
			return fake, nil
		},
	}

	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "trenchcoat", "job1")
	require.NoError(t, err)
	require.Len(t, fake.updateInputs, 1)
	updated := aws.ToString(fake.updateInputs[0].PolicyDocument)
	require.Contains(t, updated, testExternalManagementRootARN)
	require.NotContains(t, updated, testManagementRootARN,
		"the Organizations management root must come from existing trust, not the differently-accounted runner role")
	require.NoError(t, verifyDeployRunnerAssumeRoleTrustPolicy(
		updated,
		testDeployRunnerRoleARN,
		deployRunnerExternalID("trenchcoat"),
		testExternalManagementRootARN,
	))
}

func TestEnsureDeployRunnerAssumeRoleTrustRejectsMissingManagementRootIdentity(t *testing.T) {
	t.Parallel()

	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + testDeployRunnerRoleARN + `"},"Action":"sts:AssumeRole"}]}`
	fake := &fakeIAM{getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(policy)}}}
	srv := &Server{
		cfg: config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
		iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) {
			return fake, nil
		},
	}

	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
	require.ErrorContains(t, err, "management root trust")
	require.Empty(t, fake.updateInputs, "a missing management identity must fail before mutating role trust")
}

func TestEnsureDeployRunnerAssumeRoleTrustRejectsConfiguredManagementRootMismatchBeforeWrite(t *testing.T) {
	t.Parallel()

	fake := &fakeIAM{getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(testExternalManagementRootTrust)}}}
	srv := &Server{
		cfg: config.Config{
			ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN,
			ManagedOrgVendingRoleARN:      "arn:aws:iam::111122223333:role/wrong-management-account",
		},
		iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) {
			return fake, nil
		},
	}

	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
	require.ErrorContains(t, err, "management root trust is missing")
	require.Empty(t, fake.updateInputs, "a mismatched configured root must fail before mutating the observed Organizations trust")
}

func TestEnsureDeployRunnerAssumeRoleTrustNoopsWhenRunnerRoleUnset(t *testing.T) {
	t.Parallel()

	called := false
	srv := &Server{iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) {
		called = true
		return nil, nil
	}}

	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
	require.NoError(t, err)
	require.False(t, called)
}

func TestEnsureDeployRunnerAssumeRoleTrustUpdatesWhenExistingStatementLacksExternalID(t *testing.T) {
	t.Parallel()

	// Runner is already allowed but the statement lacks the external-ID condition.
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + testManagementRootARN + `"},"Action":"sts:AssumeRole"},{"Effect":"Allow","Principal":{"AWS":"` + testDeployRunnerRoleARN + `"},"Action":"sts:AssumeRole"}]}`
	fake := &fakeIAM{getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(policy)}}}
	srv := &Server{
		cfg:        config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
		iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) { return fake, nil },
	}

	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
	require.NoError(t, err)
	require.Len(t, fake.getInputs, 2)
	// Should update — the existing statement lacks the external-ID condition.
	require.Len(t, fake.updateInputs, 1)
	require.Contains(t, aws.ToString(fake.updateInputs[0].PolicyDocument), "lesser-host/deploy/demo")
}

func TestEnsureDeployRunnerAssumeRoleTrustNoopsWhenAlreadyConditioned(t *testing.T) {
	t.Parallel()

	// Root trust, exact runner allow, and mismatch deny are already present.
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + testManagementRootARN + `"},"Action":"sts:AssumeRole"},{"Effect":"Allow","Principal":{"AWS":"` + testDeployRunnerRoleARN + `"},"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"lesser-host/deploy/demo"}}},{"Effect":"Deny","Principal":{"AWS":"` + testDeployRunnerRoleARN + `"},"Action":"sts:AssumeRole","Condition":{"StringNotEquals":{"sts:ExternalId":"lesser-host/deploy/demo"}}}]}`
	fake := &fakeIAM{getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(policy)}}}
	srv := &Server{
		cfg:        config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
		iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) { return fake, nil },
	}

	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
	require.NoError(t, err)
	require.Len(t, fake.getInputs, 1)
	require.Empty(t, fake.updateInputs)
}

func TestEnsureDeployRunnerAssumeRoleTrustReportsPolicyReadAndWriteFailures(t *testing.T) {
	t.Parallel()

	t.Run("nil server", func(t *testing.T) {
		t.Parallel()

		err := (*Server)(nil).ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "server not initialized")
	})

	t.Run("missing target role identity", func(t *testing.T) {
		t.Parallel()

		srv := &Server{cfg: config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN}}
		err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "account id and role name are required")
	})

	t.Run("get role error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("get role failed")
		fake := &fakeIAM{getErr: expectedErr}
		srv := &Server{
			cfg:        config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
			iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) { return fake, nil },
		}

		err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
		require.ErrorIs(t, err, expectedErr)
		require.Contains(t, err.Error(), "get managed instance role trust policy")
		require.Empty(t, fake.updateInputs)
	})

	t.Run("empty role policy", func(t *testing.T) {
		t.Parallel()

		fake := &fakeIAM{getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(" ")}}}
		srv := &Server{
			cfg:        config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
			iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) { return fake, nil },
		}

		err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "managed instance role trust policy is empty")
		require.Empty(t, fake.updateInputs)
	})

	t.Run("update role policy error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("update failed")
		fake := &fakeIAM{
			getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(testExternalManagementRootTrust)}},
			updErr: expectedErr,
		}
		srv := &Server{
			cfg:        config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
			iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) { return fake, nil },
		}

		err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
		require.ErrorIs(t, err, expectedErr)
		require.Contains(t, err.Error(), "update managed instance role trust policy")
		require.Len(t, fake.updateInputs, 1)
	})

	t.Run("updated policy cannot be verified", func(t *testing.T) {
		t.Parallel()

		fake := &fakeIAM{
			getOut:                &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(testExternalManagementRootTrust)}},
			keepPolicyAfterUpdate: true,
		}
		srv := &Server{
			cfg: config.Config{
				ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN,
				ManagedOrgVendingRoleARN:      "arn:aws:iam::902552026581:role/lesser-host-org-vending",
			},
			iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) { return fake, nil },
		}

		err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
		require.ErrorContains(t, err, "exact deploy runner ExternalId allow is missing")
		require.Len(t, fake.getInputs, 2)
		require.Len(t, fake.updateInputs, 1)
	})
}

func TestEnsureDeployRunnerAssumeRoleTrustRejectsInvalidRunnerRoleARN(t *testing.T) {
	t.Parallel()

	srv := &Server{cfg: config.Config{ManagedProvisionRunnerRoleARN: "not-an-arn"}}
	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid managed provision runner role arn")
}

func TestEnsureDeployRunnerAssumeRoleTrustPropagatesIAMFactoryError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("iam factory failed")
	srv := &Server{
		cfg:        config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
		iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) { return nil, expectedErr },
	}

	err := srv.ensureDeployRunnerAssumeRoleTrust(context.Background(), "123456789012", "OrganizationAccountAccessRole", defaultManagedAWSRegion, "demo", "job1")
	require.ErrorIs(t, err, expectedErr)
}

func TestChildIAMClientUsesAssumedInstanceCredentials(t *testing.T) {
	t.Parallel()

	srv := &Server{
		cfg: config.Config{ManagedDefaultRegion: defaultManagedAWSRegion},
		sts: &fakeSTS{out: &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{
			AccessKeyId:     aws.String("AKIAIOSFODNN7EXAMPLE"),
			SecretAccessKey: aws.String("secret"), // #nosec G101 -- synthetic unit-test credential
			SessionToken:    aws.String("token"),  // #nosec G101 -- synthetic unit-test credential
		}}},
	}

	client, err := srv.childIAMClient(context.Background(), "123456789012", "OrganizationAccountAccessRole", "", "demo", "job1")
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestAssumeRolePolicyHelperBranches(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateIAMRoleARN(testDeployRunnerRoleARN))
	require.Error(t, validateIAMRoleARN("arn:aws:s3:::bucket"))

	pathEscaped := url.PathEscape(`{"Statement":{"Effect":"Allow","Principal":"*","Action":"*"}}`)
	decoded, err := decodeAssumeRolePolicyDocument(pathEscaped)
	require.NoError(t, err)
	require.Contains(t, decoded, `"Principal":"*"`)
	_, err = decodeAssumeRolePolicyDocument("not-json")
	require.Error(t, err)

	statements, err := normalizedPolicyStatements(map[string]any{"Effect": "Allow"})
	require.NoError(t, err)
	require.Len(t, statements, 1)
	_, err = normalizedPolicyStatements("bad")
	require.Error(t, err)

	require.True(t, assumeRoleStatementAllowsPrincipal(map[string]any{
		"Effect":    "Allow",
		"Principal": "*",
		"Action":    []any{"sts:*"},
	}, testDeployRunnerRoleARN))
	require.False(t, assumeRoleStatementAllowsPrincipal(map[string]any{
		"Effect":    "Deny",
		"Principal": "*",
		"Action":    "sts:AssumeRole",
	}, testDeployRunnerRoleARN))
	require.False(t, assumeRoleStatementDeniesExternalIDMismatch(map[string]any{
		"Effect":    "Deny",
		"Principal": "*",
		"Action":    "sts:AssumeRole",
		"Condition": map[string]any{"StringEquals": map[string]any{"sts:ExternalId": deployRunnerExternalID("demo")}},
	}, testDeployRunnerRoleARN, deployRunnerExternalID("demo")))
	require.True(t, assumeRoleStatementHasWildcardPrincipal(map[string]any{
		"Principal": map[string]any{"AWS": []any{"*"}},
	}))
	require.True(t, principalNamesARN(map[string]any{"AWS": []string{testDeployRunnerRoleARN}}, testDeployRunnerRoleARN))
	require.False(t, conditionMentionsExternalID([]string{"sts:ExternalId"}))
	require.Equal(t, "lesser-host/deploy/unknown", deployRunnerExternalID(" "))
}

func findAssumeRoleAllowForPrincipal(statements []any, principalARN string, externalID string) map[string]any {
	for _, statement := range statements {
		if !assumeRoleStatementAllowsPrincipal(statement, principalARN) {
			continue
		}
		if strings.TrimSpace(externalID) != "" && !assumeRoleStatementHasExternalID(statement, externalID) {
			continue
		}
		stmt, ok := statement.(map[string]any)
		if ok {
			return stmt
		}
	}
	return nil
}

func hasAssumeRoleAllowForPrincipal(statements []any, principalARN string, externalID string) bool {
	return findAssumeRoleAllowForPrincipal(statements, principalARN, externalID) != nil
}

func countAssumeRoleAllowsForPrincipal(statements []any, principalARN string, externalID string) int {
	var count int
	for _, statement := range statements {
		if !assumeRoleStatementAllowsPrincipal(statement, principalARN) {
			continue
		}
		if strings.TrimSpace(externalID) == "" || assumeRoleStatementHasExternalID(statement, externalID) {
			count++
		}
	}
	return count
}

func hasAssumeRoleExternalIDDenyForPrincipal(statements []any, principalARN string, externalID string) bool {
	for _, statement := range statements {
		stmt, ok := statement.(map[string]any)
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(stmt["Effect"])), "Deny") {
			continue
		}
		if !policyValueContains(stmt["Action"], "sts:AssumeRole") && !policyValueContains(stmt["Action"], "sts:*") && !policyValueContains(stmt["Action"], "*") {
			continue
		}
		if !principalAllowsARN(stmt["Principal"], principalARN) {
			continue
		}
		condition, ok := stmt["Condition"].(map[string]any)
		if !ok {
			continue
		}
		stringNotEquals, ok := condition["StringNotEquals"].(map[string]any)
		if !ok {
			continue
		}
		got, ok := stringNotEquals["sts:ExternalId"].(string)
		if ok && strings.TrimSpace(got) == strings.TrimSpace(externalID) {
			return true
		}
	}
	return false
}
