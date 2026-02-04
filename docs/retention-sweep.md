# Render retention sweep (dev verification)

M4 includes a daily retention sweep that deletes expired render artifacts from:

- DynamoDB (`RenderArtifact` records)
- S3 (thumbnail + snapshot objects)

This repo includes a dev-only smoke test script that:

1. Uploads a test thumbnail + snapshot into the artifacts bucket.
2. Inserts an **already-expired** `RenderArtifact` record into the state table.
3. Invokes the `render-worker` Lambda with a scheduled EventBridge event payload.
4. Verifies the DynamoDB record + S3 objects are deleted.

## Run

Prereqs:

- A deployed dev stack for the chosen stage (`lab` by default).
- AWS CLI configured (`AWS_PROFILE` / `AWS_REGION` as needed).

Command:

```bash
STAGE=lab scripts/dev/verify-render-retention-sweep.sh
```

Notes:

- The stack name is `lesser-host-${STAGE}` (e.g. `lesser-host-lab`).
- The script uses CloudFormation outputs to discover the table, bucket, and render-worker function name.

