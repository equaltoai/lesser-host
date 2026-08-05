# Self-owned TheoryMCP namespace standup for lesser-host

`lesser-host` deployment flows surface TheoryMCP (the `theory-cloud/theory-mcp-server` remote MCP surface) when an
agent, CLI, or deploy-guidance pack needs routed MCP access. The hosted TheoryMCP service (`theorymcp.ai`) enforces
Autheory-issued OAuth access-token validation against its own namespaces. This runbook stands up a **self-owned
`theory-mcp-server`** instance with a **self-owned namespace** so lesser-host operators and agents authenticate
against their own deployment instead of the hosted surface.

The self-owned instance uses the lab-only **harness namespace-key** auth path (SHA-256 namespace keys stored in a
Secrets Manager registry), which removes the Autheory OAuth dependency for lab workflows. The hosted service's OAuth
path remains authoritative for live; this runbook is explicitly a lab/development posture.

## Scope

- Applies to `lesser-host` operators who need MCP-backed workflows without hosted TheoryMCP credentials.
- Applies to the `theory-cloud/theory-mcp-server` repository (internal).
- Lab only. Harness namespace keys **fail closed on live** by design (`internal/runtimeconfig/harness_keys.go`).

## Prerequisites

- Access to `theory-cloud/theory-mcp-server` (internal GitHub repo).
- AWS profile named `Mcp` in the deploy account (the repo convention).
- A Route 53 hosted zone you own (used for `api.<stage>.<zone>` and the public MCP domain).
- Go `1.26.5`, Node `>=24 <25` for `cdk/`.

## Step 1 — Clone and check out the deploy source branch

`theory-mcp-server` promotes `staging -> premain -> main -> staging`. `premain` is the source branch for `lab`.

```bash
git clone https://github.com/theory-cloud/theory-mcp-server.git
cd theory-mcp-server
git checkout premain
```

## Step 2 — Deploy your own theory-mcp-server instance

The deploy contract lives in `app-theory/app.json`. The canonical install command is:

```bash
./scripts/theorycloud-install --stage lab --profile Mcp
```

`app-theory/app.json` declares the following parameters (override only via `--param name=value`):

| Parameter | Default | Notes |
| --- | --- | --- |
| `hostedZoneId` | `Z0394708286VLXKNCUHBX` | Your Route 53 zone id |
| `hostedZoneName` | `theorymcp.ai` | Your Route 53 zone name |
| `namespaceVendingCallerPrincipalArn` | theory-cloud account role | Must be replaced with your own `arn:aws:iam::<ACCOUNT>:role/theorycloud-ai-namespace-vending-<stage>` |
| `namespaceVendingGenomeId` | `progenitor-for-namespace` | Server-pinned; do not change |
| `namespaceVendingGenomeVersion` | `1.0.0` | Server-pinned; do not change |
| `namespaceVendingGenomeChecksum` | `sha256:61c6...ee600` | Server-pinned; do not change |

Example self-owned override:

```bash
./scripts/theorycloud-install --stage lab --profile Mcp \
  --param hostedZoneId=ZXXXXXXXXXXXXX \
  --param hostedZoneName=example.com \
  --param namespaceVendingCallerPrincipalArn=arn:aws:iam::123456789012:role/theorycloud-ai-namespace-vending-lab
```

The CDK construct validates that the caller role ARN targets the deploy account with role name
`theorycloud-ai-namespace-vending-<stage>` (see `cdk/lib/namespace-vending.ts`), so create that role first and give
the `Mcp` profile permission to assume it.

## Step 3 — Configure the harness namespace-key registry

The lab harness auth path reads a Secrets Manager secret. Default secret name (lab):
`theory-mcp-server/lab/mcp-harness-namespace-keys` (override via
`THEORY_MCP_HARNESS_NAMESPACE_KEY_SECRET_NAME`).

The secret payload is a JSON registry:

```json
{
  "keys": [
    {
      "client_namespace": "<your-namespace>",
      "key_id": "host-lab",
      "token_sha256": "<sha256 hex of your raw namespace key>",
      "expires_at": "2027-01-01T00:00:00Z",
      "status": "active",
      "scopes": ["mcp:tools", "ai.kb.query", "memory.append"]
    }
  ]
}
```

Mint a raw key and store only its SHA-256:

```bash
RAW_KEY="$(openssl rand -hex 32)"
printf '%s' "$RAW_KEY" | shasum -a 256   # this hex goes into token_sha256
# keep RAW_KEY private; it is the bearer token
```

## Step 4 — Seed your namespace via the internal namespace-vending API

The internal API path is `POST /v1/internal/namespace-vending` on the namespace-vending gateway
(`api.<stage>.<zone>`). It is SigV4-only and caller-restricted to the `namespaceVendingCallerPrincipalArn` role.
It seeds tenant, namespace, agent accounts, soul/instructions, skill-bank assets, install layouts, and lifecycle
packs for the routed namespace.

Use the documented request contract in `internal/vending/` (the handler is registered in `cmd/api/namespace_vending.go`).
After seeding, the namespace route `/{your-namespace}/mcp` and agent routes
`/{your-namespace}/agents/{agent}/mcp` resolve against your instance.

## Step 5 — Authenticate MCP clients against your namespace

Connect an MCP client (Codex, Claude Code, opencode, or the theory CLI guidance flow) to your self-owned route with
the harness namespace key as the bearer token:

```json
{
  "mcpServers": {
    "theory-mcp-self": {
      "url": "https://<your-domain>/<your-namespace>/mcp",
      "headers": {
        "Authorization": "Bearer <RAW_KEY>"
      }
    }
  }
}
```

## Step 6 — Proceed with lesser-host deploy guidance

With the self-owned route authenticated, lesser-host deploy preparation (`theory app up --stage lab --execute`
per the AppTheory contract at `app-theory/app.json`) no longer depends on hosted TheoryMCP credentials for the
guidance/agent surfaces. Keep normal lesser-host discipline: gov-infra verifiers, multi-tenant isolation,
consumer release verification, and no direct CDK invocation outside the AppTheory wrapper.

## Rollback

- Remove the namespace key from the Secrets Manager registry (or set `status` to a non-active value).
- `./scripts/theorycloud-uninstall --stage lab --profile Mcp` destroys the self-owned instance.

## Hard boundaries

- Harness namespace keys are **lab-only**; the runtime rejects them for live stages (`internal/runtimeconfig/harness_keys.go`).
- Never commit raw namespace keys or the registry payload.
- Do not weaken the hosted service's live OAuth posture; this runbook is a lab/development path only.
