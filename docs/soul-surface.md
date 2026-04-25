# Soul surface (registry + instance proofs) + AgentCore MCP (lesser-body)

This doc is an overview of the **“soul” surfaces** in the EqualtoAI stack, and the integration contract between:

- `lesser-host` (control plane / registry + governance)
- `lesser` (per-instance deployment + domain proof endpoints)
- `lesser-body` (optional-but-default AgentCore-compatible MCP runtime for managed instances)

## What “soul” means (two distinct surfaces)

1) **Soul registry (control plane)**
   - Hosted by `lesser-host`.
   - Public read + authenticated lifecycle APIs under:
     - `GET/POST /api/v1/soul/*`
   - Owns:
     - on-chain identity anchors (contracts)
     - off-chain identity state (DynamoDB via TableTheory)
     - registration artifacts (S3, versioned)
     - operations (Safe-ready payloads + execution recording)

2) **Instance proof surface**
   - Hosted by `lesser` (per-instance).
   - Exposes proof material used by soul registration flows:
     - DNS TXT: `_lesser-soul-agent.<domain>` = `lesser-soul-agent=<token>`
     - HTTPS well-known: `https://<domain>/.well-known/lesser-soul-agent` = `{"lesser-soul-agent":"<token>"}` (JSON)

These two surfaces are related, but intentionally not the same thing. See `docs/adr/0001-component-placement.md`.

## Identity primitives (authoritative docs)

- Canonical identifiers + signatures: `docs/adr/0002-canonical-identifiers-and-signatures.md`
  - `agentId = keccak256("${normalizedDomain}/${normalizedLocalAgentId}")` as `uint256`
  - ERC-721 policy: `tokenId == agentId`
  - Registration signing: EIP-191 over `keccak256(JCS(registration-without-selfAttestation))`
- Suspension policy: `docs/adr/0003-suspension-policy.md`
- Agent ID conformance vectors: `docs/spec/agent-id-test-vectors.md`

## Soul registry API surface (`/api/v1/soul/*`)

The soul registry is served by `cmd/control-plane-api` through the `lesser.host` distribution.

### Public read (no auth)

- `GET /api/v1/soul/config`
- `GET /api/v1/soul/agents/{agentId}`
- `GET /api/v1/soul/agents/{agentId}/registration`
- `GET /api/v1/soul/agents/{agentId}/reputation`
- `GET /api/v1/soul/agents/{agentId}/validations` (paginated)
- `GET /api/v1/soul/search?q=...&capability=...`

#### `GET /api/v1/soul/search` query semantics

The soul search endpoint is intentionally fail-closed and does not perform an unbounded cross-domain local-ID scan.

Accepted lookup forms:

- domain-only: `q=example.com`
- domain-qualified agent: `q=example.com/medic`
- explicit domain + local query: `q=medic&domain=example.com`
- current-instance bare local query: `q=medic`, `q=@medic`, or `q=medic/` when the request host maps to a verified
  instance domain in the control plane

Managed stage domains stay exact for lookup. For example, `domain=dev.simulacrum.greater.website` searches the
stage-scoped soul index for `dev.simulacrum.greater.website` rather than rewriting to `simulacrum.greater.website`.

Security boundaries:

- if the request host does not map to a verified instance domain, bare local queries still reject with a bad request
- if both `q` and `domain=` specify domains and they do not match, the request rejects instead of guessing
- malformed local IDs and malformed domains reject with bad-request semantics rather than falling back to a wider search

### Authenticated lifecycle (portal/operator session)

All write endpoints require `Authorization: Bearer <session>`. Sessions are shared with the portal/operator auth system
(see `docs/portal.md`).

- Registration:
  - `POST /api/v1/soul/agents/register/begin`
  - `POST /api/v1/soul/agents/register/{id}/verify`
- Post-mint lifecycle:
  - `POST /api/v1/soul/agents/{agentId}/rotate-wallet/begin`
  - `POST /api/v1/soul/agents/{agentId}/rotate-wallet/confirm`
  - `POST /api/v1/soul/agents/{agentId}/update-registration`
- “Mine”:
  - `GET /api/v1/soul/agents/mine`
- Validation challenges:
  - `POST /api/v1/soul/agents/{agentId}/validations/challenges`
  - `POST /api/v1/soul/agents/{agentId}/validations/challenges/{challengeId}/response`
  - `POST /api/v1/soul/agents/{agentId}/validations/challenges/{challengeId}/evaluate`
- Operations + execution recording:
  - `GET /api/v1/soul/operations`
  - `GET /api/v1/soul/operations/{id}`
  - `POST /api/v1/soul/operations/{id}/record-execution`
- Operator/admin controls:
  - `POST /api/v1/soul/agents/{agentId}/suspend`
  - `POST /api/v1/soul/agents/{agentId}/reinstate`
  - `POST /api/v1/soul/reputation/publish`
  - `POST /api/v1/soul/validation/publish`

## Inbound email bridge (`inbound.lessersoul.ai`)

Inbound soul email delivery uses a bridge domain instead of direct HTTP forwarding from Migadu:

```text
Sender -> Migadu mailbox -> agent@inbound.lessersoul.ai -> SES receipt rule -> email-ingress Lambda -> comm-worker
```

- Migadu remains the mailbox and outbound SMTP provider for `@lessersoul.ai`.
- New provisioning and operational backfills set Migadu forwarding targets to `<localPart>@inbound.lessersoul.ai`.
- Amazon SES receives mail for `inbound.lessersoul.ai`, stores the raw message in S3, and invokes `cmd/email-ingress`.
- `cmd/email-ingress` parses the raw RFC 5322 message and enqueues the existing `communication:inbound` payload shape for `comm-worker`.
- `comm-worker` continues to resolve the final recipient against the canonical `@lessersoul.ai` address, so downstream routing and delivery semantics stay unchanged.

Operational notes:

- SES receiving is region-bound; `lesser-host` deploys the bridge only in an SES-inbound-capable region.
- DNS for `inbound.lessersoul.ai` is managed outside Route53 today, so the stack outputs the DKIM verification CNAMEs and MX target for manual GoDaddy entry.
- Raw inbound mail is retained briefly in a dedicated S3 bucket with lifecycle expiration to support debugging without keeping message bodies indefinitely.

## Soul Comm Mailbox v1 authority

ADR 0005 defines the bounded mailbox authority decision for soul communications. The short version:

- `lesser-host` owns canonical mailbox delivery objects, including delivery/provider/thread/idempotency facts, bounded
  content, content identity, and read/unread/archive/delete state.
- `lesser` receives notification summaries/projections for UX/activity only; it is not authoritative mailbox state.
- `lesser-body` remains the MCP facade and exposes tools over host's API contract. It must not persist mailbox truth.
- List endpoints must return redacted previews/metadata only. Full content requires an explicit content/read call and
  emits access-audit evidence.
- Content storage is bounded by retention, encryption, access audit, and no-permanent-semantic-memory constraints.
- Sensitive mailbox APIs require strict hash-only instance auth: bearer raw key -> `sha256(raw_key)` -> stored hash match.
  Legacy plaintext fallback paths must not be accepted for mailbox list/content/state endpoints.

This is an explicit, governance-documented exception to host's normal metadata-only posture for tenant content. The
exception is limited to soul comm mailbox delivery artifacts and does not authorize cross-tenant search, tenant content
analytics, or body-owned mailbox storage.

### `update-registration` contract (lesser-body / MCP endpoint compatible)

`POST /api/v1/soul/agents/{agentId}/update-registration` publishes the **current** registration JSON to S3 at:

- `registry/v1/agents/<agentId>/registration.json`

Rules (high level):

- `agentId` in the JSON must match the path parameter.
- `domain` + `localId` must match the agent identity record.
- `wallet` must match:
  - on-chain `SoulRegistry.getAgentWallet(agentId)`
  - the wallet stored in the control-plane identity record (to fail closed on out-of-sync states).
- `attestations.selfAttestation` must be a valid EIP-191 signature per ADR 0002.
- Capabilities are normalized/validated (when an allowlist is configured) and used to maintain capability indexes.

This endpoint does not special-case MCP; any registration fields (including `endpoints.mcp`) are covered by the signature
and stored as part of the registration object.

Storage layout details are in `docs/soul-pack-bucket-layout.md`.

## Managed provisioning: lesser-body deploy + `POST /mcp/{actor}` wiring

Managed instances can optionally deploy `lesser-body` (AgentCore MCP runtime) into the **instance account** and expose it
at a **path** on the instance API domain:

- MCP URL: `POST https://api.<stageDomain>/mcp/{actor}`
- Well-known: `GET https://api.<stageDomain>/.well-known/mcp.json`

Key constraints:
- No Lambda Function URLs in the MCP flow.
- No CloudFront required for MCP routing (AgentCore calls API Gateway directly).
- Cross-stack wiring uses **SSM Parameter Store** only (no CloudFormation exports/imports).

### Instance flags

- `body_enabled`: controls the `lesser-body` + MCP wiring steps.
- `soul_enabled`: enables soul registry features for the instance (portal/UI + proof workflows); it does **not** deploy
  any instance-side “soul runtime”.

`body_enabled` defaults to `true` for managed instances.

### Control-plane config knobs (managed lesser-body)

`lesser-host` exposes these stage-scoped env vars for managed provisioning:

- `MANAGED_LESSER_BODY_DEFAULT_VERSION` (optional release tag or `latest`; used when a request doesn’t specify a version)
- `MANAGED_LESSER_BODY_GITHUB_OWNER` (default `equaltoai`)
- `MANAGED_LESSER_BODY_GITHUB_REPO` (default `lesser-body`)

These are passed into the CodeBuild runner as `LESSER_BODY_GITHUB_OWNER`, `LESSER_BODY_GITHUB_REPO`, and
`LESSER_BODY_VERSION` (when set) for `RUN_MODE=lesser-body`.

### Provisioning worker step sequence (high level)

After the initial Lesser deploy and receipt ingest:

1) `body.deploy.*` — CodeBuild runner `RUN_MODE=lesser-body`
2) `deploy.mcp.*` — CodeBuild runner `RUN_MODE=lesser-mcp` (re-deploy Lesser stage stack to attach `/mcp/{actor}`)

### Receipts (debuggable artifacts)

The runner uploads receipts to the artifacts bucket for inspection:

- Lesser receipt: `managed/provisioning/<slug>/<jobId>/state.json`
- Lesser-body receipt: `managed/provisioning/<slug>/<jobId>/body-state.json`
- MCP wiring receipt: `managed/provisioning/<slug>/<jobId>/mcp-state.json`

In artifact mode, those managed copies retain the runtime fields the control plane already ingests and add
`managed_deploy_artifacts` so operators can see which verified Lesser or `lesser-body` release assets were used.

### SSM contract inside the instance account

Required parameters (well-known names; `${app}` = instance slug, `${stage}` = `dev|staging|live`):

From Lesser (inputs for `lesser-body`):
- `/${app}/${stage}/lesser/exports/v1/table_name`
- `/${app}/${stage}/lesser/exports/v1/domain`

From `lesser-body` (inputs for Lesser `/mcp/{actor}` wiring):
- `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`

### What `lesser-body` must provide

In managed mode, `lesser-body` is expected to:

- deploy into the instance account/stage
- publish the MCP Lambda ARN to:
  - `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`

The managed runner treats this parameter as the contract boundary used to attach `POST /mcp/{actor}` to the instance API gateway.

### MCP URL derivation

`lesser-host` derives the MCP URL from the instance base domain + control-plane stage mapping:

- `dev`: `https://api.dev.<baseDomain>/mcp/{actor}`
- `staging`: `https://api.staging.<baseDomain>/mcp/{actor}`
- `live`: `https://api.<baseDomain>/mcp/{actor}`

When `body_enabled=true`, `mcp_url` is included in:
- portal instance responses
- provisioning job responses

### Smoke test (MCP initialize)

`/mcp/{actor}` is authenticated. Use a Lesser OAuth access token (JWT) or a managed instance key (when configured).

```bash
curl -sSfL -X POST "https://api.<stageDomain>/mcp/<actor>" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
```

See `docs/managed-instance-provisioning.md` for a fuller managed provisioning runbook.
