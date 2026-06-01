# ADR 0007: Hosted identity and ENS invariants

- Status: Accepted
- Date: 2026-06-01

## Context

Project 44 makes `lessersoul.eth` usable by both `lab` and `live` while moving persistent hosted online identity away
from any mandatory smart-contract soul assumption.

Before implementation changes fan out, host needs a short invariant record so later milestones do not accidentally:

- reuse legacy `SOUL_CHAIN_ID` / `SoulRegistry` configuration for ENS gateway chain selection;
- keep emitting bare managed ENS names such as `<name>.lessersoul.eth`;
- require a `SoulRegistry` mint before host can persist, publish, or serve managed online identity; or
- imply that x402 wallet/payment flows require a smart-contract soul.

This ADR is documentation-only. It does not perform AWS, Sepolia, mainnet, production, resolver, backfill, or
SoulRegistry actions.

## Decision

### 1. Future managed names use one shared-safe handle rule

Future user and agent names for managed hosted identity MUST be safe across all public identity projections host derives
from that name:

- ENS labels;
- ActivityPub actor/user names;
- email local-part derivation; and
- URL path segments.

Existing managed names are assumed clean for Project 44. Later validator work MUST reject future names that are not
shared-safe; it MUST NOT introduce an ENS-specific escaping, encoding, or translation layer for otherwise invalid names.

The exact validator belongs to the implementation milestone, but it must be deterministic, conservative, and shared by
every managed identity surface that consumes the handle.

### 2. Canonical managed ENS names are instance-scoped

The canonical managed ENS name form is:

```text
<name>.<instance-slug>.lessersoul.eth
```

For managed identities, the `<instance-slug>` label is part of the public identity boundary. New public channels and
artifacts MUST NOT advertise legacy bare names such as `<name>.lessersoul.eth` as canonical managed names.

Existing bare names, aliases, examples, or receipts may exist as legacy migration inputs. Later backfill/canary work must
treat them as compatibility material, not as the target shape.

### 3. ENS chain and resolver selection are independent from SoulRegistry chain selection

ENS gateway chain/resolver configuration is its own stage-owned concern:

- `lab` uses `lessersoul.eth` on Sepolia.
- `live` uses `lessersoul.eth` on Ethereum mainnet.

ENS gateway resolver sender validation, resolver addresses, gateway signing keys, and deployment runbooks MUST be keyed
by ENS stage/network configuration. They MUST NOT derive their network, resolver, or sender from legacy SoulRegistry
configuration such as `SOUL_CHAIN_ID`, `soulRegistryContractAddress*`, mint-signer settings, TipSplitter registry
addresses, or any other smart-contract soul deployment knob.

Legacy SoulRegistry configuration can continue to exist for optional on-chain registry paths. It is not an ENS source of
truth.

### 4. Hosted online identity persists in host state and public artifacts

Persistent hosted online identity is host-backed. Its source of truth is the control plane state and artifacts host owns,
including TableTheory/DynamoDB identity records, registration/channel artifacts, public identity responses, ENS gateway
material, and other documented public artifacts.

`SoulRegistry` mint state is not required for the identity to persist, resolve, or be presented as a managed hosted
identity. On-chain receipts may add assurance evidence, but lack of a mint MUST NOT create a parallel namespace, rotate
the hosted identity, or downgrade capability policy by itself.

The existing `hosted_offchain` / `immutable_onchain` vocabulary remains the right assurance vocabulary:

- `hosted_offchain` means host has persisted hosted identity state and artifacts.
- `immutable_onchain` means a mint execution has been recorded on-chain and reconciled as assurance evidence.
- assurance state is not capability authorization.

### 5. Legacy on-chain SoulRegistry paths are optional for hosted identity

The on-chain `SoulRegistry` and related paths remain available as optional or legacy assurance/lifecycle surfaces:

- mint execution and recording;
- on-chain wallet rotation;
- post-mint registration update flows that verify `SoulRegistry.getAgentWallet(agentId)`;
- TipSplitter agent-wallet resolution; and
- Safe-ready governance payloads for non-trivial on-chain mutations.

Those paths retain the normal host discipline: Sepolia before mainnet, hardhat/Slither/solhint for Solidity changes,
Safe-ready payloads for non-trivial mainnet mutations, no mint-signer key leakage, and no single-signer mainnet
shortcuts.

They do not gate persistent hosted online identity, managed ENS publication, hosted communication capability policy, or
x402 grant authority.

### 6. x402 does not require smart-contract souls

x402 payment/wallet flows may be enabled through host-scoped policy and grant state without requiring a smart-contract
soul. A successful x402 grant is still bounded by host policy, instance authentication, request hashes, usage slots,
expiry, and evidence minimization.

x402 grants MUST NOT be presented as principal/operator authority, tenant-data access, wallet authority, on-chain
authority, or proof that a `SoulRegistry` mint exists.

## Docs/spec/runbook audit result

The Project 44 M0 docs audit searched host docs/specs/runbooks for `SoulRegistry`, `on-chain`, `mint`, `ENS`,
`lessersoul.eth`, `SOUL_CHAIN_ID`, `hosted_offchain`, `immutable_onchain`, and `x402` wording.

Findings:

- `docs/soul-surface.md`, `docs/hosted-bound-soul-launch-gates.md`, `docs/portal.md`, and
  `docs/soul-agent-first-client-contract.md` already distinguish `hosted_offchain` from `immutable_onchain` and already
  say x402/capability policy is not gated by on-chain assurance.
- `docs/soul-registry.md`, `docs/runbook-sepolia-contract-deploy.md`, ADR 0002, and ADR 0003 are still accurate for the
  legacy/on-chain registry and tipping surfaces they describe. This ADR supersedes those older assumptions only for
  Project 44 managed hosted online identity and ENS configuration.
- `docs/ens-offchain-resolver.md` describes the resolver contract and current mainnet-oriented deployment shape. Later
  Project 44 resolver/runbook work must extend it for the lab Sepolia and live mainnet split; M0 intentionally performs
  no deploy or resolver mutation.
- Project 44 M2 updates the v3 fixture examples to the canonical instance-scoped managed ENS form. Any remaining bare
  `lessersoul.eth` examples are legacy, resolver-specific, or non-managed fixtures unless called out separately.

No behavior changes are made by this audit.

## `lesser-soul` public spec/docs follow-up

`lesser-soul` owns the public `spec.lessersoul.ai` docs/namespace surface, while host owns the managed hosting
implementation. The current public namespace is focused on Agent Social Attribution JSON-LD, so Project 44 should not
quietly mutate that immutable namespace in host.

Follow-up issue opened for the `soul` steward to scope any public docs/spec wording that should clarify hosted/off-chain
identity and x402 semantics:

- https://github.com/equaltoai/lesser-soul/issues/14

Until that issue lands, this host ADR is the implementation-facing source of truth for Project 44 invariants inside
`lesser-host`.

## Implementation checklist for later milestones

Later Project 44 milestones should use this checklist as their done/rollback/evidence gate.

### M1 — Split ENS config from legacy SoulRegistry config

- [ ] ENS stage/network/resolver/sender configuration is represented with ENS-specific names.
- [ ] Lab resolver sender validation is Sepolia-specific; live resolver sender validation is mainnet-specific.
- [ ] No ENS code path derives chain or resolver behavior from `SOUL_CHAIN_ID` or `soulRegistryContractAddress*`.
- [ ] Validation covers both lab and live config shapes without deploying either stage.
- [ ] Rollback preserves the previous resolver sender/config values per stage.

### M2 — Shared-safe handle validator and canonical ENS derivation

- [ ] One shared validator is used for future managed names across ENS, ActivityPub, email local-part derivation, and URL
      paths.
- [ ] The canonical derivation is exactly `<name>.<instance-slug>.lessersoul.eth`.
- [ ] Existing names are treated as already clean; no new encoding/translation layer is introduced.
- [ ] Tests cover accepted handles, rejected separator/path/email/ENS-invalid handles, and instance-scoped name
      derivation.

### M3 — Registration, provisioning, and public artifact sync

- [ ] New managed public channels advertise the instance-scoped ENS name.
- [ ] Registration/provisioning sync persists hosted identity in host state and artifacts before any optional on-chain
      assurance step.
- [ ] No SoulRegistry readiness/mint requirement blocks hosted identity, ENS publication, channel policy, or x402 grant
      eligibility.
- [ ] Consumer-facing schema examples and generated contract snapshots are updated where the public shape changes.

### M4 — Resolver deployment/runbooks

- [ ] Sepolia lab resolver deployment/runbook evidence is recorded before any live/mainnet action.
- [ ] Live/mainnet resolver actions use Safe-ready governance where required.
- [ ] Resolver sender validation is tested against the exact resolver address for each stage.
- [ ] No mainnet single-signer shortcut is used for non-trivial mutations.

### M5 — Migration, backfill, and canaries

- [ ] Backfill is idempotent and records per-identity status, errors, and retryability.
- [ ] At least one lab canary verifies the new instance-scoped ENS name, legacy compatibility posture, and fail-closed
      behavior before live/default rollout.
- [ ] Rollback can restore the previous advertised/resolver material without deleting hosted identity state.
- [ ] Evidence records which legacy bare names remain compatibility aliases and which public artifacts switched.

### M6 — Legacy on-chain soul decoupling and cleanup

- [ ] Optional/on-chain SoulRegistry paths are still safe, tested, and documented as assurance/lifecycle flows.
- [ ] Hosted identity, communication policy, and x402 grants continue to work for `hosted_offchain` identities when
      policy allows them.
- [ ] Public docs avoid wording that makes smart-contract souls mandatory for hosted online identity.
- [ ] Any `lesser-soul` public spec/docs decision is linked from the host issue/PR before Project 44 closes.

### Evidence requirements

- [ ] PRs link the relevant Project 44 issue(s) and this ADR.
- [ ] Validation output names exact commands run and explains omitted gates.
- [ ] No AWS, Sepolia, mainnet, production, or backfill action is bundled into docs-only milestones.
- [ ] On-chain-affecting milestones include hardhat/Slither/solhint evidence and Safe-ready payload records where
      applicable.
- [ ] Provisioning-affecting milestones preserve consumer release verification and tenant isolation evidence.
