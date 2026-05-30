package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/outboundhttp"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const instanceMetricsTimeout = 10 * time.Second

type controlPlaneSTSAPI interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

type controlPlaneSecretsManagerAPI interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

type lesserInstanceMetricsResponse struct {
	Period struct {
		Start    string `json:"start"`
		End      string `json:"end"`
		Days     int    `json:"days"`
		Timezone string `json:"timezone"`
	} `json:"period"`
	Daily []lesserInstanceMetricsDailyRow `json:"daily"`
}

type lesserInstanceMetricsDailyRow struct {
	Date             string  `json:"date"`
	TotalRequests    int64   `json:"total_requests"`
	UniqueUsers      int64   `json:"unique_users"`
	DynamoDBReads    int64   `json:"dynamodb_reads"`
	DynamoDBWrites   int64   `json:"dynamodb_writes"`
	LambdaDurationMs int64   `json:"lambda_duration_ms"`
	CostCents        int64   `json:"cost_cents"`
	CostDollars      float64 `json:"cost_dollars"`
	Currency         string  `json:"currency"`
}

func (s *Server) resolvePortalCostInstanceKey(ctx context.Context, inst *models.Instance) (string, error) {
	if s != nil && s.fetchInstanceKeyPlaintextFunc != nil {
		return s.fetchInstanceKeyPlaintextFunc(ctx, inst)
	}
	return s.defaultFetchInstanceKeyPlaintext(ctx, inst)
}

func (s *Server) portalManagedHTTPClient() *http.Client {
	if s != nil && s.portalCostHTTPClient != nil {
		return s.portalCostHTTPClient
	}
	return outboundhttp.NewSSRFProtectedClient(nil, outboundhttp.WithTimeout(instanceMetricsTimeout))
}

func (s *Server) fetchManagedInstanceMetrics(ctx context.Context, inst *models.Instance, apiKey string, from string, to string) (lesserInstanceMetricsResponse, *apptheory.AppError) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return lesserInstanceMetricsResponse{}, &apptheory.AppError{Code: "app.internal", Message: "failed to resolve instance metrics access"}
	}

	baseURL, err := s.resolvePortalCostMetricsBaseURL(inst)
	if err != nil {
		return lesserInstanceMetricsResponse{}, &apptheory.AppError{Code: "app.internal", Message: "failed to resolve instance metrics endpoint"}
	}

	endpoint, err := buildManagedInstanceMetricsURL(baseURL, from, to)
	if err != nil {
		return lesserInstanceMetricsResponse{}, &apptheory.AppError{Code: "app.internal", Message: "failed to resolve instance metrics endpoint"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return lesserInstanceMetricsResponse{}, &apptheory.AppError{Code: "app.internal", Message: "failed to create instance metrics request"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	client := s.portalManagedHTTPClient()

	resp, err := client.Do(req) //nolint:gosec // URL is derived from managed instance metadata or an injected test seam, not from browser input.
	if err != nil {
		return lesserInstanceMetricsResponse{}, &apptheory.AppError{Code: "app.upstream_unavailable", Message: "failed to reach instance metrics"}
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return lesserInstanceMetricsResponse{}, &apptheory.AppError{Code: "app.upstream_error", Message: "failed to fetch instance metrics"}
	}

	var decoded lesserInstanceMetricsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return lesserInstanceMetricsResponse{}, &apptheory.AppError{Code: "app.upstream_error", Message: "failed to decode instance metrics"}
	}
	if decoded.Daily == nil {
		decoded.Daily = []lesserInstanceMetricsDailyRow{}
	}
	return decoded, nil
}

func (s *Server) resolvePortalCostMetricsBaseURL(inst *models.Instance) (string, error) {
	if s != nil && s.resolveInstanceMetricsBaseURLFunc != nil {
		return s.resolveInstanceMetricsBaseURLFunc(inst)
	}
	return s.defaultInstanceMetricsBaseURL(inst)
}

func (s *Server) defaultInstanceMetricsBaseURL(inst *models.Instance) (string, error) {
	if inst == nil {
		return "", fmt.Errorf("instance is nil")
	}
	baseDomain := strings.TrimSpace(inst.HostedBaseDomain)
	if baseDomain == "" {
		return "", fmt.Errorf("hosted base domain is not configured")
	}
	stage := ""
	if s != nil {
		stage = s.cfg.Stage
	}
	stageDomain := managedInstanceStageDomain(stage, baseDomain)
	if strings.TrimSpace(stageDomain) == "" {
		return "", fmt.Errorf("managed instance domain is not configured")
	}
	return "https://api." + strings.TrimSpace(stageDomain), nil
}

func buildManagedInstanceMetricsURL(baseURL string, from string, to string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("base url is required")
	}
	u, err := url.Parse(baseURL + "/api/v1/instance/metrics/daily")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("from", strings.TrimSpace(from))
	q.Set("to", strings.TrimSpace(to))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *Server) defaultFetchInstanceKeyPlaintext(ctx context.Context, inst *models.Instance) (string, error) {
	secretArn, accountID, roleName, region, err := s.instanceSecretFetchInputs(inst)
	if err != nil {
		return "", err
	}

	stsClient, secretsClient, err := s.instanceMetricsAWSClients(ctx)
	if err != nil {
		return "", err
	}
	if accountID == "" || roleName == "" || stsClient == nil {
		return getControlPlaneSecretPlaintext(ctx, secretsClient, secretArn)
	}

	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)
	sessionName := fmt.Sprintf("lesser-host-%s-cost-%s", strings.TrimSpace(s.cfg.Stage), strings.TrimSpace(inst.Slug))
	if len(sessionName) > 64 {
		sessionName = sessionName[:64]
	}

	assumed, err := stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String(sessionName),
		DurationSeconds: aws.Int32(900),
	})
	if err != nil {
		return "", fmt.Errorf("assume instance role: %w", err)
	}
	if assumed == nil || assumed.Credentials == nil {
		return "", fmt.Errorf("assume role returned empty credentials")
	}

	creds := credentials.NewStaticCredentialsProvider(
		aws.ToString(assumed.Credentials.AccessKeyId),
		aws.ToString(assumed.Credentials.SecretAccessKey),
		aws.ToString(assumed.Credentials.SessionToken),
	)
	child := secretsmanager.New(secretsmanager.Options{
		Region:      region,
		Credentials: aws.NewCredentialsCache(creds),
	})

	return getControlPlaneSecretPlaintext(ctx, child, secretArn)
}

func (s *Server) instanceMetricsAWSClients(ctx context.Context) (controlPlaneSTSAPI, controlPlaneSecretsManagerAPI, error) {
	if s != nil && s.sts != nil && s.secrets != nil {
		return s.sts, s.secrets, nil
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, nil, err
	}

	baseSTS := sts.NewFromConfig(awsCfg)
	stsClient := controlPlaneSTSAPI(baseSTS)
	if s != nil && strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN) != "" {
		roleArn := strings.TrimSpace(s.cfg.ManagedOrgVendingRoleARN)
		provider := stscreds.NewAssumeRoleProvider(baseSTS, roleArn, func(o *stscreds.AssumeRoleOptions) {
			sessionName := fmt.Sprintf("lesser-host-%s-cost-vending", strings.TrimSpace(s.cfg.Stage))
			if len(sessionName) > 64 {
				sessionName = sessionName[:64]
			}
			o.RoleSessionName = sessionName
			o.Duration = 3600 * time.Second
		})
		mgmtCfg := awsCfg
		mgmtCfg.Credentials = aws.NewCredentialsCache(provider)
		stsClient = sts.NewFromConfig(mgmtCfg)
	}

	return stsClient, secretsmanager.NewFromConfig(awsCfg), nil
}

func (s *Server) instanceSecretFetchInputs(inst *models.Instance) (secretArn string, accountID string, roleName string, region string, err error) {
	if s == nil {
		return "", "", "", "", fmt.Errorf("server not initialized")
	}
	if inst == nil {
		return "", "", "", "", fmt.Errorf("instance is nil")
	}
	secretArn = strings.TrimSpace(inst.LesserHostInstanceKeySecretARN)
	if secretArn == "" {
		return "", "", "", "", fmt.Errorf("instance api key secret arn is not configured")
	}
	accountID = strings.TrimSpace(inst.HostedAccountID)
	roleName = strings.TrimSpace(s.cfg.ManagedInstanceRoleName)
	region = managedInstanceSecretRegion(inst, s.cfg.ManagedDefaultRegion)
	return secretArn, accountID, roleName, region, nil
}

func managedInstanceSecretRegion(inst *models.Instance, fallback string) string {
	region := ""
	if inst != nil {
		region = strings.TrimSpace(inst.HostedRegion)
	}
	if region != "" {
		return region
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	return "us-east-1"
}

func getControlPlaneSecretPlaintext(ctx context.Context, sm controlPlaneSecretsManagerAPI, secretArn string) (string, error) {
	secretArn = strings.TrimSpace(secretArn)
	if secretArn == "" {
		return "", fmt.Errorf("secret arn is required")
	}
	if sm == nil {
		return "", fmt.Errorf("secrets manager client not initialized")
	}

	out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretArn)})
	if err != nil {
		return "", fmt.Errorf("get secret value: %w", err)
	}

	raw := strings.TrimSpace(aws.ToString(out.SecretString))
	if raw == "" && len(out.SecretBinary) > 0 {
		raw = strings.TrimSpace(string(out.SecretBinary))
	}
	plaintext, err := unwrapControlPlaneSecretString(raw)
	if err != nil {
		return "", fmt.Errorf("parse secret value: %w", err)
	}
	return plaintext, nil
}

func unwrapControlPlaneSecretString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("secret value is empty")
	}
	if strings.HasPrefix(raw, "{") {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return "", fmt.Errorf("unmarshal secret string: %w", err)
		}
		val := strings.TrimSpace(parsed["secret"])
		if val == "" {
			return "", fmt.Errorf("secret payload missing 'secret' key")
		}
		return val, nil
	}
	return raw, nil
}
