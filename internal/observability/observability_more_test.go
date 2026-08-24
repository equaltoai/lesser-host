package observability

import (
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

func TestObservabilityHelpers_Coverage(t *testing.T) {
	t.Setenv("STAGE", "prod")

	emitTrustProxy503BestEffort("control-plane-api", apptheory.LogRecord{Status: 503})
	emitTrustProxy503BestEffort("trust-api", apptheory.LogRecord{
		Status:   503,
		TenantID: "inst-1",
		Method:   "GET",
		Path:     "/attestations",
	})

	emitCommWebhookMetricsBestEffort("trust-api", apptheory.LogRecord{Path: "/webhooks/comm/email/inbound", Status: 500})
	emitCommWebhookMetricsBestEffort("control-plane-api", apptheory.LogRecord{Path: "/not-a-webhook", Status: 500})
	emitCommWebhookMetricsBestEffort("control-plane-api", apptheory.LogRecord{Path: "/webhooks/comm/email/inbound", Status: 202})
	emitCommWebhookMetricsBestEffort("control-plane-api", apptheory.LogRecord{Path: "/webhooks/comm/sms/inbound", Status: 422})
	emitCommWebhookMetricsBestEffort("control-plane-api", apptheory.LogRecord{Path: "/webhooks/comm/voice/inbound", Status: 503})
	emitCommWebhookMetricsBestEffort("control-plane-api", apptheory.LogRecord{Path: "/webhooks/comm/voice/status", Status: 503})
	emitCommWebhookMetricsBestEffort("control-plane-api", apptheory.LogRecord{Path: "/webhooks/comm/other", Status: 503})
}

func TestSanitizeLogPathRedactsPrivateMintConversationIDs(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in             string
		rawSecret      string
		wantPrefix     string
		wantSuffixGone string
	}{
		"host instance single read": {
			in:             "/api/v1/soul/instance/agents/0xabc/mint-conversations/conv-private-1?cursor=raw-cursor",
			rawSecret:      "conv-private-1",
			wantPrefix:     "/api/v1/soul/instance/agents/0xabc/mint-conversations/conversation-sha256:",
			wantSuffixGone: "raw-cursor",
		},
		"lesser self scope proxy": {
			in:             "/api/v1/souls/bound/me/mint-conversations/lesser-private-conv#frag",
			rawSecret:      "lesser-private-conv",
			wantPrefix:     "/api/v1/souls/bound/me/mint-conversations/conversation-sha256:",
			wantSuffixGone: "frag",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := SanitizeLogPath(tc.in)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("expected sanitized path prefix %q, got %q", tc.wantPrefix, got)
			}
			if strings.Contains(got, tc.rawSecret) || strings.Contains(got, tc.wantSuffixGone) {
				t.Fatalf("sanitized path leaked private path/query data: %q", got)
			}
		})
	}
}

func TestSanitizeLogPathLeavesOtherRoutesUnchanged(t *testing.T) {
	t.Parallel()

	path := "/api/v1/soul/instance/agents/0xabc/mint-conversations?limit=20"
	if got := SanitizeLogPath(path); got != path {
		t.Fatalf("expected list path unchanged, got %q", got)
	}
}

func TestSanitizeMetricTagsRedactsPathWithoutMutatingInput(t *testing.T) {
	t.Parallel()

	rawPath := "/api/v1/soul/instance/agents/0xabc/mint-conversations/conv-private-1?cursor=raw-cursor"
	tags := map[string]string{
		"method": "GET",
		"path":   rawPath,
		"status": "200",
	}

	got := SanitizeMetricTags(tags)
	if got == nil {
		t.Fatal("expected sanitized tags")
	}
	if got["method"] != "GET" || got["status"] != "200" {
		t.Fatalf("expected non-path tags preserved, got %#v", got)
	}
	if !strings.HasPrefix(got["path"], "/api/v1/soul/instance/agents/0xabc/mint-conversations/conversation-sha256:") {
		t.Fatalf("expected sanitized metric path, got %q", got["path"])
	}
	if strings.Contains(got["path"], "conv-private-1") || strings.Contains(got["path"], "raw-cursor") {
		t.Fatalf("sanitized metric path leaked private path/query data: %q", got["path"])
	}
	if tags["path"] != rawPath {
		t.Fatalf("expected input tags to remain unchanged, got %q", tags["path"])
	}
}

func TestSanitizeMetricTagsLeavesPathlessTagsUnchanged(t *testing.T) {
	t.Parallel()

	tags := map[string]string{"method": "GET", "status": "200"}
	got := SanitizeMetricTags(tags)
	if got["method"] != "GET" || got["status"] != "200" {
		t.Fatalf("expected pathless tags preserved, got %#v", got)
	}
	if _, ok := got["path"]; ok {
		t.Fatalf("expected no path tag, got %#v", got)
	}
}

func TestCommWebhookProviderAndChannel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path     string
		provider string
		channel  string
	}{
		{path: "/webhooks/comm/email/inbound", provider: "migadu", channel: "email"},
		{path: "/webhooks/comm/sms/inbound", provider: "telnyx", channel: "sms"},
		{path: "/webhooks/comm/voice/inbound", provider: "telnyx", channel: "voice"},
		{path: "/webhooks/comm/voice/status", provider: "telnyx", channel: "voice"},
		{path: " /unknown ", provider: "", channel: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			provider, channel := commWebhookProviderAndChannel(tc.path)
			if provider != tc.provider || channel != tc.channel {
				t.Fatalf("got (%q,%q), want (%q,%q)", provider, channel, tc.provider, tc.channel)
			}
		})
	}
}
