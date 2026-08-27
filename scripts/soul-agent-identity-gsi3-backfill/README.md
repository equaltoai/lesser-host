# soul-agent-identity-gsi3-backfill

Operator tool that backfills the GSI key attributes onto existing soul items
after the stack updates that create the indexes have been deployed (issue
#1061). One run covers **both** models:

- **SoulAgentIdentity** — the gsi3 status enumeration index (`gsi3PK`/`gsi3SK`,
  part C1).
- **SoulAgentMintConversation** — the gsi4 agent-scoped time-ordered index
  (`gsi4PK`/`gsi4SK`, part C2 / issue #1067).

The name is kept from part C1 so the #1069 deploy-notes invocation keeps
working; the tool itself is dual-model (see "Why the name" below).

- Dry-run by default; mutations only under `--apply`.
- Writes ONLY the two index attributes per model, via conditional updates that
  never clobber concurrent live writes: absent keys use `attribute_not_exists`;
  present-but-wrong (stale) keys use a repair write bound to the observed stale
  values.
- One bounded scan covers both models (a combined filter routes each item to its
  model by `SK` prefix); throttled; resumable via a `LastEvaluatedKey` checkpoint
  stamped with the run mode, stage, and table (resume refuses a mismatch).
- Refuses to run until **both** gsi3 and gsi4 exist and are `ACTIVE` (preflight
  `DescribeTable`).
- Writes **one completeness marker per model** (`META#SOULAGENTIDENTITY#GSI3` /
  `BACKFILL` and `META#SOULAGENTMINTCONVERSATION#GSI4` / `BACKFILL`), each only
  after a complete, error-free apply pass for THAT model. The request-path
  consumers (soul publish, soul reputation worker for gsi3; the operator
  mint-conversation list for gsi4) fail closed until their model's marker
  exists.

## Why

`SoulAgentIdentity` items are keyed `PK=SOUL#AGENT#<agentId>`, `SK=IDENTITY` —
there was no key-bounded way to enumerate identities, so the request-path
enumerations were full-table scans (gsi3, `gsi3PK=IDENTITY#<status>`,
`gsi3SK=<agentId>`).

`SoulAgentMintConversation` items are keyed
`PK=SOUL#AGENT#<agentId>`, `SK=MINT_CONVERSATION#<conversationId>` where the
conversation id is a crypto/rand token with no recency meaning — an SK-ordered
page selected an arbitrary conversation subset beyond the page limit. The fix is
the gsi4 time-ordered index (`gsi4PK=SOUL#AGENT#<agentId>`,
`gsi4SK=<createdAt>#<conversationId>`).

DynamoDB creates one GSI per `UpdateTable`, so each index deployed in its own
stack update; this tool backfills the pre-existing items afterward. The two
indexes deploy as two stack updates (C1 then C2); run this tool once after the
C2 deploy to backfill both models.

## Usage

```bash
# Inspect what would change (no writes):
go run ./scripts/soul-agent-identity-gsi3-backfill --profile my-profile --stage lab

# Apply (writes gsi3+gsi4 attrs + both completeness markers):
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
| `--apply` | false | Mutate items (and write the markers). Default is dry-run. |
| `--page-size` | 100 (max 200) | Scan page size (DynamoDB `Limit`). |
| `--sleep-ms` | 100 | Base sleep between pages; jitter up to +50%. |
| `--checkpoint` | `soul-agent-identity-gsi3-backfill.<stage>.checkpoint.json` | Resume checkpoint path. |
| `--resume` | false | Continue from the checkpoint file (must exist). |

### Resume

An interrupted run leaves a checkpoint file (last evaluated key + per-model
cumulative counts). The checkpoint is stamped with the run **mode**
(`dry-run` or `apply`), the stage, the table, and a format version. Rerun with
`--resume` to continue instead of restarting — the resumed run MUST use the same
mode, stage, and table as the interrupted run:

```bash
# interrupted apply run:
go run ./scripts/soul-agent-identity-gsi3-backfill --profile p --stage live --resume --apply

# interrupted dry-run:
go run ./scripts/soul-agent-identity-gsi3-backfill --profile p --stage live --resume
```

Resuming with a different mode (e.g. `--resume --apply` after an interrupted
**dry run**) is refused with an explicit error: it would skip every
pre-checkpoint item and could certify a partial backfill. A checkpoint from
another stage/table, or one written by the pre-dual-model (v1) tool, is refused
the same way. Restart without `--resume` (or delete the checkpoint) to switch.

On a complete pass the checkpoint is removed. Restarting without `--resume` when
a checkpoint exists prints a warning (pass `--resume` to continue, or delete the
file to start over).

### Preflight refusal

The tool refuses to scan/write unless `DescribeTable` shows **both** gsi3 and
gsi4 present and `ACTIVE`:

- `SoulAgentIdentity (gsi3) does not exist on <table>; deploy the stack update first (one GSI per deploy)`
- `SoulAgentMintConversation (gsi4) does not exist on <table>; deploy the stack update first (one GSI per deploy)`
- `SoulAgentMintConversation (gsi4) on <table> is CREATING, not ACTIVE; wait for the index creation to finish`

### Count report (completion proof)

The final lines are per-model — the operator's proof the backfill ran for each
model:

```text
soul-agent-identity-gsi3-backfill complete
SoulAgentIdentity scanned=12 updated=11 repaired=0 already_correct=1 errors=0 marker=written
SoulAgentMintConversation scanned=3 updated=3 repaired=0 already_correct=0 errors=0 marker=written
completed_at=...
```

- `updated` — items whose index attributes were absent, backfilled with the
  `attribute_not_exists` conditional.
- `repaired` — items whose index attributes were present but stale, repaired
  with a conditional write bound to the observed stale values.
- `already_correct` — items whose index attributes already matched the computed
  keys.
- `marker=written` — a complete, error-free apply pass for that model; its
  consumers now trust the index.
- `marker=would-write` — dry-run completed.
- `marker=not-written` — errors occurred for that model (including any failed
  repair); fix and rerun (the marker stays absent and that model's consumers
  keep failing closed, which is intentional). Markers are per-model: a clean
  identity pass certifies the identity marker even while a mint-conversation
  error withholds the mint marker.

## Safety properties

- **No clobbering**: absent-key items are written with
  `SET <pkAttr> = :pk, <skAttr> = :sk WHERE attribute_not_exists(<pkAttr>) AND attribute_not_exists(<skAttr>)`.
  A conditional failure means a concurrent live write already set the
  attributes; the item is counted `already_correct`.
- **Stale-key repair is fail-closed**: an item whose index attributes are
  present but wrong is repaired with
  `SET <pkAttr> = :pk, <skAttr> = :sk WHERE <pkAttr> = :observedPk AND <skAttr> = :observedSk`
  — bound to the values the scan read, so a concurrent live writer is never
  clobbered. ANY repair failure (including a conditional failure, i.e. a
  concurrent writer changed the keys) is counted as an `error` and withholds
  that model's marker; the tool never certifies an item it did not repair or
  verify.
- **Byte-identical keys**: the mint-conversation plan computes `gsi4PK`/`gsi4SK`
  via the same model helpers as live writes, so backfilled and live-written
  index keys are identical.
- **Throttled**: bounded pages + sleep/jitter between pages; never saturates.
- **No secrets or table data in logs**: counts, dry-run samples (agent IDs,
  capped), and error lines only.
- **Fail closed**: no marker ⇒ consumers error explicitly (no silent
  empty/partial reads).

## Why the name

The tool started (part C1) as the identity-only gsi3 backfill. Part C2
(issue #1067) extended it to the mint-conversation gsi4 backfill with a second
model plan, and the name was deliberately kept so the #1069 deploy-notes
invocation and the C1 error-message guidance keep working. The README and the
per-model report make the dual-model behavior explicit. A future rename to a
generic name is possible but would touch the C1 error strings and deploy notes;
not worth the churn for an operator-invoked tool.

## Tests

```bash
go test ./scripts/soul-agent-identity-gsi3-backfill/ -count=1
```

Covers preflight refusal (missing/not-ACTIVE index for either model), dry-run
purity (no writes), the conditional-update behavior (absent-keys success,
absent-keys condition-conflict ⇒ already-correct), stale-key repair (success
bound to observed values; conditional failure ⇒ error and marker withheld;
already-correct only for truly-correct items; both models), per-model marker
gating (a clean identity pass certifies the identity marker while a
mint-conversation error withholds the mint marker), checkpoint resume
(interrupted run continues from the persisted key; cross-mode, version, and
stage/table mismatch resume refused), the inter-page throttle sleep (invoked
between pages), item routing by SK, and flag parsing.
