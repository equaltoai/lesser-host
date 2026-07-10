# Hosted Genesis SQS demotion runbook

Project 51 M4 demotes hosted-genesis SQS from user-visible authority.

## Authority order

1. `HostedGenesisSession` in Host DynamoDB is the source of truth for status, retry guidance, and finalize gates.
2. AppTheory MicroVM registry/lifecycle state is execution/cache state only and must be reconstructible from `HostedGenesisSession`.
3. The hosted-genesis SQS queue, if present, is non-authoritative MicroVM dispatch/operator/backfill/janitor transport. DLQ backlog or AI-worker outage must not change what status reads or finalize preflight decide once a `HostedGenesisSession` exists.

## Operator recovery

- Read the `HostedGenesisSession` row first by instance slug and conversation id.
- If the session is `declaration_ready`, use the declaration checkpoint and `CanPublish` gate; do not inspect queue state to decide readiness.
- If the session is failed, return the bounded recovery action from the session failure projection.
- Treat hosted-genesis queue/DLQ messages as hints for MicroVM dispatch recovery, backfill, or cleanup only. Never replay a message without checking the session `version`, current `status`, registration id, agent id, instance slug, and turn/idempotency ledger.

## No-leak telemetry

Logs, audit entries, queue payloads, and template outputs may contain only IDs/hashes, categories, status, and bounded content-free counters where an existing metric requires them. They must not contain raw transcripts, prompts, bearer tokens, Instance API keys, provider credentials, wallet signatures, SSM values, MicroVM endpoint tokens, shell/auth tokens, or raw provider responses.
