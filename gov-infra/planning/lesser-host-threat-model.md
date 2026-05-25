# lesser-host Threat Model (custom — v0.1)

This document enumerates the highest-risk threats for the in-scope system and assigns stable IDs (`THR-*`) that must map
to controls in `gov-infra/planning/lesser-host-controls-matrix.md`.

## Scope (must be explicit)
- **System:** AWS-backed multi-service control plane for hosted Lesser instance provisioning + governance + trust/safety services (Go Lambdas, Svelte web UI, AWS CDK infra, Solidity contracts).
- **In-scope data:** authentication/session tokens, wallet addresses, instance API keys (hashed), Stripe billing identifiers, x402 invocation grant hashes/usage slots, AI provider prompts/outputs (may contain sensitive user content), inbound email bodies/headers stored temporarily for routing, bounded soul comm mailbox content/state, operational telemetry, deploy receipts/artifacts, secrets in AWS SSM/KMS.
- **Environments:** `lab`, `staging`, `prod` (define “prod-like”: internet-reachable and/or connected to real third-party services).
- **Third parties:** AWS, Migadu, Telnyx, Stripe, caller-provided x402 facilitators, Anthropic, OpenAI, Ethereum JSON-RPC providers, GitHub Actions.
- **Out of scope:** per-tenant `lesser` application internals (except where the control plane ingests deploy receipts); end-user device security.
- **Assurance target:** audit-ready hardening for security-critical paths with deterministic, CI-enforced verifiers.

## Assets and Trust Boundaries (high level)
- **Primary assets:**
  - Operator/admin authority (RBAC, session tokens)
  - Customer accounts + billing state
  - Instance registration state (domains, wallets, instance keys)
  - Attestation signing key material (KMS-backed)
  - AI moderation/evidence pipelines (messages, outputs)
  - Inbound email bridge artifacts (SES receipt events + raw S3 objects)
  - Bounded soul comm mailbox content, content identity, read/archive/delete state, and audit events
  - Scoped x402 invocation grants (hash-only caller/payment evidence, one-time token hash, usage-slot state)
  - Provisioning runner permissions (Organizations + CodeBuild)
  - Deploy receipts + artifact buckets
- **Trust boundaries:**
  - Public internet → CloudFront → API Lambda Function URLs
  - Control plane → AWS APIs (DynamoDB/S3/SQS/KMS/Route53/Organizations)
  - Control plane → third parties (Migadu, Telnyx, Stripe, AI providers, RPC)
  - SES inbound pipeline → S3/Lambda/SQS (raw email receipt and normalization)
  - Instance-authenticated soul comm mailbox APIs → bounded content/state storage
  - Public x402 grant issue → host caller-access payment policy; instance-authenticated x402 grant consume → bounded usage slots
  - Operator browser (web UI) → API endpoints
  - Managed provisioning worker → external releases/artifacts (supply chain)
- **Entry points:**
  - Public HTTP endpoints (`/api/*`, `/.well-known/*`, `/attestations*`, `/setup/*`)
  - Instance-authenticated mailbox list/content/state endpoints for soul comms
  - Public x402 grant issue and instance-authenticated x402 grant consume endpoints
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
| THR-11 | Bounded mailbox content/state drift | Host's soul comm mailbox exception expands beyond delivery artifacts: content is retained indefinitely, list endpoints expose full bodies, read/archive/delete state lacks audit, instance-auth falls back to plaintext, or mailbox data crosses tenant boundaries. | CMP-4 SEC-4 CON-3 DOC-4 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (CMP-4 after Host 1, SEC-4/CON-3 as APIs land) |
| THR-12 | x402 invocation grant authority drift | Public paid callers gain principal/operator authority, grant tokens or payment evidence are stored/logged raw, facilitator claims are treated as host-verified settlement, grants are reusable beyond expiry/max usage, or consume bypasses instance-domain ownership. | SEC-4 CON-3 QUA-1 DOC-4 | `bash gov-infra/verifiers/gov-verify-rubric.sh` (SEC-4 P0 tests, CON-3 contract parity, QUA-1 regression tests, DOC-4 docs integrity) |
| T-CSP-001 | CSP byte-string drift | A refactor, CDK change, or framework update alters the CSP header content (directive values, order, join separator, or count), silently weakening the strict single-origin posture and allowing inline scripts, third-party origins, or unsafe-eval. | C-CSP-FT SEC-5 SEC-6 | `bash gov-infra/verifiers/sec/web-csp-integrity.sh` (SEC-5) |
| T-OAC-001 | OAC form transport degradation | The OAC signing mechanism for S3 PUTs (used by the FaceTheory hydration sidecar upload path) is weakened, bypassed, or removed, allowing unsigned or unauthorized writes to the htmlStoreBucket. | C-OAC-MUT SEC-7 | `bash gov-infra/verifiers/sec/oac-form-integrity.sh` (SEC-7) |
| T-COMP-001 | CloudFront distribution composition regression | Changes to the CloudFront distribution (additional behaviors, origin changes, path routing, response headers policies) weaken multi-origin isolation, CSP delivery, or the SPA rewrite function. | C-CDN-COMP SEC-8 | `bash gov-infra/verifiers/sec/cloudfront-composition.sh` (SEC-8) |
| T-AUTH-DRIFT-001 | Trust-auth / attestation signing drift | Instance auth (sha256 key hash matching) or attestation signing key management is weakened — raw keys stored, hash comparison relaxed, attestation signing keys shared across instances, or instance-auth bypass introduced for “trusted” callers. | C-AUTH-LOCK SEC-9 | `bash gov-infra/verifiers/sec/trust-auth-preservation.sh` (SEC-9) |
| T-SUPPLY-001 | Supply-chain release verification bypass | Consumer release verification for lesser/body artifacts is weakened, skipped, or bypassed — checksum comparison removed, verification script altered, release manifest accepted after deploy, or “trusted” commit exceptions introduced. | C-SUPPLY-LOCK SEC-10 | `bash gov-infra/verifiers/sec/release-verification-preservation.sh` (SEC-10) |
| T-MCP-ROUTE-001 | MCP route ownership regression | Host-owned MCP routes (used by the trust API, control plane, and comm APIs) are lost, duplicated, or misattributed in the build/deploy pipeline, causing body to receive incorrect route mappings or host-owned routes to be treated as tenant-owned. | C-MCP-ROUTE CON-4 | `bash gov-infra/verifiers/con/wire-mcp-route-ownership.sh` (CON-4) |

## Parity Rule (no “named threat without control”)
- Every `THR-*` and `T-*-*` listed above must appear at least once in the controls matrix “Threat IDs” column.
- The repo must have a deterministic parity check that fails if any threat is unmapped.

## Notes
- Keep standards/framework text out of the repo when licensing is uncertain; reference IDs/titles only.
- Prefer threats phrased as “failure modes” the repo can actually prevent or detect.
