# TheoryMCP authentication and namespace-assisted deployment plan

Status: offline implementation complete; cloud execution deferred until AWS credentials and a vended namespace are
available. Branch: `satish/theorymcp-namespace-standup-for-lesser-host`.

## Problem

lesser-host deployment and stewardship flows may surface the hosted TheoryMCP service when an agent, CLI, or
deploy-guidance pack needs routed MCP access. The hosted surface requires route-scoped authentication. That prompt is
separate from lesser-host's AWS deployment credentials and must not be addressed by weakening auth or deploying an
unmanaged copy of theory-mcp-server.

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

## Correct boundary: pre-vended namespace + preserved authentication

Review of the TheoryMCP namespace-vending contract establishes that independent namespace bootstrap is not a
lesser-host responsibility:

1. The internal namespace-vending API is an `AWS_IAM` handoff owned by TheoryMCP and TheoryCloud.ai.
2. TheoryMCP creates an exact stage-scoped caller role in its account and trusts the TheoryCloud.ai account to assume
   it. A lesser-host operator does not independently assume that role or seed the first namespace.
3. The public MCP route becomes usable only after the owner vends the namespace and optional agent endpoint.
4. Normal consumers complete route-scoped Autheory OAuth. A harness key is a lab-only, operator-issued validation
   credential for an already-vended namespace; it is not a general OAuth bypass or bootstrap path.

Therefore this change does not modify lesser-host auth or deploy theory-mcp-server. It implements a safe offline plan
for consuming a pre-vended namespace and separately previewing lesser-host's AppTheory deploy contract.

## Deployment plan

1. **Owner vends the namespace (external prerequisite)**
   - Obtain `client_namespace`, optional `agent_id`, stage, and approved auth path from the owner.
2. **Render the zero-cloud implementation plan**
   - Run `bash scripts/plan-theorymcp-assisted-deploy.sh --client-namespace <slug> --agent-id <slug> --profile default`.
   - The script validates route segments, prints the MCP routes, and invokes only the symbolic `theory app up`
     preview. It hard-refuses live and execution flags.
3. **Prepare the provenance-valid deployment checkout**
   - Initialize `factory/products/lesser-host`; actual deploys from a standalone clone fail closed in
     `scripts/validate-deploy-provenance.sh`.
4. **Authenticate to TheoryMCP**
   - Complete route-scoped OAuth, or use an operator-issued lab harness key without putting it in git or command
     history.
5. **Add AWS credentials and deploy later**
   - Once profile `default` is valid and the exact lab invocation is authorized, run
     `theory app up --stage lab --execute` from the Factory submodule.
   - Then provision the managed instance through lesser-host (which deploys lesser and lesser-body).

## Security and governance notes

- **No auth bypass**: lesser-host does not disable or substitute TheoryMCP authorization.
- **Lab-only harness posture**: harness keys fail closed on live and are issued by the TheoryMCP operator.
- **No secrets in git**: OAuth tokens, harness keys, AWS credentials, and receipts stay in approved secret stores.
- **Hosted-service boundary**: theory-mcp-server is consumed, not vendored or redistributed.
- **Gov-infra**: any verifier change in `theory-mcp-server` must pass
  `bash gov-infra/verifiers/gov-verify-rubric.sh`; do not weaken gates.
- **lesser-host discipline** still applies: gov-infra rubric, multi-tenant isolation, consumer release verification,
  AppTheory contract for deploys.

## Files

- Offline planner: `scripts/plan-theorymcp-assisted-deploy.sh`
- Runbook: `docs/runbooks/theorymcp-namespace-assisted-deploy.md`
- This plan: `docs/theorymcp-namespace-deploy-plan.md`
