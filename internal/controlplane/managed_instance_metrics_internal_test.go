package controlplane

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type fakeControlPlaneSecretsManager struct {
	out         *secretsmanager.GetSecretValueOutput
	err         error
	gotSecretID string
}

func (f *fakeControlPlaneSecretsManager) GetSecretValue(_ context.Context, params *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if params != nil {
		f.gotSecretID = aws.ToString(params.SecretId)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

type fakeControlPlaneSTS struct {
	out            *sts.AssumeRoleOutput
	err            error
	gotRoleArn     string
	gotSessionName string
}

func (f *fakeControlPlaneSTS) AssumeRole(_ context.Context, params *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	if params != nil {
		f.gotRoleArn = aws.ToString(params.RoleArn)
		f.gotSessionName = aws.ToString(params.RoleSessionName)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func TestManagedInstanceSecretRegion(t *testing.T) {
	t.Parallel()

	require.Equal(t, "us-west-2", managedInstanceSecretRegion(&models.Instance{HostedRegion: " us-west-2 "}, "us-east-1"))
	require.Equal(t, "eu-central-1", managedInstanceSecretRegion(&models.Instance{}, " eu-central-1 "))
	require.Equal(t, "us-east-1", managedInstanceSecretRegion(nil, ""))
}

func TestInstanceSecretFetchInputs(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{ManagedInstanceRoleName: " OrgRole ", ManagedDefaultRegion: " us-west-2 "}}
	inst := &models.Instance{
		LesserHostInstanceKeySecretARN: " arn:secret ",
		HostedAccountID:                " 123456789012 ",
	}

	secretArn, accountID, roleName, region, err := s.instanceSecretFetchInputs(inst)
	require.NoError(t, err)
	require.Equal(t, "arn:secret", secretArn)
	require.Equal(t, "123456789012", accountID)
	require.Equal(t, "OrgRole", roleName)
	require.Equal(t, "us-west-2", region)

	_, _, _, _, err = (*Server)(nil).instanceSecretFetchInputs(inst)
	require.ErrorContains(t, err, "server not initialized")
	_, _, _, _, err = s.instanceSecretFetchInputs(nil)
	require.ErrorContains(t, err, "instance is nil")
	_, _, _, _, err = s.instanceSecretFetchInputs(&models.Instance{})
	require.ErrorContains(t, err, "secret arn")
}

func TestUnwrapControlPlaneSecretString(t *testing.T) {
	t.Parallel()

	raw, err := unwrapControlPlaneSecretString(" raw-secret ")
	require.NoError(t, err)
	require.Equal(t, "raw-secret", raw)

	jsonSecret, err := unwrapControlPlaneSecretString(`{"secret":" json-secret "}`)
	require.NoError(t, err)
	require.Equal(t, "json-secret", jsonSecret)

	_, err = unwrapControlPlaneSecretString("")
	require.ErrorContains(t, err, "empty")
	_, err = unwrapControlPlaneSecretString(`{"secret":`)
	require.ErrorContains(t, err, "unmarshal")
	_, err = unwrapControlPlaneSecretString(`{"other":"value"}`)
	require.ErrorContains(t, err, "missing 'secret'")
}

func TestGetControlPlaneSecretPlaintext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fake := &fakeControlPlaneSecretsManager{out: &secretsmanager.GetSecretValueOutput{SecretString: aws.String(`{"secret":" key-from-json "}`)}}
	got, err := getControlPlaneSecretPlaintext(ctx, fake, " arn:secret ")
	require.NoError(t, err)
	require.Equal(t, "key-from-json", got)
	require.Equal(t, "arn:secret", fake.gotSecretID)

	binary := &fakeControlPlaneSecretsManager{out: &secretsmanager.GetSecretValueOutput{SecretBinary: []byte(" binary-key ")}}
	got, err = getControlPlaneSecretPlaintext(ctx, binary, "arn:binary")
	require.NoError(t, err)
	require.Equal(t, "binary-key", got)

	_, err = getControlPlaneSecretPlaintext(ctx, fake, "")
	require.ErrorContains(t, err, "secret arn")
	_, err = getControlPlaneSecretPlaintext(ctx, nil, "arn:secret")
	require.ErrorContains(t, err, "not initialized")
	_, err = getControlPlaneSecretPlaintext(ctx, &fakeControlPlaneSecretsManager{err: errors.New("boom")}, "arn:secret")
	require.ErrorContains(t, err, "get secret value")
}

func TestDefaultFetchInstanceKeyPlaintextSameAccountUsesInjectedSecrets(t *testing.T) {
	t.Parallel()

	fakeSecrets := &fakeControlPlaneSecretsManager{out: &secretsmanager.GetSecretValueOutput{SecretString: aws.String(" host-key ")}}
	s := &Server{cfg: config.Config{ManagedDefaultRegion: "us-east-2"}, sts: &fakeControlPlaneSTS{}, secrets: fakeSecrets}
	got, err := s.defaultFetchInstanceKeyPlaintext(context.Background(), &models.Instance{LesserHostInstanceKeySecretARN: "arn:secret"})
	require.NoError(t, err)
	require.Equal(t, "host-key", got)
	require.Equal(t, "arn:secret", fakeSecrets.gotSecretID)
}

func TestDefaultFetchInstanceKeyPlaintextCrossAccountAssumeRoleFailures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fakeSecrets := &fakeControlPlaneSecretsManager{out: &secretsmanager.GetSecretValueOutput{SecretString: aws.String("unused")}}
	assumeDenied := &fakeControlPlaneSTS{err: errors.New("denied")}
	s := &Server{
		cfg:     config.Config{Stage: "lab", ManagedInstanceRoleName: "OrgRole", ManagedDefaultRegion: "us-east-1"},
		sts:     assumeDenied,
		secrets: fakeSecrets,
	}
	inst := &models.Instance{
		Slug:                           "demo",
		HostedAccountID:                "123456789012",
		HostedRegion:                   "us-west-2",
		LesserHostInstanceKeySecretARN: "arn:secret",
	}

	_, err := s.defaultFetchInstanceKeyPlaintext(ctx, inst)
	require.ErrorContains(t, err, "assume instance role")
	require.Equal(t, "arn:aws:iam::123456789012:role/OrgRole", assumeDenied.gotRoleArn)
	require.Equal(t, "lesser-host-lab-cost-demo", assumeDenied.gotSessionName)

	emptyAssume := &fakeControlPlaneSTS{out: &sts.AssumeRoleOutput{}}
	s.sts = emptyAssume
	_, err = s.defaultFetchInstanceKeyPlaintext(ctx, inst)
	require.ErrorContains(t, err, "empty credentials")
}

func TestBuildPortalCostResponseFromLesserUsesCentsAndSkipsEmptyDates(t *testing.T) {
	t.Parallel()

	resp := buildPortalCostResponseFromLesser("demo", testCostDate1, testCostDate2, lesserInstanceMetricsResponse{Daily: []lesserInstanceMetricsDailyRow{
		{CostDollars: 99, Currency: "USD"},
		{Date: testCostDate2, CostCents: 34, TotalRequests: 2},
	}})

	require.Equal(t, 1, resp.Count)
	require.Len(t, resp.Days, 1)
	require.Equal(t, testCostDate2, resp.Days[0].Date)
	require.InDelta(t, 0.34, resp.Days[0].DayCost, 0.000001)
	require.Equal(t, "USD", resp.Currency)
}

func TestParseCostDateWindowDefaults(t *testing.T) {
	t.Parallel()

	from, to, appErr := parseCostDateWindow(newCostHandlerCtx(testCostSlug1, nil))
	require.Nil(t, appErr)
	fromTime, err := time.Parse("2006-01-02", from)
	require.NoError(t, err)
	toTime, err := time.Parse("2006-01-02", to)
	require.NoError(t, err)
	require.Equal(t, costQueryDefaultDays, int(toTime.Sub(fromTime).Hours()/24))
}

func TestBuildManagedInstanceMetricsURLRequiresBaseURL(t *testing.T) {
	t.Parallel()

	_, err := buildManagedInstanceMetricsURL(" ", testCostDate1, testCostDate2)
	require.ErrorContains(t, err, "base url")
}

func TestInstanceMetricsAWSClientsUsesInjectedClients(t *testing.T) {
	t.Parallel()

	fakeSecrets := &fakeControlPlaneSecretsManager{out: &secretsmanager.GetSecretValueOutput{SecretString: aws.String("key")}}
	s := &Server{sts: &fakeControlPlaneSTS{}, secrets: fakeSecrets}
	stsClient, secretsClient, err := s.instanceMetricsAWSClients(context.Background())
	require.NoError(t, err)
	require.NotNil(t, stsClient)
	require.Same(t, fakeSecrets, secretsClient)
}

func TestDefaultInstanceMetricsBaseURLValidation(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "lab"}}
	_, err := s.defaultInstanceMetricsBaseURL(nil)
	require.ErrorContains(t, err, "instance is nil")
	_, err = s.defaultInstanceMetricsBaseURL(&models.Instance{})
	require.ErrorContains(t, err, "hosted base domain")
}

func TestResolvePortalCostInstanceKeyUsesInjectedResolver(t *testing.T) {
	t.Parallel()

	s := &Server{fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
		return "injected-key", nil
	}}
	got, err := s.resolvePortalCostInstanceKey(context.Background(), &models.Instance{})
	require.NoError(t, err)
	require.Equal(t, "injected-key", got)

	_, err = (*Server)(nil).resolvePortalCostInstanceKey(context.Background(), &models.Instance{})
	require.ErrorContains(t, err, "server not initialized")
}

func TestResolvePortalCostMetricsBaseURLUsesInjectedResolver(t *testing.T) {
	t.Parallel()

	s := &Server{resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
		return "https://metrics.example", nil
	}}
	got, err := s.resolvePortalCostMetricsBaseURL(&models.Instance{})
	require.NoError(t, err)
	require.Equal(t, "https://metrics.example", got)
}

func TestFetchManagedInstanceMetricsAdditionalFailures(t *testing.T) {
	t.Parallel()

	s := &Server{resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
		return "https://metrics.example", nil
	}}
	_, appErr := s.fetchManagedInstanceMetrics(context.Background(), &models.Instance{}, " ", testCostDate1, testCostDate2)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	s.resolveInstanceMetricsBaseURLFunc = func(*models.Instance) (string, error) { return "://bad", nil }
	_, appErr = s.fetchManagedInstanceMetrics(context.Background(), &models.Instance{}, testRawKey, testCostDate1, testCostDate2)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestFetchManagedInstanceMetricsDecodeError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer ts.Close()

	s := &Server{
		portalCostHTTPClient: ts.Client(),
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}
	_, appErr := s.fetchManagedInstanceMetrics(context.Background(), &models.Instance{}, testRawKey, testCostDate1, testCostDate2)
	require.NotNil(t, appErr)
	require.Equal(t, "app.upstream_error", appErr.Code)
	require.NotContains(t, appErr.Message, testRawKey)
}
