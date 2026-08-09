# Soul-registry audit: Legacy TheoryLive recovery contract

## Proposed change

Add an off-chain-only, read-only recovery inventory/detail contract for authenticated Managed instances. It selects exact legacy Host declaration evidence and immutable registration-version evidence under Slug, registration, agent, and conversation guards. It never publishes, finalizes, remints, changes an agent identifier, or writes a sibling repository's state.

## Solidity contract changes

None. Hardhat, Slither, solhint, gas, mainnet signing, and Safe-ready payloads are not applicable.

## On-chain Go code changes

None. No RPC call, Mint-signer, nonce, gas, transaction, or event parsing changes.

## Off-chain state changes

- DynamoDB models: no new source-of-truth row is required for the read contract. Existing `SoulAgentIdentity`, `SoulAgentPromotion`, `SoulAgentVersion`, `SoulAgentMintConversation`, and tenant-scoped `HostedGenesisSession` rows remain authoritative for their existing fields.
- Source-of-truth clarity:
  - hosted identity/version: `SoulAgentIdentity` plus `SoulAgentVersion` and checksum-verified S3 objects;
  - legacy declaration bytes: decoded `SoulAgentMintConversation.ProducedDeclarations`, accepted only when bound to the same Slug/registration/agent/conversation by `HostedGenesisSession` and promotion evidence;
  - execution/cache state: never authoritative and never exposed;
  - Body projections: Body-owned after explicit adoption.
- Reconciliation trigger: none on read. Recovery is side-effect-free.
- Divergence handling: return an explicit classification and fail closed when bindings or checksums disagree. Never synthesize a missing historical registration.
- Index/search: inventory is restricted to identities within the authenticated instance's verified domain set; no cross-tenant scan result is returned.
- Backfill: no implicit backfill. A future public-registration repair for Silas would require an explicitly operator-gated new publication/re-attestation event and cannot masquerade as restoration of missing history.

## Safe-ready governance payload

Not applicable; no on-chain mutation.

## Mint-signer key handling

Unchanged and untouched.

## Soul-namespace coordination

No JSON-LD namespace URL or semantics change. Soul steward coordination is not required.

## Consumer impact

- Lesser instances: no recovery-read dependency; existing binding and private self-scope contracts remain unchanged.
- Body: calls Host directly with its existing managed InstanceKey and receives exact declarations plus honest migration provenance for operator-gated adoption.
- Sim: should validate tenant isolation, checksum mismatch failure, declaration-only recovery, and no-write behavior.
- External on-chain callers: none.

## Audit verdict

Clean for enumeration as an additive, off-chain-only Soul registry recovery surface. The public `/api/v1/soul/agents/{agentId}/registration` contract must not fabricate Silas's missing artifact.
