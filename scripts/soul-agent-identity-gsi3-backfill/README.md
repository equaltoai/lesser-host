# soul-agent-identity-gsi3-backfill

Operator tool that backfills the gsi3 status enumeration index attributes
(`gsi3PK`/`gsi3SK`) onto existing `SoulAgentIdentity` items after the stack
update that creates the index has been deployed (issue #1061 part C1).

- Dry-run by default; mutations only under `--apply`.
- Writes ONLY the two gsi3 attributes, via conditional updates that never clobber
  concurrent live writes: absent keys use `attribute_not_exists`; present-but-wrong
  (stale) keys use a repair write bound to the observed stale values.
- Throttled, bounded, key-only scans; resumable via a `LastEvaluatedKey` checkpoint
  stamped with the run mode, stage, and table (resume refuses a mismatch).
- Refuses to run until gsi3 exists and is `ACTIVE` (preflight `DescribeTable`).
- Writes a completeness marker (`META#SOULAGENTIDENTITY#GSI3` / `BACKFILL`) only after a
  complete, error-free apply pass. The request-path identity enumerations
  (soul publish, soul reputation worker) fail closed until that marker exists, so
  running `--apply` to completion is required before those paths answer from gsi3.

## Why

`SoulAgentIdentity` items are keyed `PK=SOUL#AGENT#<agentId>`, `SK=IDENTITY` — there was
no key-bounded way to enumerate identities, so the request-path enumerations were
full-table scans. The fix is a dedicated GSI (gsi3, `gsi3PK=IDENTITY#<status>`,
`gsi3SK=<agentId>`). DynamoDB creates one GSI per `UpdateTable`, so the index deploys in
its own stack update; this tool backfills the pre-existing items afterward.

## Usage

```bash
# Inspect what would change (no writes):
go run ./scripts/soul-agent-identity-gsi3-backfill --profile my-profile --stage lab

# Apply (writes gsi3 attrs + completeness marker):
go run ./scripts/soul-agent-identity-gsi3-backfill --profile my-profile --stage live --apply
```

`AWS_PROFILE` is honored when `--profile` is omitted. The table resolves to
`lesser-host-<stage>-state` (override with `--table`).

### Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--stage` | — (required, `lab`\|`live`) | Deploy stage; resolves the table name. |
| `--profile` | `AWS_PROFILE` env | AWS profile for credentials. |
| `--region` | `AWS_REGION` \|\| `us-east-1` | AWS region. |
| `--table` | `lesser-host-<stage>-state` | Table override. |
| `--apply` | false | Mutate items (and write the marker). Default is dry-run. |
| `--page-size` | 100 (max 200) | Scan page size (DynamoDB `Limit`). |
| `--sleep-ms` | 100 | Base sleep between pages; jitter up to +50%. |
| `--checkpoint` | `soul-agent-identity-gsi3-backfill.<stage>.checkpoint.json` | Resume checkpoint path. |
| `--resume` | false | Continue from the checkpoint file (must exist). |

### Resume

An interrupted run leaves a checkpoint file (last evaluated key + cumulative counts).
The checkpoint is stamped with the **run mode** (`dry-run` or `apply`), the stage, and
the table. Rerun with `--resume` to continue instead of restarting — the resumed run
MUST use the same mode, stage, and table as the interrupted run:

```bash
# interrupted apply run:
go run ./scripts/soul-agent-identity-gsi3-backfill --profile p --stage live --resume --apply

# interrupted dry-run:
go run ./scripts/soul-agent-identity-gsi3-backfill --profile p --stage live --resume
```

Resuming with a different mode (e.g. `--resume --apply` after an interrupted **dry run**)
is refused with an explicit error: it would skip every pre-checkpoint item and could
certify a partial backfill. Restart without `--resume` (or delete the checkpoint) to
switch modes. A checkpoint from another stage/table is refused the same way.

On a complete pass the checkpoint is removed. Restarting without `--resume` when a
checkpoint exists prints a warning (pass `--resume` to continue, or delete the file to
start over).

### Preflight refusal

The tool refuses to scan/write unless `DescribeTable` shows gsi3 present and `ACTIVE`:

- `gsi3 does not exist on <table>; deploy the stack update first (one GSI per deploy)`
- `gsi3 on <table> is CREATING, not ACTIVE; wait for the index creation to finish`

### Count report (completion proof)

The final line is the operator's proof the backfill ran:

```text
soul-agent-identity-gsi3-backfill complete scanned=12 updated=11 repaired=0 already_correct=1 errors=0 marker=written completed_at=...
```

- `updated` — items whose gsi3 attributes were absent, backfilled with the
  `attribute_not_exists` conditional.
- `repaired` — items whose gsi3 attributes were present but stale, repaired with a
  conditional write bound to the observed stale values.
- `already_correct` — items whose gsi3 attributes already matched the status-derived
  keys.
- `marker=written` — a complete, error-free apply pass; consumers now trust gsi3.
- `marker=would-write` — dry-run completed.
- `marker=not-written` — errors occurred (including any failed repair); fix and rerun
  (the marker stays absent and consumers keep failing closed, which is intentional).

## Safety properties

- **No clobbering**: absent-key items are written with
  `SET gsi3PK = :pk, gsi3SK = :sk WHERE attribute_not_exists(gsi3PK) AND attribute_not_exists(gsi3SK)`.
  A conditional failure means a concurrent live write already set the attributes; the
  item is counted `already_correct`.
- **Stale-key repair is fail-closed**: an item whose gsi3 attributes are present but
  wrong is repaired with `SET gsi3PK = :pk, gsi3SK = :sk WHERE gsi3PK = :observedPk AND gsi3SK = :observedSk`
  — bound to the values the scan read, so a concurrent live writer is never clobbered.
  ANY repair failure (including a conditional failure, i.e. a concurrent writer changed
  the keys) is counted as an `error` and withholds the marker; the tool never certifies
  an item it did not repair or verify.
- **Throttled**: bounded pages + sleep/jitter between pages; never saturates.
- **No secrets or table data in logs**: counts, dry-run samples (agent IDs, capped), and
  error lines only.
- **Fail closed**: no marker ⇒ consumers error explicitly (no silent empty/partial reads).

## Part C2 (issue #1067)

The mint-conversation gsi3 (gsi4) backfill extends this tool with a second `modelPlan`;
the scan/checkpoint/throttle machinery is model-agnostic. Nothing for the second model
is built in this PR.

## Tests

```bash
go test ./scripts/soul-agent-identity-gsi3-backfill/ -count=1
```

Covers preflight refusal (missing/not-ACTIVE index), dry-run purity (no writes), the
conditional-update behavior (absent-keys success, absent-keys condition-conflict ⇒
already-correct), stale-key repair (success bound to observed values; conditional failure
⇒ error and marker withheld; already-correct only for truly-correct items), checkpoint
resume (interrupted run continues from the persisted key; cross-mode and stage/table
mismatch resume refused), the inter-page throttle sleep (invoked between pages), and
flag parsing.
