# ADR 0001: Component Placement (Contracts vs APIs vs Instance Routing)

- Status: Accepted (amended 2026-03-04)
- Date: 2026-02-21 (amended 2026-03-04)

## Context

The term “soul” is used across the EqualtoAI workspace for multiple features. `lesser-soul/SPEC.md` defines
**lesser-soul as the agent identity/reputation/validation registry layer** (EIP-8004 identity registry + off-chain
registries).

To avoid duplication and drift, we must explicitly define where each responsibility lives, and what is considered
source-of-truth.

## Decision

### 1) Smart contracts (EVM)

**Location:** `lesser-host/contracts/` (same place TipSplitter lives)

- Contracts MUST treat `agentId` as an opaque `uint256` input and MUST NOT implement domain/local string normalization.
- Contracts MUST provide the minimum on-chain anchor surface:
  - `getAgentWallet(agentId)` (EIP-8004 compatibility)
  - wallet rotation verification
  - attestation roots for reputation/validation
- Contracts MUST be non-upgradeable; new versions deploy new addresses and consumers are updated via Safe.

### 2) Registry APIs + persistence (control plane)

**Location:** `lesser-host/cmd/control-plane-api` (AppTheory HTTP) + `lesser-host/internal/store/models` (TableTheory)

- The registry API is served through the existing `lesser-host` distribution under `/api/v1/soul/*`.
- APIs MUST implement:
  - `normalizedDomain` normalization
  - `localAgentId` normalization/validation
  - `agentId` derivation
  - signature verification, proof verification, and Safe-ready operation creation
- **All off-chain durable state MUST be stored using TableTheory models** in the existing state table (no raw DynamoDB
  client usage for app state).

### 3) Instance integration (proofs + optional MCP)

**Location:** `lesser` (well-known proof surface) + `lesser-body` (optional MCP runtime)

- Instances MUST expose the soul proof surface used during registration (DNS TXT + HTTPS well-known).
- MCP runtime is provided by `lesser-body` (managed default) and is intentionally decoupled from the soul registry.
- The registry API (`/api/v1/soul/*`) remains hosted in `lesser-host`.

## Consequences

- One canonical implementation of normalization + `agentId` derivation exists in the control plane (and is reused in
  tests/fixtures).
- Contracts stay simple and chain-agnostic; string normalization and proof checks stay off-chain.
- Instance-side services (e.g., MCP) can evolve independently without changing the registry API location or persistence
  model.
