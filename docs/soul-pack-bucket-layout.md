# Soul registry bucket S3 key layout

Each `lesser-host` stage provisions a private, versioned S3 bucket named:

`lesser-host-<stage>-<account>-<region>-soul-packs`

Despite the historical `soul-packs` name, this bucket is **not** used to distribute signed deployment “packs”.

It stores **Soul Registry artifacts** under a dedicated prefix: `registry/v1/`.

## Stage pointer (SSM)

The control plane stores a per-stage pointer in the central account:

- `/soul/<stage>/packBucketName` — the bucket name (historical name; stable contract)

## Soul Registry artifacts (`registry/v1/`)

All registry artifacts live under `registry/v1/`.

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

When distributing a snapshot bundle, use the manifest + KMS signature pattern for integrity:

- `registry/v1/<type>/roots/<rootHex>/manifest.json`
- `registry/v1/<type>/roots/<rootHex>/manifest.sig`

Where `<type>` is `reputation` or `validation`.
