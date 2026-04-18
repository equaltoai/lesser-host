---
name: scope-need
description: Use when a user brings a new capability, feature request, or enhancement need for host in vague terms. Interviews conversationally and produces a scoped-need document. Applies Gate 1 (host-mission alignment), Gate 2 (narrowest scope), and Gate 3 (specialist routing) before producing output.
---

# Scope a need

A need arrives fuzzy. A feature arrives sharp. This skill is the conversation that turns fuzzy into sharp, with three specific filters: host-mission alignment, narrowest-scope discipline, and specialist-skill routing.

## Your posture

You are interviewing, not pitching. host is a managed-hosting control plane with a governance-first posture, on-chain anchoring, multi-tenant isolation, and a narrow mission: **run lesser instances responsibly for customers, anchor agent identity on-chain, underwrite operator trust via attestation + evidence, and enforce operational quality through the gov-infra rubric.**

The scoping question is always three-part:

1. **Is this host-mission work — governance, multi-tenant, on-chain, managed-provisioning, managed-updates, trust-API, consumer-release-verification, comm-routing, or operator-portal — or is it scope growth outside that mission?**
2. **If it's in-mission, what is the narrowest possible scope that preserves governance rubric, multi-tenant isolation, on-chain integrity, consumer release verification, trust-API rigor, CSP, AGPL coverage, and idiomatic framework consumption?**
3. **Does the change touch the governance rubric, provisioning / managed-update, soul registry, trust API / CSP / instance-auth, or framework consumption? If yes, route to the appropriate specialist skill before enumeration.**

The default for security, multi-tenant, on-chain-integrity, governance, trust-API, release-verification, operational-reliability, AGPL, and framework-feedback work is "yes, evaluate at Gate 2." The default for net-new capability outside the mission is "no."

## Start with memory and the architecture

- **Read `README.md`, `AGENTS.md`, `gov-infra/README.md`, `gov-infra/AGENTS.md`, and `docs/`** for canonical architecture and contracts.
- `memory_recent` — has this need or adjacent work been scoped before?
- `query_knowledge` — do AppTheory / TableTheory / FaceTheory or sibling equaltoai repos already cover this concept?

If tools are unavailable, surface and ask the user to re-auth.

## The interview

Ask, one or two at a time:

1. **Who is asking and why now?** Customer / operator report, security finding, CVE, compliance requirement, advisor-dispatched brief, or Aron-direct?
2. **What problem does it solve?** Current pain, not speculative improvement.
3. **Which surface does it touch?**
   - Control-plane API / portal
   - Trust API / attestations / instance-auth
   - Soul registry (on-chain + off-chain)
   - Provisioning worker / managed-update workers
   - AI worker / comm worker / render worker / soul-reputation worker
   - Email ingress
   - Governance rubric / verifier / evidence
   - CDK / IaC / CloudFront / DynamoDB
   - `contracts/` (Solidity) / Hardhat / Safe-ready payloads
   - `web/` SPA / CSP
   - AGPL / licensing
   - Framework consumption pattern
4. **Which consumers are affected?** Managed-instance operators, customers (portal), sibling repos (lesser / body / soul / greater), on-chain actors, external vendors (Stripe, AI providers), trust-API public readers.
5. **Is this a tenant-isolation-affecting change?** If yes, elevated scrutiny.
6. **Is this an on-chain or Safe-ready-governance change?** If yes, route to `evolve-soul-registry`.
7. **Is this a governance-rubric change?** (Adding a verifier, tightening thresholds, changing evidence policy) route to `maintain-governance-rubric`.
8. **Is this a provisioning / managed-update change?** (Including consumer release verification) route to `provision-managed-instance`.
9. **Is this a trust-API / CSP / instance-auth change?** Route to `audit-trust-and-safety`.
10. **Is this framework-awkward?** Route to `coordinate-framework-feedback`.
11. **What does success look like?** Observable, testable.
12. **What is explicitly out of scope?**

## The three gating questions

### Gate 1: Is this host-mission work?

Eight possible verdicts:

1. **Yes — security / tenant-isolation / on-chain-integrity work.** CVE response, isolation bug, on-chain miscalculation fix. Always accepted. Proceed to Gate 2.
2. **Yes — governance-rubric work.** New verifier, tightened threshold, evidence-policy refinement. Proceed; route through `maintain-governance-rubric`.
3. **Yes — provisioning / managed-update work.** New provisioning step, managed-update recovery improvement, consumer-release-verification refinement. Proceed; route through `provision-managed-instance`.
4. **Yes — soul-registry work.** New contract function, Safe-ready payload pattern, off-chain state migration. Proceed; route through `evolve-soul-registry`.
5. **Yes — trust-API / CSP / instance-auth work.** New attestation type, tightened auth, CSP refinement, safety evidence service. Proceed; route through `audit-trust-and-safety`.
6. **Yes — operational-reliability / AGPL / observability work.** Latency, availability, observability for observed gaps, rate-limiting, license vetting, CVE-response hardening. Accepted.
7. **Yes — framework-feedback work.** A host concern surfaces AppTheory / TableTheory / FaceTheory awkwardness. Route through `coordinate-framework-feedback`.
8. **No — out-of-scope growth.** Tenant-side data operations, tenant user management, tenant content moderation, general identity-provider scope for non-lesser consumers, general payments processing beyond tipping and host billing. Produces a redirect document.

### Gate 2: What is the narrowest possible scope?

Prefer:

- Bug fixes scoped to the specific reported symptom
- Governance-rubric additions (new verifier) over modifications (loosening an existing one)
- Additive provisioning steps that preserve existing flows
- On-chain additions (new function on existing contract) over replacements; Safe-ready-governance-coordinated for non-trivial changes
- Trust-API additions (new attestation type) over modifications of existing shapes
- CSP additions (new `'self'`-origin surface) over loosening (new third-party origin)
- Dependency bumps within current major versions

Avoid:

- Refactors "while we're in there"
- New verifiers that aren't deterministic or don't produce evidence
- Provisioning-pipeline rewrites; incremental improvements preferred
- On-chain refactors without clear upgrade path
- Trust-API breaking changes
- CSP loosening ever
- Cross-tenant queries in host's DynamoDB
- Reading tenant content into host's plane

### Gate 3: Specialist routing

If the change touches any of these, the specialist skill runs before enumeration:

- **Governance rubric, verifier, evidence, pack.json** → `maintain-governance-rubric`
- **Provisioning, managed-update, consumer release verification** → `provision-managed-instance`
- **Soul registry (on-chain + off-chain + governance)** → `evolve-soul-registry`
- **Trust API, CSP, instance-auth, attestations** → `audit-trust-and-safety`
- **Framework awkwardness** → `coordinate-framework-feedback`
- **Advisor-dispatched brief** → `review-advisor-brief`

## Output: the scoped-need document

### For Gate 1 verdict "host-mission work":

```markdown
# Scoped Need: <short name>

## Background
<one paragraph>

## Driver
<operator / customer / CVE / compliance / advisor-dispatched / Aron-direct>

## Problem
<what is broken, missing, or painful today>

## Surface affected
<control-plane / trust-API / soul / provisioning / managed-update / worker / email-ingress / gov-infra / CDK / contracts / web / AGPL / framework>

## Lambda(s) affected
<enumerated, or "none — infrastructure / gov-infra / contracts only">

## Classification
<security / tenant-isolation / on-chain-integrity / governance / provisioning / managed-update / soul-registry / trust-API / CSP / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>

## Narrowest-scope proposal
<smallest change that addresses the need>

## What this need explicitly does not cover
<bounded scope>

## Success criteria
<observable, testable>

## Specialist routing
- Governance rubric: <not touched / walk via maintain-governance-rubric>
- Provisioning / managed-update / release verification: <not touched / walk via provision-managed-instance>
- Soul registry: <not touched / walk via evolve-soul-registry>
- Trust API / CSP / instance-auth: <not touched / walk via audit-trust-and-safety>
- Framework consumption: <idiomatic / awkwardness via coordinate-framework-feedback>
- Advisor brief: <n/a / review via review-advisor-brief>

## Consumer impact
<managed operators / customers / sibling repos / on-chain actors / external vendors>

## Multi-tenant isolation impact
<none / elevated scrutiny required — document>

## On-chain impact
<none / off-chain only / on-chain — Safe-ready path>

## AGPL posture
<no change / confirmed AGPL-compatible / decision required>

## Open questions
<unresolved>
```

### For Gate 1 verdict "out-of-scope growth":

```markdown
# Redirect: <short name>

## Background
<what was asked>

## Why this doesn't belong in host
<scope bounded to managed-hosting control plane; this is X, which belongs in Y>

## Appropriate owner
<tenant-side lesser / body / soul / greater / Theory Cloud framework / separate repo / scoping with Aron>

## Path for the requesting user
<rough outline>

## Recommended next step
<specific handoff>
```

## Persist before handoff

Append only when scoping surfaces a recurring pattern — a redirect category, a governance-rubric evolution signal, a multi-tenant-isolation pattern, an on-chain coordination pattern. Routine completions aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **In-mission, specialist walk required** — invoke the appropriate specialist skill before enumeration.
- **In-mission, none of the above** — invoke `enumerate-changes` directly.
- **Advisor-dispatched scope** — `review-advisor-brief` already ran; output includes Aron's authorization.
- **Out-of-scope** — redirect document is the handoff.
- **Resolved to "no change needed"** — record and stop.
- **User defers** — record and stop.
