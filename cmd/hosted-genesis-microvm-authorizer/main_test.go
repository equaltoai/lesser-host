package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestAuthorizeFailsClosedWithoutLabAndHash(t *testing.T) {
	t.Parallel()
	event := events.APIGatewayV2CustomAuthorizerV2Request{Headers: map[string]string{"authorization": "Bearer token"}}
	if authorize(event, func(string) string { return "" }) {
		t.Fatal("expected authorizer to deny without lab context and hash")
	}
	if authorize(event, func(key string) string {
		if key == "STAGE" {
			return "live"
		}
		return tokenHash("token")
	}) {
		t.Fatal("expected authorizer to deny outside lab")
	}
}

func TestAuthorizeAcceptsOnlyHashedBearerToken(t *testing.T) {
	t.Parallel()
	event := events.APIGatewayV2CustomAuthorizerV2Request{Headers: map[string]string{"Authorization": "Bearer lab-token"}}
	getenv := func(key string) string {
		switch key {
		case "STAGE":
			return "lab"
		case "HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256":
			return "sha256:" + tokenHash("lab-token")
		default:
			return ""
		}
	}
	if !authorize(event, getenv) {
		t.Fatal("expected matching hashed lab bearer token to authorize")
	}
	event.Headers["Authorization"] = "Bearer wrong"
	if authorize(event, getenv) {
		t.Fatal("expected mismatched bearer token to deny")
	}
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
