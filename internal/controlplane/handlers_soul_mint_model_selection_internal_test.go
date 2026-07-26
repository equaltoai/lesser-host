package controlplane

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/ai/modelselection"
)

func TestHostedGenesisModelValidationAllowsAliasesAndOmission(t *testing.T) {
	for _, raw := range []string{`{"message":"advance"}`, `{"message":"advance","model":"gpt-5.6-luna"}`, `{"message":"advance","model":"claude-sonnet-5"}`} {
		var req soulMintConversationRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		if err := validateHostedGenesisModelRequest(req); err != nil {
			t.Fatalf("model request %s rejected: %v", raw, err)
		}
	}
}

func TestHostedGenesisModelValidationRejectsEmptyAndFreeFormInputAtStart(t *testing.T) {
	for _, raw := range []string{`{"message":"advance","model":""}`, `{"message":"advance","model":"gpt-5-mini"}`, `{"message":"advance","model":"openai:gpt-5-mini"}`} {
		var req soulMintConversationRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		appErr := validateHostedGenesisModelRequest(req)
		if appErr == nil || appErr.Code != appTheoryCodeBadRequest {
			t.Fatalf("model request %s error = %#v, want app.bad_request", raw, appErr)
		}
		for _, alias := range modelselection.ValidAliases() {
			if !strings.Contains(appErr.Message, alias) {
				t.Fatalf("model request %s error %q does not name valid alias %q", raw, appErr.Message, alias)
			}
		}
	}
}
