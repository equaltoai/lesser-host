# TheoryMCP authentication: bypass approach and deployment plan

Status: planning. Branch: `satish/theorymcp-namespace-standup-for-lesser-host`.

## Problem

lesser-host deployment and stewardship flows surface the Theory Cloud MCP server (`theory-cloud/theory-mcp-server`,
internal) whenever an agent, CLI, or deploy-guidance pack needs routed MCP access. The hosted surface (`theorymcp.ai`)
requires Autheory-issued OAuth access tokens (JWT, validated via JWKS) for every routed namespace/agent MCP endpoint.
Without hosted Autheory credentials for the operator's account, the MCP authentication step blocks the lesser-host
startup/deploy flow.

## How the hosted authentication works

From `theory-cloud/theory-mcp-server` (`internal/auth/validator.go`, `issuer.go`, `middleware.go`):

1. Every MCP route (`/{client_namespace}/mcp`, `/{client_namespace}/agents/{agent}/mcp`) requires a bearer token.
2. Tokens are Autheory-issued JWTs. Validation is fail-closed:
   - **Issuer** must equal `AUTHEORY_ISSUER_URL` (lab: `https://auth.lab.autheory.app`; live: `https://auth.autheory.app`).
   - **Audience** must equal the routed protected-resource URL (the MCP route itself).
   - **Scopes** required per operation: `mcp:tools` (MCP tools), `ai.kb.query` (knowledge), `memory.append` (memory writes).
   - **Authorized party** must be in the allow-list (`THEORY_MCP_ALLOWED_AUTHORIZED_PARTIES`; default `theorymcp-web`).
   - **Namespace claim** (`organization_id` / `org_id` / `tenant_id`) must match the tenant behind the routed
     `client_namespace`.
3. The runtime fails closed at startup if `AUTHEORY_ISSUER_URL` or `THEORY_MCP_PUBLIC_BASE_URL` is missing.

## Bypass strategy: self-owned instance + self-owned namespace

Instead of authenticating against the hosted `theorymcp.ai` + Autheory, deploy a **self-owned `theory-mcp-server`**
instance and route lesser-host's MCP consumers at a **self-owned namespace** on it. Two levers exist in the code:

1. **Own the namespace** — the internal namespace-vending API (`POST /v1/internal/namespace-vending` in
   `cmd/api/namespace_vending.go`) seeds tenant → namespace → agent accounts → soul/instructions → skill bank →
   install layouts → lifecycle packs for a routed `client_namespace`. This is the same mechanism the hosted service
   uses, exposed on the self-owned deployment.
2. **Own the auth** — the **harness namespace-key** path (`internal/auth/harness_keys.go`) validates static
   namespace keys (SHA-256 of the raw key, stored in a Secrets Manager registry) instead of JWKS/OAuth. It is
   **lab-only by design**: the runtime rejects harness keys for `live` stages
   (`internal/runtimeconfig/harness_keys.go`). This removes the Autheory dependency for lab/development.

Why harness keys: no JWKS discovery, no Autheory account, no token endpoint — a raw bearer token scoped to the
namespace, mirroring lesser-host's own instance-key auth pattern (`sha256(raw_key)` matching). Aligns with the
existing trust posture.

## Deployment plan

1. **Deploy self-owned theory-mcp-server (lab)**
   - Repo: `theory-cloud/theory-mcp-server` (internal), branch `premain` (lab source).
   - Command: `./scripts/theorycloud-install --stage lab --profile Mcp`
   - Override deploy contract parameters (`app-theory/app.json`): own `hostedZoneId`/`hostedZoneName`; own
     `namespaceVendingCallerPrincipalArn` (`arn:aws:iam::<ACCOUNT>:role/theorycloud-ai-namespace-vending-lab`).
     Genome id/version/checksum are server-pinned and must not change.
   - Create the `theorycloud-ai-namespace-vending-lab` role in the deploy account and allow the `Mcp` profile to
     assume it (the CDK construct validates role name + account).
2. **Configure harness namespace-key registry**
   - Secrets Manager secret `theory-mcp-server/lab/mcp-harness-namespace-keys` (JSON registry with
     `client_namespace`, `key_id`, `token_sha256`, `expires_at`, `status`, `scopes`).
   - Mint raw keys; store only SHA-256.
3. **Seed the namespace**
   - Call `POST /v1/internal/namespace-vending` (SigV4, caller-restricted) to seed tenant/namespace/agents/layouts.
   - Namespace routes resolve: `/{your-namespace}/mcp`, `/{your-namespace}/agents/{agent}/mcp`.
4. **Point lesser-host MCP consumers at the self-owned routes**
   - MCP client configs (Codex/Claude Code/opencode) use `https://<your-domain>/<your-namespace>/mcp` with the
     harness namespace key as bearer token.
   - lesser-host deploy guidance then runs against the self-owned instance (no hosted Autheory needed).
5. **Proceed with lesser-host**
   - `AWS_PROFILE=<profile> theory app up --stage lab --execute` (AppTheory contract; never direct CDK).
   - Then provision a managed instance via the portal (provisions lesser + lesser-body).

## Security and governance notes

- **Lab-only**: harness keys fail closed on live. Do not enable this posture on `live`.
- **No secrets in git**: registry payloads and raw keys stay in Secrets Manager / operator vaults.
- **Do not weaken the hosted service**: this bypass applies to the operator's self-owned deployment only.
- **Gov-infra**: any verifier change in `theory-mcp-server` must pass
  `bash gov-infra/verifiers/gov-verify-rubric.sh`; do not weaken gates.
- **lesser-host discipline** still applies: gov-infra rubric, multi-tenant isolation, consumer release verification,
  AppTheory contract for deploys.

## Files

- Runbook: `docs/runbooks/theorymcp-namespace-standup.md`
- This plan: `docs/theorymcp-namespace-deploy-plan.md`
