# SoulPackBucket S3 key layout

Each `lesser-host` stage provisions a private, versioned S3 bucket named:

`lesser-host-<stage>-<account>-<region>-soul-packs`

The bucket stores:

- **Immutable** signed Soul packs (used for managed deployments).
- **Soul Registry artifacts** (registration + attestations), under a dedicated prefix.

## Signed Soul packs (bucket root)

A pack version is identified by `<version>` and consists of exactly three objects at the bucket root:

- `soul-pack-<version>.tgz` — tarball containing the pack contents
- `soul-pack-<version>.manifest.json` — manifest listing every file path + sha256 in the tarball
- `soul-pack-<version>.manifest.sig` — KMS signature over the sha256 digest of the manifest JSON

`lesser-host` provisioning fetches these exact keys when `RUN_MODE` is `soul-deploy` or `soul-init` in the managed
runner.

## Stage pointers (SSM)

The control plane stores per-stage pointers in the central account:

- `/soul/<stage>/packBucketName` — the SoulPackBucket name
- `/soul/<stage>/signingKeyArn` — KMS key used to verify `*.manifest.sig`
- `/soul/<stage>/packVersion` — current `<version>` pointer (or an instance may pin a specific version)

## Soul Registry artifacts (`registry/v1/`)

All registry artifacts live under `registry/v1/` to avoid collisions with Soul pack objects at the bucket root.

### Agent registration (current)

- `registry/v1/agents/<agentId>/registration.json`

Notes:
- This key always points at the **current** registration file for the agent. Overwrites are allowed; bucket versioning
  retains history.
- The registration file is expected to carry its wallet signature in the JSON body
  (`attestations.selfAttestation`), per `docs/adr/0002-canonical-identifiers-and-signatures.md`.

### Reputation snapshots (historical, immutable)

- `registry/v1/reputation/roots/<rootHex>/snapshot.json`
- `registry/v1/reputation/roots/<rootHex>/proofs.json`
- `registry/v1/reputation/roots/<rootHex>/manifest.json`

Where `<rootHex>` is the on-chain Merkle root (lowercase hex with `0x` prefix) published via
`ReputationAttestation.publishRoot(...)`.

### Reputation recompute snapshots (pre-root)

- `registry/v1/reputation/snapshots/chain-<chainId>/block-<blockRef>.json`

Notes:
- Written by the scheduled reputation recomputation job (M6) for audit/debugging before Merkle roots exist.
- These snapshots are repeatable/deterministic for the same block range + weights configuration; overwrites are
  acceptable and bucket versioning retains history.

### Validation snapshots (historical, immutable)

- `registry/v1/validation/roots/<rootHex>/snapshot.json`
- `registry/v1/validation/roots/<rootHex>/proofs.json`
- `registry/v1/validation/roots/<rootHex>/manifest.json`

Where `<rootHex>` is the on-chain Merkle root published via `ValidationAttestation.publishRoot(...)`.

### Snapshot signing (KMS; when distributed)

Current root publications include a `manifest.json` with sha256 sums, but do not yet write a KMS signature.

When a snapshot is distributed as a pack, use the same pattern as Soul packs for integrity:

- `registry/v1/<type>/roots/<rootHex>/manifest.json`
- `registry/v1/<type>/roots/<rootHex>/manifest.sig`

Where `<type>` is `reputation` or `validation`.
