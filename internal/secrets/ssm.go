package secrets

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// SSMAPI is the subset of the AWS SSM client used by this package.
type SSMAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

var (
	defaultSSMOnce   sync.Once
	defaultSSMClient SSMAPI
	defaultSSMErr    error
)

type cachedParam struct {
	Value     string
	ExpiresAt time.Time
}

var paramCache sync.Map // map[name]cachedParam

// GetSSMParameter loads and returns a decrypted parameter value from SSM.
func GetSSMParameter(ctx context.Context, client SSMAPI, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("parameter name is required")
	}

	if client == nil {
		c, err := defaultClient(ctx)
		if err != nil {
			return "", err
		}
		client = c
	}

	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           &name,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("ssm get parameter %q: %w", name, err)
	}
	if out == nil || out.Parameter == nil {
		return "", fmt.Errorf("ssm get parameter %q: empty response", name)
	}
	value := strings.TrimSpace(aws.ToString(out.Parameter.Value))
	if value == "" {
		return "", fmt.Errorf("ssm parameter %q is empty", name)
	}
	return value, nil
}

// GetSSMParameterCached loads a parameter from SSM and caches it for a TTL.
func GetSSMParameterCached(ctx context.Context, client SSMAPI, name string, ttl time.Duration) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("parameter name is required")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	if v, ok := paramCache.Load(name); ok {
		cp, ok := v.(cachedParam)
		if ok && strings.TrimSpace(cp.Value) != "" && time.Now().Before(cp.ExpiresAt) {
			return cp.Value, nil
		}
	}

	value, err := GetSSMParameter(ctx, client, name)
	if err != nil {
		return "", err
	}
	paramCache.Store(name, cachedParam{Value: value, ExpiresAt: time.Now().Add(ttl)})
	return value, nil
}

func defaultClient(ctx context.Context) (SSMAPI, error) {
	defaultSSMOnce.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			defaultSSMErr = err
			return
		}
		defaultSSMClient = ssm.NewFromConfig(cfg)
	})
	if defaultSSMErr != nil {
		return nil, defaultSSMErr
	}
	if defaultSSMClient == nil {
		return nil, fmt.Errorf("ssm client not initialized")
	}
	return defaultSSMClient, nil
}
