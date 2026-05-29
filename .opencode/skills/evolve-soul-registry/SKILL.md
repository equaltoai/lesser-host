---
name: evolve-soul-registry
description: Use when a change touches the soul registry — Solidity contracts in `contracts/`, on-chain-reaching Go code, off-chain DynamoDB state that mirrors on-chain references, Safe-ready governance payloads, mint-signer key handling, or the soul-namespace contract lesser-soul publishes. Walks contract integrity (Slither + hardhat + solhint), deploy discipline (Sepolia → Safe-ready mainnet), off-chain reconciliation, and governance-coordination.
---

# Evolve the soul registry

host's soul registry is the on-chain + off-chain + governed identity authority for equaltoai agents. It anchors agent identity via ERC-721 tokens on Ethereum, routes tips via TipSplitter, and supports governance mutations via Safe-ready payloads. On-chain actions are irreversible; off-chain state must stay consistent with on-chain references; governance payloads require multisig execution for non-trivial mutations.

This skill walks every soul-registry-affecting change.

## The soul-registry architecture (memorize)

- **`contracts/`** — Solidity source (ERC-721 agent-mint, TipSplitter, governance helpers) with Hardhat test harness, Slither SAST, solhint lint
- **`internal/soul*/`** — Go packages for soul-registry subsystems (registration, local identity resolution, search, avatars, reputation, comm)
- **`cmd/soul-reputation-worker/`** — periodic reputation aggregation
- **Soul-registry API** — `/api/v1/soul/*` — registration, governance, lookup, search
- **Off-chain DynamoDB state** — mirrors on-chain references + holds attributes not suitable on-chain (avatars, search indexes, mutable metadata)
- **Safe-ready payloads** — multisig-ready transaction blobs for on-chain governance mutations
- **Mint-signer key** — the signing key for agent-mint transactions; generated via `scripts/generate-mint-signer-key.sh`; handled per operator security policy; never logged
- **Eth RPC endpoint** — configured per environment in SSM (Sepolia for test, mainnet for production)
- **Lesser-soul namespace** — `spec.lessersoul.ai/ns/agent-attribution/v1` JSON-LD contract that host's registry implements

## When this skill runs

Invoke when:

- A change modifies Solidity contracts in `contracts/`
- A change modifies on-chain-reaching Go code (signing, RPC calls, transaction preparation, event parsing)
- A change modifies off-chain DynamoDB state that mirrors on-chain references
- A change modifies Safe-ready payload preparation
- A change touches mint-signer key handling or rotation
- A change affects the soul-namespace contract (coordinate with `soul` steward)
- A change adds a new on-chain function / event / token type
- A change modifies governance-mutation flow (who signs, threshold, execution path)
- An on-chain transaction reverts unexpectedly and requires investigation
- Off-chain state diverges from on-chain state

## Preconditions

- **The change is described concretely.** "Improve soul registry" is too vague; "add a new `setAvatarURI(uint256 tokenId, string calldata uri)` function to AgentMint.sol, restricted to the token owner or approved operator, with SetAvatarURI event emission, changing the soul-registry API `PUT /api/v1/soul/agents/:id/avatar` to call it via the mint-signer" is concrete.
- **MCP tools healthy**, `memory_recent` first — on-chain coordination accumulates context.
- **On-chain network state** known — Sepolia + mainnet contract addresses, current block, Safe signer set.
- **For Solidity changes**: a dev environment with Hardhat + Slither + solhint ready.
- **For Safe-ready payload changes**: Safe transaction service access + signer coordination understood.

## The six-dimension walk

### Dimension 1: Solidity contract correctness

For each contract change:

- **Functionality** — what the function does, its state changes, its event emissions
- **Access control** — who can call it (owner-only, approved-only, public, restricted by role)
- **Economic model** — if the function has financial implications (mint fees, gas cost, tipping splits), the math must be exactly right
- **Reentrancy** — calls to external contracts follow checks-effects-interactions or use reentrancy guards
- **Overflow / underflow** — Solidity 0.8+ has default checks, but custom math may still overflow
- **Gas cost** — expensive functions may be DoS vectors
- **Upgradeability** — contract code is immutable; state lives on-chain; upgrades happen via new contract deploys with migration logic

Tool results:

- **Hardhat tests pass** — new tests added for the new functionality; existing tests continue to pass
- **Slither findings resolved** — each finding fixed or explicitly allowlisted with documented rationale
- **solhint clean** — style and convention lint
- **Gas report** — if relevant, compare before / after gas costs

### Dimension 2: On-chain-reaching Go code correctness

For Go code that constructs or sends on-chain transactions:

- **Signing discipline** — mint-signer key is loaded from SSM / Secrets Manager at runtime, never hardcoded, never logged
- **Nonce management** — transaction nonces managed correctly to avoid "already known" or "nonce too low" errors
- **Gas estimation** — estimates done before send; gas price sourced sensibly; caps on gas to avoid runaway costs
- **Idempotency** — re-running a preparation step produces the same transaction (for the same intent); re-sending a signed transaction behaves correctly against Ethereum's replay protection
- **Dry-run mode** — for any irreversible on-chain call, a dry-run mode that simulates without sending is available
- **Explicit confirmation** — for any mutating call, explicit confirmation from the caller (via code path or human-in-the-loop) required
- **Revert reason parsing** — on-chain reverts surface meaningful error messages, not silent failures
- **Event parsing** — inbound event parsing tolerates future event-signature additions (forward-compat)

### Dimension 3: Off-chain state consistency

For changes affecting DynamoDB state that mirrors on-chain references:

- **Reconciliation** — when on-chain state changes (new mint, transfer, burn, event emission), off-chain state updates. What triggers the reconciliation? (Event-listening worker, periodic sweep, on-demand refresh.)
- **Source-of-truth clarity** — for any given attribute, is on-chain or off-chain the source of truth? Document explicitly.
- **Divergence handling** — if reconciliation fails, how is divergence detected and surfaced?
- **Indexes and search** — off-chain search indexes (soul-search, by alias, by local ID, by ENS) are rebuilt on state change
- **Backfill and migration** — soul-backfill scripts (`scripts/soul-backfill-m*`) exist for retroactive reconciliation; new state shapes may require new backfill

### Dimension 4: Safe-ready governance payload preparation

For on-chain mutations that should be multisig-governed:

- **Payload shape** — the Safe transaction format (target, value, data, operation)
- **Multi-signature threshold** — confirm how many signers are required for this mutation class
- **Signer coordination** — signers are notified and coordinate execution; the PR includes the prepared payload
- **Mainnet vs Sepolia** — Sepolia can be single-signer for test; mainnet non-trivial mutations require Safe-ready
- **Execution evidence** — after Safe multisig execution, the on-chain transaction is recorded in `gov-infra/evidence/` or `docs/contracts/deployments/`
- **Post-execution reconciliation** — off-chain state updates to reference the new on-chain state

### Dimension 5: Mint-signer key handling

- **Key generation** — via `scripts/generate-mint-signer-key.sh`, locally, with per-operator security policy for storage
- **Key storage** — never in git; runtime loaded from SSM / Secrets Manager
- **Key rotation** — rotation plan exists and is exercised periodically; rotation is a coordinated event
- **Key leakage response** — incident-response plan for suspected compromise includes revocation of the signer's on-chain authority and migration to a new signer
- **Multiple signers** — if multiple environments have separate mint-signers (Sepolia / mainnet), they are isolated

### Dimension 6: Soul-namespace coordination

- **`lesser-soul` publishes `spec.lessersoul.ai/ns/agent-attribution/v1`** — the public JSON-LD context
- **host's soul-registry implements the semantics** the namespace describes
- **Changes to the namespace shape or URL** require coordination with the `soul` steward
- **Versioning** — breaking changes to the namespace semantics land at `/v2`, not by mutating `/v1`

## The audit output

```markdown
## Soul-registry audit: <change name>

### Proposed change
<concrete description>

### Solidity contract changes (if applicable)
- Contract(s): <list>
- Functionality: <...>
- Access control: <...>
- Economic model impact: <...>
- Reentrancy / safety considerations: <...>
- Gas cost: <before → after>
- Hardhat test coverage: <added / existing>
- Slither findings: <resolved / allowlisted with rationale>
- solhint: <clean>

### On-chain Go code changes (if applicable)
- Signing discipline: <mint-signer loaded from SSM; never hardcoded; never logged>
- Nonce management: <...>
- Gas estimation / caps: <...>
- Idempotency / dry-run / confirmation: <...>
- Revert reason handling: <...>
- Event parsing forward-compat: <...>

### Off-chain state changes (if applicable)
- DynamoDB model update: <...>
- Source-of-truth clarity: <on-chain vs off-chain per attribute>
- Reconciliation trigger: <event listener / sweep / on-demand>
- Divergence handling: <...>
- Index / search rebuild: <...>
- Backfill required: <no / yes — script added>

### Safe-ready governance payload (if on-chain mutation)
- Multisig threshold: <...>
- Signer coordination: <planned>
- Sepolia test: <planned / completed>
- Mainnet Safe-ready payload: <prepared / to be prepared>
- Post-execution evidence: <planned>
- Off-chain reconciliation: <planned>

### Mint-signer key handling (if touched)
- Key generation flow: <unchanged / updated>
- Key storage: <SSM / Secrets Manager path unchanged>
- Rotation plan impact: <none / updated>

### Soul-namespace coordination (if namespace shape changes)
- `soul` steward coordination: <required / not required>
- Versioning: <stays at /v1 additively / moves to /v2>

### Consumer impact
- Lesser instances (agents that use the registry): <...>
- Body (uses identity tools): <...>
- Simulacrum (validates stack including registry): <...>
- External on-chain callers: <...>

### Proposed next skill
<enumerate-changes if audit clean; provision-managed-instance if registry provisioning is affected; audit-trust-and-safety if attestation or instance-auth is affected; maintain-governance-rubric if a new verifier is needed; scope-need if audit surfaces scope growth>
```

## Refusal cases

- **"Deploy this contract to mainnet single-signer; the Safe process is slow."** Refuse. Never.
- **"Skip Slither for this contract change; the findings are noise."** Refuse. Findings resolve; the verifier stays.
- **"Skip hardhat tests; we reviewed manually."** Refuse.
- **"Use a new signing key on mainnet without Sepolia validation."** Refuse.
- **"Hardcode the mint-signer key for this one CDK synth."** Refuse. Never.
- **"Log the raw mint-signer key once for debugging."** Never.
- **"Log the full signed transaction including `data` payload."** Only if redacted; sensitive call data stays out of logs.
- **"Bypass Safe-ready governance for this 'small' mutation."** Evaluate; small mutations may be acceptable single-signer on Sepolia but never on mainnet for non-trivial changes. Document rationale explicitly.
- **"Deploy a compiled contract artifact without source in the tree."** Refuse. AGPL + verifiability requires source.
- **"Skip Etherscan source verification on mainnet."** Refuse. Verifiability is part of the trust posture.
- **"Modify on-chain state directly from host via a raw RPC call without going through our documented flow."** Refuse.
- **"Delete the previous mint-signer immediately after rotation."** Plan rotation carefully — signers may have in-flight transactions; coordinate cutover.
- **"Change the JSON-LD namespace URL or semantics without coordinating with `soul`."** Refuse.

## Persist

Append every meaningful soul-registry event — contract deploy (with deploy tx, contract address, Slither/hardhat state), Safe-ready governance execution (with multisig tx, signers), mint-signer rotation, off-chain state migration. These are high-signal memory material — the historical record of on-chain / governed state is part of the soul-registry's paper trail. Include: date, network, action, evidence link.

Five meaningful entries is a lower bound for soul-registry work; on-chain and governance events are inherently memorable.

## Handoff

- **Audit clean, Solidity-only** — invoke `enumerate-changes`. Sepolia deploy and mainnet Safe-ready execution happen as separate events per roadmap.
- **Audit clean, Go-only on-chain-reaching** — invoke `enumerate-changes`.
- **Audit clean, off-chain-only reconciliation** — invoke `enumerate-changes`.
- **Audit surfaces governance-rubric need** (e.g. new verifier for contract deploys) — invoke `maintain-governance-rubric`.
- **Audit surfaces trust-API impact** — invoke `audit-trust-and-safety`.
- **Audit surfaces provisioning impact** — invoke `provision-managed-instance`.
- **Audit surfaces namespace coordination** — coordinate via `soul` steward through the user.
- **Audit surfaces scope growth** — revisit `scope-need`.
- **Audit reveals an existing bug (on-chain state diverged, Slither finding newly triggered, etc.)** — route through `investigate-issue` for root cause, then back here.
- **Audit surfaces framework awkwardness** — `coordinate-framework-feedback`.
