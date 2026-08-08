# Soul email channel regression backfill

This runbook repairs issue #1023: a v3 registration re-publication omitted optional communication fields and removed
materialized Host communication state. The repair restores the existing Migadu identity. It does **not** provision a
mailbox, generate a password, call Migadu, or read/decrypt an SSM value.

## Safety contract

- Deploy the omission-preservation code fix before applying the backfill. Otherwise another registration sync can delete
  restored state again.
- Run the tool from the Host account with `AWS_PROFILE=Lesser`. `TheoryLive` and `Trench` are tenant-instance AWS
  profiles; they are not authorized sources or destinations for Control plane state repair.
- The tool is dry-run by default. Apply requires all of `--source-table`, `--confirm-stage`, and `--rollback-out`.
- The source table must be a DynamoDB point-in-time restore of the same stage's state table at a verified last-good time.
- The tool copies the exact original `CHANNEL#email`, `SoulEmailAgentIndex`, `SoulChannelAgentIndex`, and, when present,
  `CONTACT_PREFERENCES`. It verifies that the original `SecretRef` still exists without decrypting it.
- Existing conflicting records block. Writes are per-agent DynamoDB transactions with create-only conditions and an
  audit event. There is no cross-domain or cross-agent overwrite path.
- Never call the email provision endpoint as recovery. It can create or rotate provider state instead of restoring the
  existing identity.

## Affected Host partitions

| Tenant instance profile | Host stage | Host state table | Managed-instance domain filters |
|---|---|---|---|
| TheoryLive | `live` | `lesser-host-live-state` | `theory.greater.website` |
| Trench | `lab` | `lesser-host-lab-state` | `trenchcoat.greater.website`, `dev.trenchcoat.greater.website` |

Read-only inventory on 2026-08-08 found 7 matching live identities and 26 matching lab identities. Those are inventory
counts, not proof that every identity previously had a provisioned mailbox. Eligibility comes only from the exact
last-good source record and the still-existing SSM parameter named by its `SecretRef`.

## 1. Establish the last-good time

Use the last known successful outbound email event and the first failed `comm.channel_not_provisioned` event to choose a
UTC point strictly before deletion. Correlate the request/X-Ray identifiers from the incident with Control plane logs and
version history. If one timestamp does not cover both Host stages, use a separate timestamp per stage.

Do not guess past the available recovery window. Confirm point-in-time recovery first:

```bash
AWS_PROFILE=Lesser aws dynamodb describe-continuous-backups \
  --region us-east-1 \
  --table-name lesser-host-live-state

AWS_PROFILE=Lesser aws dynamodb describe-continuous-backups \
  --region us-east-1 \
  --table-name lesser-host-lab-state
```

## 2. Restore temporary source tables

These commands create new temporary tables; they do not replace either active table. The operator must authorize and run
them with the incident's verified UTC timestamps.

```bash
export LIVE_RESTORE_TIME='<UTC timestamp before live deletion>'
export LAB_RESTORE_TIME='<UTC timestamp before lab deletion>'
export LIVE_SOURCE_TABLE='issue-1023-live-last-good'
export LAB_SOURCE_TABLE='issue-1023-lab-last-good'

AWS_PROFILE=Lesser aws dynamodb restore-table-to-point-in-time \
  --region us-east-1 \
  --source-table-name lesser-host-live-state \
  --target-table-name "$LIVE_SOURCE_TABLE" \
  --restore-date-time "$LIVE_RESTORE_TIME"

AWS_PROFILE=Lesser aws dynamodb restore-table-to-point-in-time \
  --region us-east-1 \
  --source-table-name lesser-host-lab-state \
  --target-table-name "$LAB_SOURCE_TABLE" \
  --restore-date-time "$LAB_RESTORE_TIME"
```

Wait for both source tables to reach `ACTIVE`. Do not apply from a source table that is still restoring.

## 3. Dry-run both tenant-instance scopes

Write evidence outside the repository. Reports contain agent identifiers and hashes, never SSM secret values.

```bash
AWS_PROFILE=Lesser AWS_REGION=us-east-1 go run ./scripts/soul-email-channel-backfill \
  --stage live \
  --source-table "$LIVE_SOURCE_TABLE" \
  --domain theory.greater.website \
  --out /secure/issue-1023-theorylive-dry-run.json

AWS_PROFILE=Lesser AWS_REGION=us-east-1 go run ./scripts/soul-email-channel-backfill \
  --stage lab \
  --source-table "$LAB_SOURCE_TABLE" \
  --domain trenchcoat.greater.website \
  --domain dev.trenchcoat.greater.website \
  --out /secure/issue-1023-trench-dry-run.json
```

Proceed only when:

- `errors == 0` and `blocked == 0`;
- every `planned` agent has all expected create actions;
- the recovered email hash is stable across repeated dry-runs;
- `ssm_parameter_present` is true for each planned agent;
- source and destination stages/tables/domains match the matrix above.

A `source_channel_secret_parameter_missing` result is a hard stop. The mailbox may need a controlled password rotation on
the existing Migadu identity; do not provision a replacement and do not fabricate a new `SecretRef`.

## 4. Lab/Trench canary, then lab fleet

After the preventive fix has been released from `main`, the operator deploys it to `lab` with the canonical AppTheory
command. Do not set a timeout:

```bash
AWS_PROFILE=Lesser theory app up --stage lab --execute
```

After the deployment is healthy, select one eligible Trench agent from the dry-run report.

```bash
export CANARY_AGENT_ID='<agent id from the reviewed dry-run>'

AWS_PROFILE=Lesser AWS_REGION=us-east-1 go run ./scripts/soul-email-channel-backfill \
  --stage lab \
  --source-table "$LAB_SOURCE_TABLE" \
  --agent-id "$CANARY_AGENT_ID" \
  --apply \
  --confirm-stage lab \
  --rollback-out /secure/issue-1023-trench-canary-created-keys.json \
  --out /secure/issue-1023-trench-canary-apply.json
```

Verify channel discovery, contact preferences, inbox/sent reads, and a real outbound email. After the lab soak passes,
apply the two Trench domain filters to the full lab scope using new evidence paths.

## 5. Live/TheoryLive canary, then live fleet

Live execution requires explicit operator authorization and follows a successful lab canary/soak plus operator-owned
promotion/release from `main`. Deploy the preventive fix with the canonical AppTheory command; do not set a timeout:

```bash
AWS_PROFILE=Lesser theory app up --stage live --execute
```

Then apply one reviewed TheoryLive canary and verify it before the domain-wide apply:

```bash
AWS_PROFILE=Lesser AWS_REGION=us-east-1 go run ./scripts/soul-email-channel-backfill \
  --stage live \
  --source-table "$LIVE_SOURCE_TABLE" \
  --agent-id '<reviewed TheoryLive agent id>' \
  --apply \
  --confirm-stage live \
  --rollback-out /secure/issue-1023-theorylive-canary-created-keys.json \
  --out /secure/issue-1023-theorylive-canary-apply.json

AWS_PROFILE=Lesser AWS_REGION=us-east-1 go run ./scripts/soul-email-channel-backfill \
  --stage live \
  --source-table "$LIVE_SOURCE_TABLE" \
  --domain theory.greater.website \
  --apply \
  --confirm-stage live \
  --rollback-out /secure/issue-1023-theorylive-created-keys.json \
  --out /secure/issue-1023-theorylive-apply.json
```

## 6. Post-apply proof

Re-run both dry-runs. Restored agents must classify as `already_healthy`; no new `planned`, `blocked`, or `error` records
may remain. For each canary and a sampled fleet set, verify:

1. `GET /api/v1/soul/agents/{id}/channels` returns the original email channel and contact preferences.
2. The email index resolves to the same agent.
3. `email_read` succeeds for inbox and sent folders.
4. `email_send` succeeds and records a sent delivery.
5. Control plane audit contains `soul.channel.email.backfill`.
6. A channel-less registration re-publication no longer mutates the managed email channel or contact preferences.

Identify and repair the client that emitted the omission before closing the incident. The Host guard remains required even
after that caller is fixed.

## 7. Temporary-table cleanup

Temporary source tables and local rollback/evidence files are incident artifacts. Deleting a DynamoDB table is destructive
and requires separate explicit operator authorization. Do not delete either source table until post-apply verification,
incident review, and the required evidence-retention decision are complete.
