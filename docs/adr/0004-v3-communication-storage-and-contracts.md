# ADR 0004 — v3 communication: secret storage + contract fixtures

**Date:** 2026-03-04  
**Status:** Accepted

## Context

lesser-soul v3 introduces communication channels (ENS/email/phone), contact preferences, and a communication gateway
(`comm-worker`) that routes inbound provider webhooks to lesser instances and handles outbound dispatch via
provider APIs.

The v3 draft spec contains ambiguous/conflicting guidance on where per-agent credentials (e.g., mailbox passwords)
should live, and it does not fully freeze the exact cross-repo payload shapes for comm-related APIs.

This ADR records the decisions used by `lesser-host` and points to the schema/fixture contract sources.

## Decisions

### 1) Per-agent mailbox / provider credentials

- **Platform-wide provider credentials** (e.g., Migadu/Telnyx API keys) live in **SSM Parameter Store** (SecureString).
- **Per-agent secrets** (mailbox passwords, phone/SIP secrets) also live in **SSM Parameter Store** (SecureString).
- **DynamoDB channel records store only non-secret metadata + secret references** (SSM parameter name/ARN), never the
  raw secret.

Rationale:
- Minimizes blast radius via IAM (SSM parameters can be scoped per prefix / per agent).
- Avoids persisting plaintext secrets in DynamoDB items that are broadly readable to application code.
- Enables rotation without rewriting historical DynamoDB records.

### 2) “Sent mail” strategy (`agent://email/sent`)

Outbound communication is recorded in two places:
- **Authoritative host log** (for delivery status + reputation aggregation).
- **Instance notification event** (for agent UX): the comm API delivers a `communication:outbound` notification to the
  owning lesser instance after a send is accepted/queued/sent.

`agent://email/sent` is implemented in `lesser-body` by filtering the instance’s existing notification/activity
storage, not by IMAP access to the mailbox.

Rationale:
- Keeps the inbound/outbound UX symmetric (both appear as notifications).
- Avoids introducing mailbox polling/credentials into `lesser-body`.

### 3) Attachment metadata MVP contract

Inbound email delivery includes **attachment metadata only** in the notification payload:
- `id`, `filename`, `contentType`, `sizeBytes`, optional `sha256`

The MVP does **not** deliver raw attachment bytes or pre-signed URLs. A future extension may add an artifact handle
or fetch API.

Rationale:
- Keeps the notification payload small and deterministic.
- Enables safe incremental delivery of attachments as separately access-controlled artifacts.

## Contract sources

The frozen JSON Schema contracts and example fixtures live under:

- `docs/spec/v3/schemas/`
- `docs/spec/v3/fixtures/`

These are intended for cross-repo consumption by `lesser` and `lesser-body`.

