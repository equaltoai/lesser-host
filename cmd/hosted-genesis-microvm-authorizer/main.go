package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/observability"
)

const serviceName = "hosted-genesis-microvm-authorizer"

var authorizerApp = apptheory.New(apptheory.WithObservability(observability.New(serviceName)))

func main() {
	if authorizerApp == nil {
		panic("hosted genesis microvm authorizer observability not initialized")
	}
	lambda.Start(handle)
}

func handle(ctx context.Context, event events.APIGatewayV2CustomAuthorizerV2Request) (events.APIGatewayV2CustomAuthorizerSimpleResponse, error) {
	_ = ctx
	allowed := authorize(event, os.Getenv)
	scope := "hosted-genesis-microvm"
	if stage := authorizedStage(os.Getenv("STAGE")); stage != "" {
		scope += "-" + stage
	}
	return events.APIGatewayV2CustomAuthorizerSimpleResponse{
		IsAuthorized: allowed,
		Context: map[string]interface{}{
			"authenticated": allowed,
			"scope":         scope,
		},
	}, nil
}

func authorize(event events.APIGatewayV2CustomAuthorizerV2Request, getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	if authorizedStage(getenv("STAGE")) == "" {
		return false
	}
	expected := normalizeSHA256(getenv("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256"))
	if expected == "" {
		return false
	}
	authorization := headerValue(event.Headers, "authorization")
	if authorization == "" {
		authorization = headerValue(event.Headers, "Authorization")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if token == "" || token == authorization {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	got := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func authorizedStage(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "lab":
		return "lab"
	case "live":
		return "live"
	default:
		return ""
	}
}

func normalizeSHA256(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return ""
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return value
}

func headerValue(headers map[string]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
