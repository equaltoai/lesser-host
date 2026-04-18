---
name: coordinate-framework-feedback
description: Use when building or maintaining host surfaces framework awkwardness — an AppTheory middleware / construct limitation, a TableTheory query-builder friction, a FaceTheory pattern gap (for `web/`). Produces a cleanly-shaped signal for the relevant Theory Cloud framework steward rather than a local patch. host's role as a high-governance, multi-tenant, multi-worker consumer means framework-consumption awkwardness here is scope-evidence for framework evolution under the harder constraints.
---

# Coordinate framework feedback

host consumes AppTheory, TableTheory, and (where applicable) FaceTheory in demanding contexts — a control-plane API with governance-rubric CI enforcement, a trust API with strict CSP, a multi-worker provisioning pipeline with on-chain integration. When consuming the frameworks is awkward under these conditions, that awkwardness is especially high-signal for framework evolution — the stress-test constraint is real.

This skill handles the signal cleanly. It walks the awkwardness, separates "host is expressing the concern wrong" from "the framework has a genuine gap under host's constraints," and produces a shaped report for the relevant framework steward.

## The frameworks host consumes

- **AppTheory v0.19.1** — Lambda runtime, middleware chain, CDK constructs, MCP server runtime (where applicable). Steward: Theory Cloud AppTheory steward.
- **TableTheory v1.5.1** — DynamoDB ORM, single-table tag semantics, used for control-plane state, trust attestations, soul-registry off-chain state. Steward: Theory Cloud TableTheory steward.
- **FaceTheory patterns** in `web/` (where consumed) — Svelte 5 SSR / SSG / ISR concerns. Steward: Theory Cloud FaceTheory steward.

## When this skill runs

Invoke when:

- A handler pattern would require an AppTheory middleware hook that doesn't exist
- A control-plane flow would require an AppTheory runtime feature that isn't supported
- A CDK construct pattern forces a workaround or duplication
- A TableTheory query would require a tag semantic, query-builder capability, or lifecycle event that isn't supported
- A multi-worker pattern (SQS-driven + DynamoDB Streams-driven + event-driven) surfaces a friction
- A `web/` pattern in Svelte 5 surfaces a FaceTheory gap
- A governance-rubric verifier would require a framework feature to emit deterministic evidence
- `scope-need` flags a change as framework-awkward
- `investigate-issue` surfaces a root cause in a framework

## Preconditions

- **The awkwardness is described concretely.** "AppTheory is hard to use in host" is too vague; "AppTheory's middleware chain doesn't give a way to emit structured audit events per-handler tied to the request's tenant context, forcing host to manually wire audit emission in every handler — 20 lines of boilerplate per handler where an audit-middleware hook would be 3" is concrete.
- **The idiomatic attempt is captured.** What would the code look like if the framework supported the concern cleanly?
- **The current workaround (if any) is captured.** What does host currently do? Cost of the workaround?
- **MCP tools healthy**, `memory_recent` first — prior framework-feedback signals matter.

## The three-step walk

### Step 1: Is host expressing the concern wrong?

Before assuming framework limitation:

- **Idiomatic framework usage**: what does AppTheory / TableTheory / FaceTheory offer? Consult `query_knowledge` against the framework knowledge base.
- **Alternative patterns**: different way to express the same concern?
- **Recent framework versions**: the pinned version may lag current capability.

If host's usage is bent rather than idiomatic, the fix is local: reshape host's code. Proceed to `scope-need` for the local change.

### Step 2: Is the framework genuinely limiting?

Characterize the gap:

- **The concern, concretely**
- **The ideal framework support**
- **The current gap** — specifically what is missing (middleware hook, construct pattern, query-builder capability, tag semantic, lifecycle event)
- **The workaround shape (if any)** — code complexity, test burden, performance impact, maintenance drag, governance-rubric impact (if the workaround makes evidence emission awkward)
- **The scope of the gap** — specific to host's governance / multi-tenant / multi-worker context, or broader? host's stress-test context is a specific constraint; the gap may or may not apply to lighter consumers.

### Step 3: Shape the signal for the framework steward

Produce:

```markdown
## Framework-feedback signal: <short name>

### Target framework
<AppTheory / AppTheory MCP runtime / AppTheory CDK constructs / TableTheory / FaceTheory>

### Framework version in use
<pinned version>

### The concern (under host's constraints)
<one-to-two sentences; note the host-specific constraint context — governance-rubric / multi-tenant / multi-worker / on-chain-integration / strict-CSP>

### The idiomatic code host would write if the framework supported it
```<language>
// Code sketch
```

### The current workaround in host (or "blocked")
```<language>
// Current code with comments on why awkward
```

### Cost of the workaround
- Code complexity: <...>
- Test burden: <...>
- Performance impact: <...>
- Maintenance drag: <...>
- Governance-rubric impact (does the workaround make evidence emission or verifier-implementation awkward?): <...>

### Scope of the gap
- Specific to host's constraints: <governance / multi-tenant / multi-worker / on-chain / strict-CSP>
- Likely broader (other consumers would benefit): <yes / no>
- Other known consumers affected: <list from query_knowledge>

### Host's workaround posture
- Continue workaround while framework evolves: <yes / no>
- Workaround is temporary / awaits framework: <yes / no>
- Governance-rubric allows the workaround (verifier can still emit evidence): <yes / no>

### Proposed next step
<framework steward scopes via the framework's own scope-need flow; host's steward does not patch the framework locally>
```

Report goes to the framework steward through the user.

## The explicit refusal to patch locally

Absolute:

- **No monkey-patches** to AppTheory runtime, middleware, MCP runtime, CDK constructs in host's tree
- **No forked copies** of TableTheory query builder, tag handling, or session helpers
- **No FaceTheory workarounds in `web/`** that vendor its patterns
- **No "temporary" framework overrides**
- **No pinning to unreleased framework commits**
- **No vendoring** framework code into `internal/`

If the framework genuinely blocks critical work, escalate to Aron. The decision to prioritize framework evolution, accept a workaround, or rethink host's approach is scope-level, not steward-level.

## The governance-rubric interplay

host's governance rubric runs in CI. A framework gap that makes evidence emission awkward or verifier implementation fragile is a governance-rubric risk, not just a code-complexity issue. Flag this explicitly when it applies — the gap affects more than engineering velocity.

## The continuity discipline

Framework-feedback signals accumulate:

- **Record in memory** — target framework, concern summary, signal sent, date
- **Track the framework steward's response** — scoped need, feature release, decline, redirect
- **Revisit on framework version bumps** — when host bumps AppTheory / TableTheory / FaceTheory, check whether pending signals are addressed
- **Duplicate-signal discipline** — before sending, check memory; don't re-send a signal already under review

## Refusal cases

- **"Patch this AppTheory middleware locally; the framework steward will get around to it."** Refuse.
- **"Fork TableTheory's query builder for a one-off optimization."** Refuse.
- **"Skip the framework-feedback signal; we need this to ship."** Refuse. Signal is asynchronous; host's local work continues via documented workaround.
- **"Send a framework-feedback signal for every minor awkwardness."** Refuse. Genuine gaps only.
- **"Copy an AppTheory construct into host's tree and modify it."** Refuse.
- **"The framework steward isn't responsive, so we should fork."** Escalate to Aron. Forking is scope-level.
- **"Our constraint is unique (governance-rubric); the framework doesn't need to accommodate it."** Evaluate. If the constraint is truly unique, workaround in host may be appropriate. If the constraint is broader (other governance-aware consumers would benefit), the signal is valid.

## Persist

Append every framework-feedback signal — target framework, concern, date, response. High-signal memory material because the flagship-consumer feedback loop is part of why host exists as an open-source example of Theory Cloud stack in demanding conditions.

Five meaningful entries is the right scale.

## Handoff

- **Signal shaped and sent** — stop. Record and continue host's local work through normal pipeline.
- **Signal reveals host is using the framework wrong** — route through `scope-need` for local change.
- **Signal is a duplicate** — don't re-send; update memory with additional data point.
- **Signal reveals a framework bug (not a gap)** — report as a bug to the framework steward, not as scoping.
- **Signal reveals cross-framework impact** — coordinate via multiple framework stewards.
- **Signal reveals governance-rubric interplay** — flag explicitly in the signal; may warrant a rubric evolution in parallel (`maintain-governance-rubric`).
