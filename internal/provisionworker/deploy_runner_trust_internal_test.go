package provisionworker

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
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
	testDeployRunnerRoleARN = "arn:aws:iam::111122223333:role/lesser-host-lab-ProvisionRunnerProjectRole"
)

type fakeIAM struct {
	getOut *iam.GetRoleOutput
	getErr error
	updErr error

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
	return &iam.UpdateAssumeRolePolicyOutput{}, nil
}

func TestEnsureAssumeRolePolicyAllowsPrincipalAddsDeployRunner(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::902552026581:root"},"Action":"sts:AssumeRole"}]}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(policy, testDeployRunnerRoleARN)
	require.NoError(t, err)
	require.True(t, changed)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(updated), &doc))
	statements, err := normalizedPolicyStatements(doc["Statement"])
	require.NoError(t, err)
	require.Len(t, statements, 2)
	require.True(t, assumeRoleStatementAllowsPrincipal(statements[1], testDeployRunnerRoleARN))
}

func TestEnsureAssumeRolePolicyAllowsPrincipalNoopsForExistingEncodedTrust(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Principal":{"AWS":["` + testDeployRunnerRoleARN + `"]},"Action":["sts:AssumeRole"]}}`

	updated, changed, err := ensureAssumeRolePolicyAllowsPrincipal(url.QueryEscape(policy), testDeployRunnerRoleARN)
	require.NoError(t, err)
	require.False(t, changed)
	require.JSONEq(t, policy, updated)
}

func TestEnsureDeployRunnerAssumeRoleTrustUpdatesTenantRole(t *testing.T) {
	t.Parallel()

	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::902552026581:root"},"Action":"sts:AssumeRole"}]}`
	fake := &fakeIAM{getOut: &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: aws.String(policy)}}}

	srv := &Server{
		cfg: config.Config{ManagedProvisionRunnerRoleARN: testDeployRunnerRoleARN},
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
	require.Len(t, fake.getInputs, 1)
	require.Equal(t, "OrganizationAccountAccessRole", aws.ToString(fake.getInputs[0].RoleName))
	require.Len(t, fake.updateInputs, 1)
	require.Equal(t, "OrganizationAccountAccessRole", aws.ToString(fake.updateInputs[0].RoleName))
	require.Contains(t, aws.ToString(fake.updateInputs[0].PolicyDocument), testDeployRunnerRoleARN)
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

func TestEnsureDeployRunnerAssumeRoleTrustNoopsWhenAlreadyAllowed(t *testing.T) {
	t.Parallel()

	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + testDeployRunnerRoleARN + `"},"Action":"sts:AssumeRole"}]}`
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
}
