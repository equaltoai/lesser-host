---
name: maintain-governance-rubric
description: Use when a change touches the gov-infra governance rubric — adding a verifier, modifying an existing one, tightening thresholds, adjusting evidence policy, bumping pack.json, updating controls matrix or threat model. Walks the rubric-impact with anti-drift discipline. Rubric changes are governance events, not ordinary code changes.
---

# Maintain the governance rubric

host's governance rubric at `gov-infra/` is the project's operational-trustworthiness substrate. It runs in CI on every PR, produces deterministic evidence, and is versioned in `pack.json` to prevent silent goalpost-shifting. When a change touches the rubric, it is a governance event — not an ordinary code change — and carries discipline that ordinary refactors don't.

This skill walks every rubric-affecting change.

## The rubric architecture (memorize)

- **`gov-infra/README.md`** — the rubric's purpose and usage
- **`gov-infra/AGENTS.md`** — agent-facing governance guidance
- **`gov-infra/pack.json`** — the versioned rubric manifest. Every meaningful change bumps the version.
- **`gov-infra/verifiers/`** — deterministic checks across categories:
  - **QUA** (quality) — linting, test coverage thresholds, build correctness
  - **CON** (contracts) — public API stability, consumer-facing shape preservation
  - **SEC** (security) — Slither on Solidity, secret-scanning, CSP validation, gosec / similar
  - **COM** (community / comms) — documentation freshness, release-notes completeness, changelog discipline
  - **CMP** (compliance) — AGPL header presence, dependency-license audit, PII-handling compliance
- **`gov-infra/evidence/`** — verifier output and artifacts. Immutable history.
- **`gov-infra/planning/`** — rubric evolution, threat model, controls matrix
- **`gov-infra/pack.json`-versioning** — anti-drift; rubric versions are named and auditable

**Grades are 0 or full points, no partial credit.** A verifier passes (evidence commits) or fails (PR does not merge). Loosening the threshold "a little" defeats the rubric's purpose.

## When this skill runs

Invoke when:

- A change **adds** a new verifier
- A change **modifies** an existing verifier's logic, threshold, or scope
- A change **removes** a verifier
- A change **adjusts evidence policy** (retention, format, scope)
- A change **bumps `gov-infra/pack.json`** version
- A change **updates the controls matrix, threat model**, or other `gov-infra/planning/` documents
- A failing verifier needs investigation — is the failure a legitimate rubric issue or a code issue?
- `scope-need` flags a change as governance-rubric-touching
- `investigate-issue` surfaces a root cause in the rubric

## Preconditions

- **The change is described concretely.** "Improve SEC rubric" is too vague; "add a verifier under `gov-infra/verifiers/sec/csp-header-strict` that validates CloudFront response-headers config for strict CSP and emits evidence at `gov-infra/evidence/sec/csp-header-strict-<commit>.json`, full points if CSP matches the expected spec, zero otherwise" is concrete.
- **MCP tools healthy**, `memory_recent` first — rubric-evolution decisions accumulate over time.
- **Current pack.json version and shape loaded in mind.**

## The five-dimension walk

### Dimension 1: Verifier identity and category

For each verifier being added / modified / removed:

- **Category** — QUA / CON / SEC / COM / CMP. Each category has different semantics; a verifier belongs to exactly one.
- **Identifier / path** — under `gov-infra/verifiers/<category>/<name>`. Naming convention: lowercase-hyphenated.
- **What it checks** — a specific claim. Good verifiers make a narrow, auditable claim. Bad verifiers check broad-vague things that invite partial-credit temptation.
- **Deterministic output** — same input → same output. Same-commit re-runs produce identical evidence.
- **Input surface** — what the verifier reads (source files, deployed state, external APIs). Verifiers that read external state are more fragile; prefer in-repo reads where possible.

### Dimension 2: Grade semantics

Every verifier produces 0 or full points:

- **Full points** — the claim holds. Evidence committed.
- **Zero points** — the claim does not hold. PR does not merge.
- **No partial credit.** A verifier that wants to produce "mostly passes" is mis-designed; split into multiple specific verifiers instead.
- **Unambiguous pass/fail** — a verifier whose interpretation is disputable is disputable. Refine until the pass/fail is unambiguous.

### Dimension 3: Evidence policy

Every verifier emits evidence on pass:

- **Format** — JSON (preferred) or a structured markdown artifact. Binary formats discouraged.
- **Location** — `gov-infra/evidence/<category>/<verifier-name>-<timestamp-or-commit>.<ext>`
- **Retention** — evidence is immutable by convention (never rewritten). New evidence appends; old evidence stays for audit.
- **Contents** — the verifier's input summary, the specific claim validated, the pass result, reproducibility info (commit SHA, tool versions).
- **Signed or attestable** (future-looking) — evidence that can be independently verified has higher value. Consider whether this specific verifier's evidence benefits from attestation.

### Dimension 4: pack.json versioning and anti-drift

For rubric-shape changes:

- **`pack.json` version bumps** — every meaningful change bumps the version. Minor for additive changes; major for semantic shifts.
- **Change documentation** — the bump commit explains what changed and why, in the body.
- **Anti-drift check** — the commit diff shows the shape-of-change clearly; reviewers can see whether this is tightening or loosening.
- **Loosening requires governance-change process** — ordinary code review is not sufficient. A loosening change is a governance event with explicit rationale, review by Aron, and (potentially) a Safe-multisig-style multi-reviewer approval.
- **Tightening is easier** but still a governance event. Documentation is part of the commit.

### Dimension 5: Failure-mode analysis

For a failing verifier (before changing it):

- **Is the failure real?** The code is genuinely out of spec; fix the code. Don't weaken the verifier.
- **Is the verifier flaky?** Same input produces inconsistent output. Fix the verifier (add determinism, stabilize inputs) — not by loosening the check.
- **Is the verifier out of date?** The world moved; the rubric didn't. Update the rubric via governance-change process — not by skipping runs.
- **Is the verifier checking the wrong thing?** Replace it with a better verifier — via governance-change process.
- **Is this a legitimate governance-rubric issue?** If yes, `investigate-issue` may surface findings that route here for rubric update.

## The audit output

```markdown
## Governance-rubric audit: <change name>

### Proposed change
<concrete description — add / modify / remove verifier; update evidence policy; bump pack.json; update controls matrix / threat model>

### Verifier identity (if applicable)
- Category: <QUA / CON / SEC / COM / CMP>
- Path: `gov-infra/verifiers/<category>/<name>`
- Claim: <the specific thing this verifier asserts>
- Input surface: <in-repo / deployed state / external API>

### Grade semantics (if applicable)
- Pass condition: <precisely>
- Fail condition: <precisely>
- Partial-credit temptation: <none — intentionally narrow>

### Evidence policy (if applicable)
- Format: <JSON / markdown / ...>
- Location: <gov-infra/evidence/...>
- Retention: <immutable append>
- Signed / attestable: <yes / no>

### pack.json impact
- Current version: <X.Y.Z>
- Proposed version: <X.Y.Z+1 or major>
- Semantic nature: <additive / tightening / loosening / restructure>
- Documentation: <change note, rationale>

### Failure-mode analysis (if responding to a failing verifier)
- Failure is real (fix code not verifier): <yes / no>
- Verifier flaky: <yes / no — fix determinism>
- Verifier out of date: <yes / no — governance update>
- Verifier wrong: <yes / no — replace via governance update>

### Anti-drift check
- Reviewers can see the shape of change clearly: <yes>
- This is a loosening: <no / yes — requires elevated governance process>
- This is a tightening: <...>
- Documentation in commit body: <confirmed>

### Consumer-of-the-rubric impact
- CI gating behavior change: <...>
- Evidence consumer impact (external auditors who read evidence): <...>
- Managed-instance operator impact (operators whose deployments are affected by the rubric): <...>

### Proposed next skill
<enumerate-changes if audit clean; scope-need if audit surfaces scope growth; investigate-issue if audit reveals a rubric bug>
```

## Refusal cases

- **"Loosen the coverage threshold from 80% to 70% for this PR; it's a big change."** Refuse. Loosening is a governance event; ordinary PR review is not sufficient.
- **"Add an exception in pack.json for this one verifier to skip for a specific PR."** Refuse. Exceptions defeat determinism.
- **"Disable the Slither verifier temporarily while we resolve findings."** Refuse. Findings resolve in the code; the verifier stays on.
- **"Skip evidence emission; the output is noisy."** Refuse. Evidence is the paper trail.
- **"Make this verifier produce 'warning' status instead of pass/fail."** Refuse. 0 or full points; nothing in between.
- **"Change pack.json version silently; the change is minor."** Refuse. Versioning is anti-drift; every meaningful change documents.
- **"Read a non-reproducible external API in this verifier for convenience."** Evaluate cautiously. External reads make verifiers fragile; prefer in-repo reads. If external is necessary, pin the API version and cache for determinism.
- **"Add a verifier that checks a broad-vague claim like 'code quality is good'."** Refuse. Narrow specific claims or nothing.

## Persist

Append when the walk surfaces something worth remembering — a verifier-design pattern that worked well, a pack.json-versioning convention decision, a governance-change-process precedent, an evidence-policy evolution, an anti-drift finding. Routine rubric maintenance that flows cleanly isn't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **Audit clean, additive / tightening** — invoke `enumerate-changes` (the verifier addition / tightening lands via normal rollout).
- **Audit clean, loosening (rare, authorized)** — invoke `enumerate-changes` with governance-change documentation referenced; elevated review.
- **Audit surfaces rubric inconsistency** (e.g. category overlap, evidence-path collision) — revisit design before enumeration.
- **Audit surfaces scope growth** — revisit `scope-need`.
- **Audit reveals an existing rubric bug** — route through `investigate-issue` for root cause, then back here.
- **Audit surfaces a framework-feedback signal** (e.g. AppTheory's MCP runtime doesn't give us the hook we'd need to implement a verifier cleanly) — invoke `coordinate-framework-feedback`.
