package hostedgenesis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestQueueMessageIsNonAuthoritativeAndSecretFree(t *testing.T) {
	msg := QueueMessage{
		Kind:           QueueMessageKind,
		Step:           StepAssistantTurn,
		RegistrationID: "reg_123",
		InstanceSlug:   "slug-one",
		AgentID:        "0xabc",
		ConversationID: "conv_123",
		TurnID:         "turn_123",
		RequestID:      "req_123",
		CorrelationID:  "corr_123",
		IdempotencyKey: "idem_123",
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal QueueMessage: %v", err)
	}
	text := strings.ToLower(string(body))
	for _, forbidden := range []string{
		"raw_transcript",
		"transcript",
		"prompt",
		"bearer",
		"instance_api_key",
		"provider_secret",
		"wallet_signature",
		"ssm_value",
		"microvm_endpoint_token",
		"shell_auth_token",
		"raw_response",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("QueueMessage JSON includes forbidden authority/secret marker %q: %s", forbidden, text)
		}
	}

	typ := reflect.TypeOf(QueueMessage{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, forbidden := range []string{"transcript", "prompt", "credential", "password", "private", "walletsignature", "ssmvalue", "endpointtoken", "rawresponse"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("QueueMessage field %s is secret/authority-bearing", typ.Field(i).Name)
			}
		}
	}
}
