package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCLI_PassesRedactedM3Evidence(t *testing.T) {
	path := writeCanaryFixture(t, validCanaryEvidenceJSON())
	var stdout bytes.Buffer
	code := runCLI([]string{
		"--stage", "lab",
		"--evidence", path,
		"--require-legacy-alias",
		"--require-body-mcp",
		"--require-unknown-alias",
	}, func(string) string { return "" }, &stdout, &bytes.Buffer{}, time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC))
	if code != 0 {
		t.Fatalf("expected pass, got exit %d stdout=%s", code, stdout.String())
	}
	var report canaryValidationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.AgentsChecked != 1 || report.PrimaryChecked != 1 || report.LegacyChecked != 1 || report.BodyMCPChecked != 1 || !report.UnknownAliasChecked {
		t.Fatalf("unexpected report counts: %#v", report)
	}
	if len(report.Issues) != 0 {
		t.Fatalf("unexpected issues: %#v", report.Issues)
	}
}

func TestRunCLI_FailsWhenLegacyAliasIsAdvertised(t *testing.T) {
	fixture := strings.Replace(validCanaryEvidenceJSON(), `"legacy_address_advertised": false`, `"legacy_address_advertised": true`, 1)
	path := writeCanaryFixture(t, fixture)
	var stdout bytes.Buffer
	code := runCLI([]string{"--stage", "lab", "--evidence", path, "--require-legacy-alias"}, func(string) string { return "" }, &stdout, &bytes.Buffer{}, time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC))
	if code != 2 {
		t.Fatalf("expected validation failure exit 2, got %d stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "legacy_address_advertised") {
		t.Fatalf("expected legacy advertisement issue, got %s", stdout.String())
	}
}

func TestRunCLI_FailsOnUnredactedBodyField(t *testing.T) {
	fixture := strings.Replace(validCanaryEvidenceJSON(), `"content_redacted": true`, `"content_redacted": true, "body": "secret message content"`, 1)
	path := writeCanaryFixture(t, fixture)
	var stdout bytes.Buffer
	code := runCLI([]string{"--stage", "lab", "--evidence", path}, func(string) string { return "" }, &stdout, &bytes.Buffer{}, time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC))
	if code != 2 {
		t.Fatalf("expected redaction failure exit 2, got %d stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "redaction") || !strings.Contains(stdout.String(), "body") {
		t.Fatalf("expected body redaction issue, got %s", stdout.String())
	}
}

func TestRunCLI_FailsOnCanonicalAddressMismatch(t *testing.T) {
	fixture := strings.Replace(validCanaryEvidenceJSON(), `"pilot.simulacrum@lessersoul.ai"`, `"pilot@lessersoul.ai"`, 1)
	path := writeCanaryFixture(t, fixture)
	var stdout bytes.Buffer
	code := runCLI([]string{"--stage", "lab", "--evidence", path}, func(string) string { return "" }, &stdout, &bytes.Buffer{}, time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC))
	if code != 2 {
		t.Fatalf("expected validation failure exit 2, got %d stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "canonical_address") {
		t.Fatalf("expected canonical address issue, got %s", stdout.String())
	}
}

func writeCanaryFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m3-canary.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func validCanaryEvidenceJSON() string {
	return `{
  "schema_version": 1,
  "generated_at": "2026-05-22T14:30:00Z",
  "stage": "lab",
  "table_name": "lesser-host-lab-state",
  "mode": "redacted-canary",
  "contract": {
    "canonical_address_format": "<agent-local-id>.<instance-slug>@lessersoul.ai",
    "legacy_alias_policy": "host-internal aliases canonicalize before comm-worker channel matching",
    "redaction_policy": "message bodies, provider payloads, and credentials omitted"
  },
  "agents": [
    {
      "agent_id": "0xabc123",
      "local_id": "pilot",
      "instance_slug": "simulacrum",
      "canonical_address": "pilot.simulacrum@lessersoul.ai",
      "legacy_alias": "pilot@lessersoul.ai",
      "primary": {
        "inbound": {
          "passed": true,
          "recipient": "pilot.simulacrum@lessersoul.ai",
          "mailbox_to_address": "pilot.simulacrum@lessersoul.ai",
          "status": "accepted",
          "message_ref": "redacted-primary-message",
          "delivery_id": "comm-delivery-redacted-primary"
        },
        "outbound": {
          "passed": true,
          "sender": "pilot.simulacrum@lessersoul.ai",
          "status": "sent",
          "message_ref": "redacted-outbound-message",
          "delivery_id": "comm-delivery-redacted-outbound"
        },
        "mailbox": {
          "list": true,
          "get": true,
          "content": true,
          "search": true,
          "content_redacted": true,
          "to_address": "pilot.simulacrum@lessersoul.ai"
        },
        "resolve": {
          "status": "ok",
          "agent_id": "0xabc123",
          "address": "pilot.simulacrum@lessersoul.ai"
        },
        "contactability": {
          "passed": true,
          "address": "pilot.simulacrum@lessersoul.ai",
          "status": "ok"
        }
      },
      "legacy": {
        "inbound": {
          "passed": true,
          "recipient": "pilot@lessersoul.ai",
          "mailbox_to_address": "pilot.simulacrum@lessersoul.ai",
          "canonicalized_to": "pilot.simulacrum@lessersoul.ai",
          "status": "accepted",
          "message_ref": "redacted-legacy-message",
          "delivery_id": "comm-delivery-redacted-legacy"
        },
        "canonical_mailbox": {
          "passed": true,
          "mailbox_to_address": "pilot.simulacrum@lessersoul.ai",
          "canonicalized_to": "pilot.simulacrum@lessersoul.ai"
        },
        "non_advertisement": {
          "passed": true,
          "public_email_address": "pilot.simulacrum@lessersoul.ai",
          "public_email_addresses": ["pilot.simulacrum@lessersoul.ai"],
          "legacy_address_advertised": false
        },
        "resolve": {
          "status": "not_found"
        }
      },
      "body_mcp": {
        "identity_whoami": true,
        "identity_lookup": true,
        "identity_whoami_email": "pilot.simulacrum@lessersoul.ai",
        "identity_lookup_email": "pilot.simulacrum@lessersoul.ai",
        "email_send": true,
        "email_reply": true,
        "email_read": true,
        "email_get": true,
        "email_get_content": true,
        "email_search": true,
        "legacy_alias_inbound": true
      }
    }
  ],
  "unknown_alias": {
    "address": "unknown@lessersoul.ai",
    "inbound_status": "dropped",
    "resolve_status": "not_found"
  }
}`
}
