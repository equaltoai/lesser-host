package controlplane

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func TestParseSetupBootstrapVerifyInput(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, err := parseSetupBootstrapVerifyInput(ctx); err == nil {
		t.Fatalf("expected error")
	}

	// Accept legacy snake_case challenge id and "challenge" field for message.
	ctx.Request.Body = []byte(`{"challenge_id":"c","address":"a","signature":"s","challenge":"m"}`)
	got, err := parseSetupBootstrapVerifyInput(ctx)
	if err != nil {
		t.Fatalf("parseSetupBootstrapVerifyInput: %v", err)
	}
	if got.ChallengeID != "c" || got.Message != "m" {
		t.Fatalf("unexpected parsed input: %#v", got)
	}
}

func TestParseSetupCreateAdminRequestInput(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, appErr := parseSetupCreateAdminRequestInput(ctx); appErr == nil {
		t.Fatalf("expected error")
	}

	ctx.Request.Body = []byte(`{"username":"bootstrap","wallet":{"challengeId":"c","address":"a","signature":"s","message":"m"}}`)
	if _, appErr := parseSetupCreateAdminRequestInput(ctx); appErr == nil {
		t.Fatalf("expected reserved username error")
	}

	ctx.Request.Body = []byte(`{"username":" alice ","displayName":" Alice ","wallet":{"challengeId":" c ","address":" a ","signature":" s ","message":" m "}}`)
	req, appErr := parseSetupCreateAdminRequestInput(ctx)
	if appErr != nil {
		t.Fatalf("parseSetupCreateAdminRequestInput: %v", appErr)
	}
	if req.Username != "alice" || req.DisplayName != "Alice" || req.Wallet.ChallengeID != "c" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

