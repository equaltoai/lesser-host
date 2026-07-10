package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestAuthorizeFailsClosedWithoutCanonicalStageAndHash(t *testing.T) {
	t.Parallel()
	event := events.APIGatewayV2CustomAuthorizerV2Request{Headers: map[string]string{"authorization": "Bearer token"}}
	if authorize(event, func(string) string { return "" }) {
		t.Fatal("expected authorizer to deny without canonical stage and hash")
	}
	if authorize(event, func(key string) string {
		if key == "STAGE" {
			return "dev"
		}
		return tokenHash("token")
	}) {
		t.Fatal("expected authorizer to deny outside canonical deploy stages")
	}
}

func TestAuthorizeAcceptsOnlyHashedBearerTokenInCanonicalStages(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"lab", "live"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			event := events.APIGatewayV2CustomAuthorizerV2Request{Headers: map[string]string{"Authorization": "Bearer stage-token"}}
			getenv := func(key string) string {
				switch key {
				case "STAGE":
					return stage
				case "HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256":
					return "sha256:" + tokenHash("stage-token")
				default:
					return ""
				}
			}
			if !authorize(event, getenv) {
				t.Fatalf("expected matching hashed %s bearer token to authorize", stage)
			}
			event.Headers["Authorization"] = "Bearer wrong"
			if authorize(event, getenv) {
				t.Fatalf("expected mismatched %s bearer token to deny", stage)
			}
		})
	}
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
