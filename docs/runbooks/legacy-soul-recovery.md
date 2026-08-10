# Legacy Soul recovery runbook

Use this runbook only after the additive recovery API has passed review, been promoted by the operator, and been
deployed through the normal `lab` then `live` sequence. The steward does not merge, deploy, or mutate live data.

## Preconditions

- The exact Host release is deployed through `theory app up --stage <lab|live> --execute`.
- Lab soak has completed before live authorization.
- The caller is inside the intended Managed instance runtime and resolves its raw key from
  `LESSER_HOST_INSTANCE_KEY_ARN`; never print or persist the value.
- Capture pre-read identity, promotion, Hosted Genesis, version-row, and S3 object metadata for comparison. Do not copy
  declaration or message content into logs/evidence.

## Read sequence

1. Call `GET /api/v1/soul/instance/recovery/agents?limit=20` with the InstanceKey bearer token.
2. Follow `next_cursor` only when `has_more` is true. Treat cursors as opaque.
3. Confirm the inventory includes only the authenticated Slug's verified domains and contains no `declarations` or
   `messages` fields.
4. For each approved agent, call `GET /api/v1/soul/instance/recovery/agents/{agentId}`.
5. Validate the JSON Schema, recompute SHA256 over the exact returned `declarations` JSON bytes, and compare it to
   `migration_read_sha256`.
6. If `published_artifact_verified`, verify the complete ordered version chain and current-object checksum evidence.
7. If `legacy_declarations_only`, require empty `versions` and no `published_registration`; never invent history.
8. Hand the exact response to the separately reviewed Body adoption flow under explicit operator control.

## Fail closed

Stop on any `401`, `403`, `409`, `413`, or `500`. Do not retry around a boundary or integrity failure, select another
conversation manually, read storage directly as a consumer workaround, or create a missing public artifact. A `429`
may be retried only after `Retry-After`.

## No-business-state-change proof

After repeated reads, compare pre/post consistent reads of the identity, promotion, Hosted Genesis session, source
conversation, all version rows, and current/versioned S3 objects. They must be unchanged. `InstanceKey.LastUsedAt` and
redacted audit entries are expected security telemetry, not Soul recovery mutations. Confirm logs contain only hashed
identifiers and no raw key, declarations, messages, provider output, or signing material.

## Rollback and escalation

- Roll back only through operator-controlled Lambda release rollback and AppTheory deployment procedures.
- Do not delete Lambda versions, S3 objects, version rows, or identities.
- A checksum/binding conflict is an integrity incident, not a data-repair invitation.
- Forward publication for a legacy declaration-only agent requires separate scope, review, and honest new provenance.
  No Solidity or on-chain transaction is part of this runbook.
