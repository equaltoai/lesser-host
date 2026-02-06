# lesser-host Threat Model (custom — v0.1)

This document enumerates the highest-risk threats for the in-scope system and assigns stable IDs (`THR-*`) that must map
to controls in `gov-infra/planning/lesser-host-controls-matrix.md`.

## Scope (must be explicit)
- **System:** AWS-backed multi-service control plane for hosted Lesser instance provisioning + governance + trust/safety services (Go Lambdas, Svelte web UI, AWS CDK infra, Solidity contracts).
- **In-scope data:** authentication/session tokens, wallet addresses, instance API keys (hashed), Stripe billing identifiers, AI provider prompts/outputs (may contain sensitive user content), operational telemetry, deploy receipts/artifacts, secrets in AWS SSM/KMS.
- **Environments:** `lab`, `staging`, `prod` (define “prod-like”: internet-reachable and/or connected to real third-party services).
- **Third parties:** AWS, Stripe, Anthropic, OpenAI, Ethereum JSON-RPC providers, GitHub Actions.
- **Out of scope:** per-tenant `lesser` application internals (except where the control plane ingests deploy receipts); end-user device security.
- **Assurance target:** audit-ready hardening for security-critical paths with deterministic, CI-enforced verifiers.

## Assets and Trust Boundaries (high level)
- **Primary assets:**
  - Operator/admin authority (RBAC, session tokens)
  - Customer accounts + billing state
  - Instance registration state (domains, wallets, instance keys)
  - Attestation signing key material (KMS-backed)
  - AI moderation/evidence pipelines (messages, outputs)
  - Provisioning runner permissions (Organizations + CodeBuild)
  - Deploy receipts + artifact buckets
- **Trust boundaries:**
  - Public internet → CloudFront → API Lambda Function URLs
  - Control plane → AWS APIs (DynamoDB/S3/SQS/KMS/Route53/Organizations)
  - Control plane → third parties (Stripe, AI providers, RPC)
  - Operator browser (web UI) → API endpoints
  - Managed provisioning worker → external releases/artifacts (supply chain)
- **Entry points:**
  - Public HTTP endpoints (`/api/*`, `/.well-known/*`, `/attestations*`, `/setup/*`)
  - SQS queues (workers)
  - CodeBuild job inputs (managed provisioning)
  - Web UI build pipeline

## Top Threats (stable IDs)
Threat IDs must be stable over time. When a new class of risk is discovered:
1) add a new `THR-*`,
2) add/adjust controls in the controls matrix,
3) update the rubric/roadmap if a new verifier is required.

| Threat ID | Title | What can go wrong | Primary controls (Control IDs) | Verification (gate) |
| --- | --- | --- | --- | --- |
| THR-1 | Regression in security-critical logic | A refactor breaks authn/authz, session validation, budget enforcement, or queue processing; the change ships because it wasn’t tested. | QUA-1 QUA-3 CON-2 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (QUA-1/3, CON-2) |
| THR-2 | Boundary/contract drift | Public API shapes, deploy receipts, or attestation payloads drift without detection; downstream components misinterpret data. | QUA-2 CON-3 DOC-4 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (QUA-2, CON-3, DOC-4) |
| THR-3 | Auth bypass during bootstrap/setup | Setup flows allow unintended admin creation, replay, or privilege escalation (e.g., challenge reuse, weak gating). | SEC-4 QUA-1 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-4) |
| THR-4 | SSRF / unsafe fetches in trust services | Render/preview services fetch attacker-controlled URLs and access internal metadata or private networks; stored artifacts contain sensitive data. | SEC-4 QUA-2 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-4, QUA-2) |
| THR-5 | Supply-chain compromise | Malicious dependency, compromised GitHub Action, or unsafe install script executes in CI/CodeBuild and exfiltrates secrets or ships backdoors. | SEC-3 SEC-2 SEC-1 COM-2 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-3/2/1, COM-2) |
| THR-6 | LLM prompt/response boundary failure | System prompts or schemas drift; unsafe tool use or content injection occurs; sensitive information is included in outputs/logs. | SEC-4 DOC-4 QUA-1 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-4, DOC-4) |
| THR-7 | Security scanning dilution | Security checks are “green” only because important rules were disabled or scope excluded; regressions slip through. | COM-5 SEC-1 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (COM-5, SEC-1) |
| THR-8 | Multi-language drift | Go gates are green but TS/CDK/contracts are failing (or vice versa); CI doesn’t enforce the whole system. | QUA-1 QUA-2 CON-2 MAI-4 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (QUA-1/2, CON-2, MAI-4) |
| THR-9 | Coverage denominator games | Coverage target is “met” by shrinking scope or excluding critical packages; the number becomes meaningless. | QUA-3 COM-4 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (QUA-3, COM-4) |
| THR-10 | Governance drift (docs claim controls that aren’t real) | Threats and controls diverge; rubric/roadmap become stale; CI stops enforcing verifiers. | DOC-5 DOC-4 MAI-4 COM-3 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (DOC-5, DOC-4, MAI-4, COM-3) |

## Parity Rule (no “named threat without control”)
- Every `THR-*` listed above must appear at least once in the controls matrix “Threat IDs” column.
- The repo must have a deterministic parity check that fails if any threat is unmapped.

## Notes
- Keep standards/framework text out of the repo when licensing is uncertain; reference IDs/titles only.
- Prefer threats phrased as “failure modes” the repo can actually prevent or detect.
