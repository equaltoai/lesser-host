# Instance-scoped Lesser Soul email — M3 canary evidence

Project 37 M3 proves that managed Lesser Soul email now behaves as an
instance-scoped address contract end-to-end, without treating legacy bare
addresses as public/current channels.

This document defines the redacted evidence shape consumed by:

```bash
go run ./scripts/soul-email-m3-canary \
  --stage lab \
  --evidence gov-infra/evidence/project37/m3-email-canary-lab.json \
  --require-legacy-alias \
  --require-body-mcp \
  --require-unknown-alias \
  --out gov-infra/evidence/project37/m3-email-canary-lab-validation.json
```

The verifier is local and read-only. It does not call Host, Lesser, Body,
Migadu, SES, or any provider. Operators first run the actual lab/dev canaries
with authenticated tooling, then record only redacted facts in the evidence JSON
below and validate those facts with the script.

## M3 acceptance covered

For each checked agent, the evidence must prove:

- the current public address is `<agent-local-id>.<instance-slug>@lessersoul.ai`;
- primary-address inbound delivery reaches the agent mailbox;
- outbound email uses the instance-scoped address as sender;
- mailbox list/get/content/search paths expose the canonical address and redact
  message content;
- resolve/contactability behavior is unchanged except for the address value;
- if a legacy bare alias exists, inbound mail sent to it reaches the same
  canonical mailbox after host-internal canonicalization;
- public channel discovery does **not** advertise the legacy bare address;
- resolving a legacy bare address fails closed as a public/current contact
  lookup; and
- when `--require-body-mcp` is used, lesser-body identity and mailbox tools work
  against the same canonical address without exposing the legacy alias as the
  current channel.

The optional top-level `unknown_alias` check records that an unmigrated bare
address fails closed for inbound and/or resolve behavior. Use
`--require-unknown-alias` for release-gate evidence.

## Evidence redaction envelope

Evidence must not include message bodies, raw MIME, provider payloads, bearer
tokens, passwords, API secrets, authorization headers, or raw SSM values. The
verifier rejects fields whose names indicate those values. Message IDs,
delivery IDs, subjects, and provider detail may be replaced with stable redacted
placeholders when needed for traceability.

Allowed address fields are the public/current canonical address and the legacy
bare alias under test. Do not include unrelated customer addresses in release
evidence.

## Minimal evidence shape

```json
{
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
}
```

## Release-gate use

- Lab/dev evidence may include multiple agents. Use `--agent-id` to validate one
  canary without editing the evidence file.
- M4 release-gate evidence should use `--require-legacy-alias`,
  `--require-body-mcp`, and `--require-unknown-alias` once the corresponding
  cross-repo canaries are complete.
- The validation report belongs in `gov-infra/evidence/project37/` with the
  canary evidence. Commit only redacted evidence.
