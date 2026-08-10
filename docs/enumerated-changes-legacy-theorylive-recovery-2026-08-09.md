# Enumerated Changes: Legacy TheoryLive authoritative recovery

### 1. Detect claimed Soul versions with missing history and current artifacts

- **Paths**: `scripts/soul-integrity-scan-m2/main.go`, `scripts/soul-integrity-scan-m2/main_test.go`
- **Surface**: scripts
- **Classification**: soul-registry, operational-reliability, bug-fix, test-coverage
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none; operator-side scan only
- **On-chain impact**: off-chain only
- **Trust-API/CSP/instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: a positive `selfDescriptionVersion` with zero version rows and no current object produces three explicit integrity findings and a non-zero scan result.
- **Validation**: focused script tests, live dry-run against Silas, `go test ./...`, `go vet ./...`, tracked-source gofmt, Gov-infra rubric
- **Conventional Commit subject**: `fix(soul): detect missing registration history`
- **Status**: implemented by PR #1029 and deployed by the operator before this recovery-API follow-up

### 2. Add a pure tenant-bound recovery-source selector

- **Paths**: `internal/controlplane/` recovery source files and tests; existing store models/read helpers as needed
- **Surface**: internal/controlplane
- **Classification**: soul-registry, tenant-isolation, operational-reliability, test-coverage
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; selector requires authenticated Slug plus domain/registration/agent/conversation equality
- **On-chain impact**: off-chain only
- **Trust-API/CSP/instance-auth impact**: preserves
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic AppTheory/TableTheory reads; no workaround
- **Acceptance**: fixtures for published verified, legacy declarations-only, checksum mismatch, binding mismatch, ambiguity, and no-source states classify deterministically without writes or message decode.
- **Validation**: unit tests, `go test ./...`, `go vet ./...`, tracked-source gofmt, Gov-infra rubric
- **Conventional Commit subject**: `feat(soul): classify legacy recovery sources`
- **Status**: implemented in the recovery-API follow-up PR

### 3. Expose a bounded InstanceKey-authenticated recovery inventory

- **Paths**: `internal/controlplane/server.go`, recovery handlers/tests, rate-limit and audit tests
- **Surface**: control-plane API
- **Classification**: soul-registry, tenant-isolation, instance-auth, operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; authenticated Slug is mandatory and every result is domain-bound
- **On-chain impact**: off-chain only
- **Trust-API/CSP/instance-auth impact**: preserves exact `sha256(raw_key)` lookup; safely audits success/failure
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: `GET /api/v1/soul/instance/recovery/agents` returns only bounded metadata for the authenticated Managed instance, with pagination and no messages/declarations.
- **Validation**: valid/invalid/revoked/cross-tenant/auth/rate-limit/audit tests, `go test ./...`, `go vet ./...`, Gov-infra rubric
- **Conventional Commit subject**: `feat(soul): add instance recovery inventory`
- **Status**: implemented in the recovery-API follow-up PR

### 4. Expose exact side-effect-free recovery detail

- **Paths**: `internal/controlplane/server.go`, recovery handlers/tests, response-size/audit tests
- **Surface**: control-plane API
- **Classification**: soul-registry, tenant-isolation, instance-auth, operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; agent and every evidence row must bind to the authenticated Slug
- **On-chain impact**: off-chain only
- **Trust-API/CSP/instance-auth impact**: preserves; no raw key or private messages returned/logged
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: `GET /api/v1/soul/instance/recovery/agents/{agentId}` returns exact declarations, migration-read SHA256, immutable version metadata, and an explicit integrity classification without convergence, finalize, publish, or any Soul registry business-state write. InstanceKey last-used and redacted audit telemetry remain active.
- **Validation**: published/legacy/conflict state fixtures, no-artifact-write mocks, checksum/binding/oversize failure tests, `go test ./...`, `go vet ./...`, Gov-infra rubric
- **Conventional Commit subject**: `feat(soul): add exact recovery detail read`
- **Status**: implemented in the recovery-API follow-up PR

### 5. Publish the recovery contract and operator runbook

- **Paths**: `docs/contracts/openapi.yaml`, `docs/contracts/README.md`, new JSON fixtures/schema if required, `docs/contracts/hosted-genesis-conversation.md`, new recovery runbook, contract verifier tests
- **Surface**: docs/contracts, docs
- **Classification**: soul-registry, instance-auth, docs, test-coverage
- **Governance-rubric impact**: none; existing contract Verifier scope applies
- **Multi-tenant-isolation impact**: documents preserved boundary
- **On-chain impact**: none
- **Trust-API/CSP/instance-auth impact**: preserves
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: schemas distinguish `published_artifact_verified`, `legacy_declarations_only`, and integrity-conflict states; docs explicitly reject public projection as lossless history and prohibit replay/publication side effects.
- **Validation**: contract verifier, fixture/schema tests, Gov-infra rubric
- **Conventional Commit subject**: `docs(soul): publish legacy recovery contract`
- **Status**: implemented in the recovery-API follow-up PR

### 6. Prove lab and live read-only recovery behavior

- **Paths**: `docs/runbooks/` and `gov-infra/evidence/` evidence emitted by existing gates; no cloud-state patch script
- **Surface**: docs, operational evidence
- **Classification**: soul-registry, operational-reliability, tenant-isolation
- **Governance-rubric impact**: none; fresh Evidence only
- **Multi-tenant-isolation impact**: validates preserved boundary
- **On-chain impact**: none
- **Trust-API/CSP/instance-auth impact**: validates hash-only auth
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: lab proves retry-stable/no-write behavior and cross-tenant denial; explicitly authorized live read proves all four classifications without changing identity/session/version/S3 state.
- **Validation**: pre/post consistent reads, CloudWatch audit events, CloudFront/API 4xx/5xx monitoring, Gov-infra Evidence freshness
- **Conventional Commit subject**: `docs(soul): record recovery read validation`
