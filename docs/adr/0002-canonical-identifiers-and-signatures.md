# ADR 0002: Canonical Identifiers + Signatures (agentId, tokenId, registration, rotation, nonces)

- Status: Accepted
- Date: 2026-02-21

## Context

`lesser-soul` must support a persistent identity anchor (`agentId`) plus verifiable authorization for:

- registering an agent (domain/localId/wallet/capabilities/endpoints)
- updating the registration file (`metaURI` content)
- rotating the controlling wallet bound to an `agentId`

The registry spans off-chain state (DynamoDB) and on-chain state (contracts). Drift in normalization or signing rules
will create unrecoverable mismatches.

This ADR locks:

- normalization rules
- deterministic `agentId` derivation (must match `lesser-soul/SPEC.md`)
- token ID policy
- what bytes are signed and how they are verified
- anti-replay nonces
- TableTheory-only persistence and queryable secondary indexes (no scans)

## Decision

### 1) `normalizedDomain` (MUST mirror tip-registry normalization)

`normalizedDomain` MUST be computed using the same rules as `lesser-host/internal/domains.NormalizeDomain`:

1. trim surrounding whitespace
2. strip a trailing dot (`.`)
3. lowercase
4. reject:
   - schemes (contains `://`)
   - any of `/`, `:`, `@` (path/port/credentials)
   - wildcards (`*`)
5. IDNA UTS#46 → ASCII using `golang.org/x/net/idna` lookup profile (`idna.Lookup.ToASCII`)
6. reject:
   - empty result
   - length > 253
   - IP literals
   - domains without at least one dot (`.`)
   - invalid DNS labels (RFC-ish: `^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

### 2) `normalizedLocalAgentId` (conservative and deterministic)

`normalizedLocalAgentId` MUST be computed as:

1. trim surrounding whitespace
2. strip exactly one leading `@` if present
3. strip one trailing `/` if present
4. lowercase
5. reject if it contains any of: `/`, `:`, `@`
6. enforce length: 3–64 (inclusive)
7. enforce shape: `^[a-z0-9][a-z0-9_.-]{1,62}[a-z0-9]$`

Rationale: prevents ambiguous inputs (URLs/handles) and avoids collisions around separators (`/`) used in `agentId`
derivation.

### 3) `agentId` derivation (MUST match `lesser-soul/SPEC.md`)

`agentId` MUST be derived exactly as:

`agentId = uint256(keccak256(abi.encodePacked(normalizedDomain, "/", normalizedLocalAgentId)))`

Implementation note (non-Solidity): this is Keccak-256 of the UTF-8 bytes of the string:

`"${normalizedDomain}/${normalizedLocalAgentId}"`

Rendered as 32 bytes hex: `0x` + 64 lowercase hex characters.

Conformance vectors are in `docs/spec/agent-id-test-vectors.md`.

### 4) ERC-721 `tokenId` policy

When minting the soul ERC-721:

- `tokenId` MUST equal `agentId` (i.e., `tokenId := agentId` as `uint256`).
- No separate mapping layer between `agentId` and `tokenId` is introduced.

### 5) Registration file signing (EIP-191 over a canonical digest)

The registration file is a signed JSON document, stored in S3 and served by the control plane. To make signatures
portable:

1. Define the **unsigned registration payload** as the full registration JSON object **with**
   `attestations.selfAttestation` omitted.
2. Canonicalize using **RFC 8785 (JCS)** JSON Canonicalization Scheme.
3. Compute:
   - `registrationDigest := keccak256(jcsBytes)`
4. Produce `selfAttestation` using **EIP-191 personal sign** over the 32-byte digest:
   - client signs `registrationDigest` bytes (not a hex string) using wallet `signMessage(registrationDigest)`
   - verifier recovers address from the EIP-191 text-hash of those 32 bytes and compares to the registration’s `wallet`
     field.

Why digest-signing: fixed-size payload avoids library differences and makes signatures cheap to verify.

### 6) Wallet rotation (two signatures, on-chain verifiable)

Rotation is two-step:

1) **Propose rotation**: the **new wallet** signs a typed rotation proposal referencing `agentId`.

2) **Confirm rotation**: the **current wallet** signs the proposal hash (or typed data digest) to confirm.

To standardize, use EIP-712 typed data:

- Domain:
  - `name`: `LesserSoul`
  - `version`: `1`
  - `chainId`: chain where the soul registry is deployed
  - `verifyingContract`: `ISoulRegistry` contract address
- Types:
  - `WalletRotationProposal(uint256 agentId,address currentWallet,address newWallet,uint256 nonce,uint256 deadline)`

Rules:

- The contract stores `nonce` per `agentId` and requires `nonce` to match current value (anti-replay) before accepting.
- `deadline` is a unix timestamp (seconds); must be >= current time.
- Required signatures:
  - `newSig`: signature by `newWallet` over the proposal typed data
  - `currentSig`: signature by `currentWallet` over the proposal digest (exactly the EIP-712 digest of the typed data)
    or over the same typed data (implementation choice must be locked in the contract implementation and tests)

### 7) Anti-replay nonces (off-chain and on-chain)

- On-chain: `ISoulRegistry` maintains an `agentNonce[agentId]` used for wallet rotation (and any other signed operations
  accepted on-chain).
- Off-chain: the control plane maintains its own replay protection for API operations (begin/verify, updates, etc.).
  Off-chain nonce state MUST be stored using TableTheory (TableTheory-only persistence rule).

### 8) TableTheory-only persistence + queryable secondary indexes (no scans)

All off-chain durable state for the registry MUST be stored in DynamoDB using TableTheory models.

Primary records follow `lesser-soul/SPEC.md`:

- `PK: SOUL#AGENT#{agentId}`
  - `SK: IDENTITY`
  - `SK: REPUTATION`
  - `SK: VALIDATION#{timestamp}#{challengeId}`
  - `SK: ENDORSEMENT#{endorserAgentId}`
- `PK: SOUL#OP#{operationId}`
  - `SK: OPERATION`

To support `agents/mine` and `search` without table scans, maintain **materialized index items** (additional records)
alongside the primary records, queryable via PK+SK patterns:

- Wallet → agents:
  - `PK: SOUL#WALLET#{wallet}`
  - `SK: AGENT#{agentId}`
- Domain → agents:
  - `PK: SOUL#DOMAIN#{normalizedDomain}`
  - `SK: LOCAL#{normalizedLocalAgentId}#AGENT#{agentId}`
- Capability → agents (one item per capability):
  - `PK: SOUL#CAP#{capability}`
  - `SK: DOMAIN#{normalizedDomain}#LOCAL#{normalizedLocalAgentId}#AGENT#{agentId}`

Index items MUST be updated transactionally with the primary identity record where possible (or written with safe
idempotency + reconciliation).
