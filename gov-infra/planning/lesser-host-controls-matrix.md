# lesser-host Controls Matrix (custom — v0.1.1)

This matrix is the “requirements → controls → verifiers → evidence” backbone for lesser-host. It is intentionally
engineering-focused: it does not claim compliance, but it makes security/quality assertions traceable and repeatable.

## Scope
- **System:** AWS-backed multi-service control plane for hosted Lesser instance provisioning + governance + trust/safety services (Go Lambdas, Svelte web UI, AWS CDK infra, Solidity contracts).
- **In-scope data:** authentication/session tokens, wallet addresses, instance API keys (hashed), Stripe billing identifiers, AI provider prompts/outputs (may contain sensitive user content), operational telemetry, deploy receipts/artifacts, secrets in AWS SSM/KMS (no secrets in git).
- **Environments:** `lab` (default), `staging`, `prod`; “prod-like” means *internet-reachable* and/or *connected to real third-party services* (AWS accounts, Stripe, AI providers) even if “test” data.
- **Third parties:** AWS (Lambda, DynamoDB, S3, SQS, KMS, Route53, CloudFront, CodeBuild, Organizations), Stripe, Anthropic, OpenAI, Ethereum JSON-RPC providers, GitHub Actions.
- **Out of scope:** tenant application internals (the per-tenant `lesser` deployments) except where the control plane ingests deploy receipts or calls tenant endpoints; end-user device security.
- **Assurance target:** audit-ready hardening for security-critical paths (authn/authz, secrets, supply chain, public-facing HTTP endpoints), with deterministic, CI-enforced verifiers.

## Threats (reference IDs)
- Threats are stable IDs (`THR-*`) defined in `gov-infra/planning/lesser-host-threat-model.md`.
- Each `THR-*` must map to ≥1 row in the controls table below (validated by a deterministic parity check).

## Status (evidence-driven)
If you track implementation status, treat it as evidence-driven:
- `unknown`: no verifier/evidence yet
- `partial`: some controls exist but coverage/evidence is incomplete
- `implemented`: verifier exists and evidence path is repeatable

## Engineering Controls (Threat → Control → Verifier → Evidence)
This table is the canonical mapping used by the rubric/roadmap/evidence plan.

| Area | Threat IDs | Control ID | Requirement | Control (what we implement) | Verification (command/gate) | Evidence (artifact/location) |
| --- | --- | --- | --- | --- | --- | --- |
| Quality | THR-1 THR-8 THR-9 | QUA-1 | Unit tests prevent regressions | Run Go + Web unit tests as first-line regression gates. | `bash gov-infra/verifiers/gov-verify-rubric.sh` (QUA-1) | `gov-infra/evidence/QUA-1-output.log` |
| Quality | THR-2 THR-4 THR-6 | QUA-2 | Integration/contract tests prevent boundary drift | Keep infra/contract surface reproducible (contracts compile, CDK synth). | `bash gov-infra/verifiers/gov-verify-rubric.sh` (QUA-2) | `gov-infra/evidence/QUA-2-output.log` |
| Quality | THR-1 THR-4 THR-9 | QUA-3 | Coverage threshold is enforced (no dilution) | Measure and enforce Go coverage ≥ 80% as a floor; add JS coverage once configured. | `bash gov-infra/verifiers/gov-verify-rubric.sh` (QUA-3) | `gov-infra/evidence/QUA-3-output.log` + `gov-infra/evidence/coverage.out` |
| Consistency | — | CON-1 | Formatting is clean (no diffs) | Enforce `gofmt` cleanliness for all Go sources in-repo. | `bash gov-infra/verifiers/gov-verify-rubric.sh` (CON-1) | `gov-infra/evidence/CON-1-output.log` |
| Consistency | THR-1 THR-5 THR-7 THR-10 | CON-2 | Lint/static analysis is enforced (pinned toolchain) | Enforce Go lint (golangci-lint + gosec), Solidity lint (solhint), and TS lint/typecheck in web/cdk with pinned toolchain versions (no “whatever is installed”). | `bash gov-infra/verifiers/gov-verify-rubric.sh` (CON-2) | `gov-infra/evidence/CON-2-output.log` |
| Consistency | THR-2 THR-4 | CON-3 | Public boundary contract parity (if applicable) | Define and enforce contract parity for public boundaries (e.g., deploy receipt schema, attestations schema, API boundary invariants). | `bash gov-infra/verifiers/gov-verify-rubric.sh` (CON-3) | `gov-infra/evidence/CON-3-output.log` |
| Completeness | THR-1 THR-5 | COM-1 | All modules compile (no “mystery meat”) | Ensure all Go modules compile from a clean checkout (root + nested modules like `cdk/`). | `bash gov-infra/verifiers/gov-verify-rubric.sh` (COM-1) | `gov-infra/evidence/COM-1-output.log` |
| Completeness | THR-5 THR-10 | COM-2 | Toolchain pins align to repo expectations | Ensure Go and Node versions match the repo’s declared constraints (go.mod, CI, package engines); ensure security tools are pinned before they are trusted. | `bash gov-infra/verifiers/gov-verify-rubric.sh` (COM-2) | `gov-infra/evidence/COM-2-output.log` |
| Completeness | THR-5 | COM-3 | Lint config schema-valid (no silent skip) | Validate that lint configs are parseable and supported by the pinned toolchain (avoid “silent ignore”). | `bash gov-infra/verifiers/gov-verify-rubric.sh` (COM-3) | `gov-infra/evidence/COM-3-output.log` |
| Completeness | THR-1 THR-9 | COM-4 | Coverage threshold not diluted | Ensure the configured threshold is ≥ 80% and consistent across rubric/docs/verifiers. | `bash gov-infra/verifiers/gov-verify-rubric.sh` (COM-4) | `gov-infra/evidence/COM-4-output.log` |
| Completeness | THR-5 THR-7 | COM-5 | Security scan config not diluted | Disallow excluding high-signal security rules without explicit, version-bumped policy decisions. | `bash gov-infra/verifiers/gov-verify-rubric.sh` (COM-5) | `gov-infra/evidence/COM-5-output.log` |
| Security | THR-5 THR-7 | SEC-1 | Baseline SAST stays green | Run dedicated security/static scan gates (gosec via golangci-lint + Slither for Solidity). | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-1) | `gov-infra/evidence/SEC-1-output.log` |
| Security | THR-5 | SEC-2 | Dependency vulnerability scan stays green | Run dependency vulnerability scanning with pinned tooling (Go first; add Node tooling once chosen). | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-2) | `gov-infra/evidence/SEC-2-output.log` |
| Security | THR-5 THR-10 | SEC-3 | Supply-chain verification green | Pin GitHub Actions by commit SHA; scan Node dependency lifecycle hooks (scripts disabled install); flag risky go.mod replace directives and Python custom indexes. | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-3) | `gov-infra/evidence/SEC-3-output.log` |
| Security | THR-3 THR-4 THR-6 | SEC-4 | Domain-specific P0 regression tests | Add explicit P0 safety/security regression gates (authz invariants, SSRF defense, prompt boundaries, no sensitive logs). | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-4) | `gov-infra/evidence/SEC-4-output.log` |
| Docs | THR-1 THR-10 | DOC-5 | Threat model ↔ controls parity (no unmapped threats) | Enforce “named threats must map to controls” via deterministic parity check. | `bash gov-infra/verifiers/gov-verify-rubric.sh` (DOC-5) | `gov-infra/evidence/DOC-5-parity.log` |

> Add rows as needed for additional anti-drift (multi-module health, CI rubric enforcement, encryption/redaction semantics)
> and for repo-specific P0 gates.

## Framework Mapping (Optional)
No compliance framework is assumed for this repository (domain is `custom`). If a framework becomes in-scope later, store only requirement IDs/titles here; keep licensed standards text out of-repo.

## Notes
- Prefer deterministic verifiers (tests, static analysis, IaC assertions) over manual checklists.
- Treat this matrix as “source material”: the rubric/roadmap/evidence plan must stay consistent with Control IDs here.
