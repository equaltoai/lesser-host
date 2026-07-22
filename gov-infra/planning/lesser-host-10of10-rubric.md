# lesser-host: 10/10 Rubric (Quality, Consistency, Completeness, Security, Compliance Readiness, Maintainability, Docs)

This rubric defines what “10/10” means and how category grades are computed. It is designed to prevent goalpost drift and
“green by dilution” by making scoring **versioned, measurable, and repeatable**.

## Versioning (no moving goalposts)
- **Rubric version:** `v0.1.13` (2026-07-22)
- **Comparability rule:** grades are comparable only within the same version.
- **Change rule:** bump the version + changelog entry for any rubric change (what changed + why).

### Changelog
- `v0.1.13`: Tighten SEC-15/SEC-16 for Project 48 M11 typed declaration construction (`#977`): the AppTheory MicroVM is the only Hosted Genesis declaration producer; aiworker remains transport-only; five section tools carry exact candidate revision/hash bindings and reject unknown fields; accepted candidates and provider evidence checkpoint through guarded TableTheory transactions; deterministic review recovery and structurally bound affirmation lead to exact provider-free finalization; production transcript extraction, extraction target states, phrase-only affirmation, and fallback lanes stay deleted. Additive tightening; no legacy lane, raw SDK, content logging, exception, or skip.
- `v0.1.12`: Tighten SEC-16 after the Project 48 M11 lab actor froze at the 300-second AWS endpoint-idle boundary: Hosted Genesis must omit `ProviderIdlePolicy` for asynchronous in-VM provider work, prove AppTheory execution-role/default-log-group wiring, retain a ten-second content-free heartbeat and persistence boundaries, classify suspended accepted work as unable to complete without resume, and preserve exact exhausted retry guidance through the guarded TableTheory failure transaction. Additive tightening; no raw SDK, polling keepalive, fallback lane, manual controller mutation, content logging, exception, or skip.
- `v0.1.11`: Add SEC-16 hosted-genesis provider-telemetry and terminal-convergence guard for Project 48 M11 (`#959`): OpenAI/Anthropic SDK streaming and declaration extraction must emit content-free per-event telemetry, declaration request/output size+SHA256 metadata, and periodic heartbeats; whole provider calls must be deadline-bound and durably fail accepted turns; normal client reads may observe AppTheory's canonical MicroVM GET lifecycle but never nudge work; and terminal/failed/max-duration state must converge through exact status/version/turn-guarded TableTheory writes. Additive tightening; no raw content; no fallback lane; no exception; no skip.
- `v0.1.10`: Record the narrowly reviewed Project 17 M15 SEC-10 event (`#960` / governance `#964` / PR #965): the exact full locked-file semantic diff raises the managed Body compatibility floor to v1.0.8 and aligns fail-closed three-phase release fixtures while preserving mandatory exact-version, checksum, manifest, template, and auxiliary-asset verification. Any additional locked-file drift changes the fingerprint and fails. Additive hardening; no loosening; no exception; no skip.
- `v0.1.9`: Add SEC-15 hosted-genesis declaration-contract guard so hosted-genesis declaration production is five-body-only across deployed Host Go code: the generic legacy selector/wrapper vocabulary (`LegacyDeclarationContract`, `DeclarationContractFromVersions`, the no-contract prompt wrapper `MintConversationSystemPrompt(`) and the permissive `DeclarationContractFromEnv` selector must stay deleted everywhere in deployed Go code (not only fresh runtime paths); runtime paths must resolve through the fail-closed `RequireFiveBodyDeclarationContractFromEnv`/`ParseFiveBodyDeclarationContract` selectors; and a missing/unknown contract must surface as `operator_action_required` (never the legacy `boundaries.required` lane). Additive tightening; no loosening; no exception; no skip.
- `v0.1.8`: Add SEC-14 hosted-genesis MicroVM registry-boundary guard so deployed Host code cannot persist AppTheory generic `SessionRegistryRecord`, call `NewTableTheorySessionRegistry`, or bypass the Host-owned `HostedGenesisMicroVMExecution` cache adapter/reconstruction boundary.
- `v0.1.7`: Replace the SEC-8 Lesser instance-key mint-conversation SSE-route guard with the M4 durable-session invariant: the exact Lesser-used instance-key registration mint-conversation route must be present and pinned to the JSON control-plane origin while legacy operator/UI streaming routes remain pinned to the SSE-capable origin.
- `v0.1.6`: Tighten SEC-8 CloudFront composition verification so the exact Lesser-used instance-key registration mint-conversation SSE route must be present and pinned to the SSE-capable control-plane origin. Superseded for the Lesser instance-key route by `v0.1.7`.
- `v0.1.5`: Add CMP-4 bounded soul comm mailbox controls verifier for the approved host-authoritative mailbox content/state exception.
- `v0.1.4`: Extend CON-3 to include lesser-host REST contract parity for the soul mint-conversation surface and the generated adapter/SSE companion artifacts.
- `v0.1.3`: Extend CON-2 to include Solidity lint (solhint) and SEC-1 to include Solidity SAST (Slither) via the rubric verifier.
- `v0.1.2`: Implement remaining planned verifiers for CON-3/COM-6/SEC-4/MAI-1/MAI-3 and update rubric text to reference the verifier evidence logs (requirements unchanged).
- `v0.1.1`: Update SEC-2 verification to use `govulncheck -mode=binary` on shipped binaries due to an SSA panic in `govulncheck ./...` symbol scanning for a generic + variadic edge case (requirement unchanged).
- `v0.1`: Initial governance scaffold for lesser-host (multi-language: Go + TypeScript + CDK + Solidity) with strict anti-drift items.

## Scoring (deterministic)
- Each category is scored **0–10**.
- Point weights sum to **10** per category.
- Requirements are **pass/fail** (either earn full points or 0).
- A category is **10/10 only if all requirements in that category pass**.

## Verification (commands + deterministic artifacts are the source of truth)
Every rubric item has exactly one verification mechanism:
- a command, or
- a deterministic artifact check (required doc exists and matches an agreed format).

Enforcement rule (anti-drift):
- If an item’s verifier is a command/script, it only counts as passing once it runs in CI and produces evidence under `gov-infra/evidence/`.

---

## Quality (QUA) — reliable, testable, change-friendly
| ID | Points | Requirement | How to verify |
| --- | ---: | --- | --- |
| QUA-1 | 4 | Unit tests stay green | `go test -count=1 ./...` |
| QUA-2 | 3 | Integration/contract tests stay green (multi-language) | `(cd web && npm ci && npm test) && (cd contracts && npm ci && npm test) && (cd cdk && npm ci && npm run build && npx cdk synth -c stage=lab)` |
| QUA-3 | 3 | Coverage ≥ 80% (no denominator games) | `go test ./... -coverprofile=gov-infra/evidence/coverage.out` + threshold check (implemented in `gov-infra/verifiers/gov-verify-rubric.sh`) |

**10/10 definition:** QUA-1 through QUA-3 pass.

## Consistency (CON) — one way to do the important things
| ID | Points | Requirement | How to verify |
| --- | ---: | --- | --- |
| CON-1 | 3 | gofmt clean (no diffs) | `test -z "$(gofmt -l . | sed '/^$/d')"` |
| CON-2 | 5 | Lint/static analysis green (pinned version) | `golangci-lint run --timeout=10m` + `web` lint/typecheck + `cdk` build + `contracts` solhint (implemented in verifier; see `gov-infra/evidence/CON-2-output.log`). |
| CON-3 | 2 | Public boundary contract parity (if applicable) | TipSplitter ABI parity must hold, and the lesser-host soul mint-conversation REST contract must stay complete across `docs/contracts/openapi.yaml`, `docs/contracts/soul-mint-conversation-sse.json`, and the checked-in generated adapter (implemented in verifier; see `gov-infra/evidence/CON-3-output.log`). |

**10/10 definition:** CON-1 through CON-3 pass (or document why CON-3 is N/A and remove it with a version bump).

## Completeness (COM) — verify the verifiers (anti-drift)
| ID | Points | Requirement | How to verify |
| --- | ---: | --- | --- |
| COM-1 | 2 | All modules compile (no “mystery meat”) | Compile every `go.mod` module (implemented in verifier; see `gov-infra/evidence/COM-1-output.log`) |
| COM-2 | 2 | Toolchain pins align to repo (Go/Node versions + security tools pinned) | Enforce toolchain pins (Go/Node) + pinned security tools (implemented in verifier; see `gov-infra/evidence/COM-2-output.log`). |
| COM-3 | 2 | Lint config schema-valid (no silent skip) | `golangci-lint config verify --config .golangci.yml` (implemented in verifier; see `gov-infra/evidence/COM-3-output.log`). |
| COM-4 | 2 | Coverage threshold not diluted (≥ 80%) | Rubric doc must continue to state 80% (implemented in verifier; see `gov-infra/evidence/COM-4-output.log`) |
| COM-5 | 1 | Security scan config not diluted (no excluded high-signal rules) | Enforce policy on gosec excludes (implemented in verifier; see `gov-infra/evidence/COM-5-output.log`) |
| COM-6 | 1 | Logging/operational standards enforced (if applicable) | Enforce structured logging + observability wiring + ban fmt/log Print* in non-test Go sources (implemented in verifier; see `gov-infra/evidence/COM-6-output.log`). |

**10/10 definition:** COM-1 through COM-6 pass.

## Security (SEC) — abuse-resilient and reviewable
| ID | Points | Requirement | How to verify |
| --- | ---: | --- | --- |
| SEC-1 | 3 | Static security scan green (pinned version) | `golangci-lint run --timeout=10m` with `gosec` enabled + `slither` on `contracts` (implemented in verifier; see `gov-infra/evidence/SEC-1-output.log`). |
| SEC-2 | 3 | Dependency vulnerability scan green | `govulncheck -mode=binary` on shipped `cmd/*` binaries (implemented in verifier; see `gov-infra/evidence/SEC-2-output.log`). |
| SEC-3 | 2 | Supply-chain verification green | GitHub Actions must be pinned by full commit SHA (no tag, branch, SemVer, or expression refs in `uses:`); the verifier self-tests normal step, quoted-key, flow-mapping, and reusable-workflow syntax; Node lifecycle hooks scanned with scripts-disabled installs; Go/Python metadata scans (implemented in verifier; see `gov-infra/evidence/SEC-3-output.log`) |
| SEC-4 | 2 | Domain-specific P0 regression tests (security critical paths) | Run `TestP0_*` regression suite (bootstrap/authz invariants, SSRF defense, instance auth) (implemented in verifier; see `gov-infra/evidence/SEC-4-output.log`). |

Additional security anti-drift gates are enforced by the same verifier entrypoint. SEC-10 locks the complete Consumer release verification surface and accepts a non-empty semantic diff only when its full locked-file fingerprint is an explicitly reviewed governance event; it emits `gov-infra/evidence/SEC-10-output.log`. SEC-14 specifically locks the hosted-genesis MicroVM registry boundary to Host-owned `HostedGenesisMicroVMExecution` cache persistence plus `HostedGenesisSession` reconstruction, and emits `gov-infra/evidence/SEC-14-output.log`. SEC-15 locks Hosted Genesis declaration production to the five-body contract across deployed Host Go code: no deployed Go file may define or reference the generic legacy selector/wrapper vocabulary, only the MicroVM workload may select/build declarations, aiworker remains transport-only, and a missing/unknown contract must surface as `operator_action_required`; it emits `gov-infra/evidence/SEC-15-output.log`. SEC-16 locks phase-local typed candidate construction, strict revision/hash tool bindings, deterministic review/affirmation/finalization, content-free OpenAI/Anthropic SDK telemetry, the ten-second provider heartbeat, the no-`ProviderIdlePolicy` asynchronous AppTheory run shape, wait-only suspended/terminal lifecycle convergence, and exact status/version/turn/candidate-guarded TableTheory writes; it emits `gov-infra/evidence/SEC-16-output.log`.

SEC-1 note: Slither runs from a repo-local Python venv under `gov-infra/.tools/` (created/managed by the verifier); do not rely on a system-wide Slither install.

**10/10 definition:** SEC-1 through SEC-4 pass.

## Compliance Readiness (CMP) — auditability and evidence
| ID | Points | Requirement | How to verify |
| --- | ---: | --- | --- |
| CMP-1 | 3 | Controls matrix exists and is current | File exists: `gov-infra/planning/lesser-host-controls-matrix.md` |
| CMP-2 | 2 | Evidence plan exists and is reproducible | File exists: `gov-infra/planning/lesser-host-evidence-plan.md` |
| CMP-3 | 2 | Threat model exists and is current | File exists: `gov-infra/planning/lesser-host-threat-model.md` |
| CMP-4 | 3 | Bounded soul comm mailbox controls are explicit | `bash gov-infra/verifiers/gov-verify-rubric.sh` checks ADR 0005, the soul surface, roadmap, threat model, controls matrix, and evidence plan for retention, encryption, access audit, list/content split, no semantic-memory role, hash-only instance auth, write-once audit/events, protected identity/provenance fields, and body/lesser non-authority. The gate rejects explicit insecure variants (optional retention/encryption/audit, full-content list responses, raw-key/plaintext fallback, and cross-tenant mailbox search/analytics) instead of relying on positive keyword presence alone. |

**10/10 definition:** CMP-1 through CMP-4 pass.

## Maintainability (MAI) — convergent codebase (recommended for AI-heavy repos)
| ID | Points | Requirement | How to verify |
| --- | ---: | --- | --- |
| MAI-1 | 3 | File-size/complexity budgets enforced | Enforce max line-count budgets (Go + TS/JS) with explicit excludes for generated and `.d.ts` (implemented in verifier; see `gov-infra/evidence/MAI-1-output.log`). |
| MAI-2 | 2 | Maintainability roadmap current | Roadmap exists and contains no template tokens (implemented in verifier; see `gov-infra/evidence/MAI-2-output.log`) |
| MAI-3 | 2 | Canonical implementations (no duplicate semantics) | Enforce singleton canonical HTTP helpers under `internal/httpx` (ParseJSON/BearerToken/FirstHeaderValue/FirstQueryValue) (implemented in verifier; see `gov-infra/evidence/MAI-3-output.log`). |
| MAI-4 | 3 | CI runs `bash gov-infra/verifiers/gov-verify-rubric.sh` and fails on non-PASS | CI config scan (implemented in verifier; see `gov-infra/evidence/MAI-4-output.log`) |

**10/10 definition:** MAI-1 through MAI-4 pass.

## Docs (DOC) — integrity and parity
| ID | Points | Requirement | How to verify |
| --- | ---: | --- | --- |
| DOC-1 | 2 | Threat model present | File exists: `gov-infra/planning/lesser-host-threat-model.md` |
| DOC-2 | 2 | Evidence plan present | File exists: `gov-infra/planning/lesser-host-evidence-plan.md` |
| DOC-3 | 2 | Rubric + roadmap present | Files exist under `gov-infra/planning/` |
| DOC-4 | 2 | Doc integrity (no template tokens; required artifacts present) | `bash gov-infra/verifiers/gov-verify-rubric.sh` (DOC-4) |
| DOC-5 | 2 | Threat ↔ controls parity | `bash gov-infra/verifiers/gov-verify-rubric.sh` (DOC-5 parity check) |

**10/10 definition:** DOC-1 through DOC-5 pass.


## Maintaining 10/10 (recommended CI surface)
Minimal commands CI should run on protected branches:

```bash
# Full governance surface (writes gov-infra/evidence/*)
bash gov-infra/verifiers/gov-verify-rubric.sh
```

Rationale: the verifier is the single deterministic entrypoint that produces a machine report and per-ID evidence logs.
If CI needs a faster “inner loop”, add it deliberately (with a rubric version bump if it changes enforcement).
