# ADR 0003: Suspension Policy (Off-chain enforcement)

- Status: Accepted
- Date: 2026-02-21

## Context

The soul registry needs a way for operators to temporarily block abusive or compromised agents from being discovered and
from accruing reputation, without requiring per-agent on-chain state changes.

Suspension must be:

- **Fast** (operator-controlled, no Safe transaction required)
- **Auditable** (every action is recorded)
- **Compatible** with on-chain identity anchoring (ERC-721 soul token + `getAgentWallet(agentId)` for TipSplitter)

## Decision

### 1) Suspension is an off-chain policy flag

Suspension state is stored in the control plane DynamoDB record:

- `PK: SOUL#AGENT#{agentId}`
- `SK: IDENTITY`
- `status: active | suspended | pending`

There is **no per-agent on-chain suspension** in v1. The `ISoulRegistry` contract remains the source of truth for the
wallet binding and token existence.

### 2) Effects of suspension

When `status == "suspended"`:

- **Discovery/search MUST exclude** the agent in public search responses (`/api/v1/soul/search`) and any other discovery
  surfaces by default.
- **Reputation accrual MUST stop**: scheduled reputation recomputation/jobs must skip suspended agents (no new score
  updates) until reinstated.
- **Direct reads MAY still work**: public `GET /api/v1/soul/agents/{agentId}` and `GET .../registration` should return
  the agent with `status: "suspended"` (unless a later milestone defines a stronger blocking policy).

### 3) Writes while suspended (portal lifecycle)

To fail closed and avoid policy circumvention:

- Portal lifecycle writes that modify agent-facing metadata (e.g., `update-registration`) or initiate wallet lifecycle
  actions (e.g., rotation begin/confirm) MUST be rejected for suspended agents.

Operators MAY reinstate an agent; reinstatement is the only path back to an active lifecycle.

### 4) Reinstatement

Reinstatement is an operator action that sets `status` back to `"active"`. It does not require any on-chain operation.

Recording execution of unrelated operations (e.g., a mint receipt) MUST NOT implicitly reinstate a suspended agent.

### 5) Auditing

All suspend/reinstate actions are authenticated as operator/admin actions and recorded in the audit log
(`AuditLogEntry`), including:

- actor identity
- action (`soul.agent.suspend`, `soul.agent.reinstate`)
- target (`soul_agent_identity:{agentId}`)
- request id + timestamp

## Consequences

- Suspension can be applied instantly and consistently across discovery and reputation systems.
- The on-chain registry remains minimal and chain-agnostic.
- Portal lifecycle actions for suspended agents are explicitly blocked; operators control reinstatement.
