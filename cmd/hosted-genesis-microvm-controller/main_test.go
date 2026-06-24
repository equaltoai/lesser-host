package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

func TestControllerEventFailsClosedWhenDisabled(t *testing.T) {
	t.Parallel()
	event := events.APIGatewayV2HTTPRequest{RequestContext: events.APIGatewayV2HTTPRequestContext{RequestID: "req-1"}}
	resp, err := handleControllerEvent(context.Background(), event, func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if strings.Contains(strings.ToLower(resp.Body), "token") || strings.Contains(strings.ToLower(resp.Body), "authorization") {
		t.Fatalf("fail-closed response leaked auth vocabulary: %s", resp.Body)
	}
}

func TestControllerRequestFromEventMapsAppTheoryRoutes(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(runtimemicrovm.ControllerRequest{RequestID: "req-body", TenantID: "slug:demo", Namespace: "hosted-genesis", AuthContext: runtimemicrovm.AuthContext{Subject: "subject", TenantID: "slug:demo"}})
	req, err := controllerRequestFromEvent(events.APIGatewayV2HTTPRequest{
		RouteKey:       "POST /microvms/{session_id}/start",
		RawPath:        "/microvms/conv_123/start",
		PathParameters: map[string]string{"session_id": "conv_123"},
		Body:           string(body),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Command != runtimemicrovm.CommandStart || req.SessionID != "conv_123" || req.RequestID != "req-body" {
		t.Fatalf("unexpected mapped request: %#v", req)
	}
}
