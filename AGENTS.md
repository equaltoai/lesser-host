# lesser-host (lesser.host) — Agent Notes

This repo (`lesser-host`) is the control plane for `lesser.host`: managed hosting + governance + trust/safety services for
the `lesser` ecosystem.

If you’re an automated agent working in this repo, this doc is the “how it works” map: architecture, auth, and
deployment.

## What is `lesser-host`?

`lesser-host` is a multi-service AWS-backed control plane that:

- **Provisions and manages hosted `lesser` instances** (managed AWS accounts + DNS under a parent domain).
- **Tracks instance configuration and metered usage** (budgets + credits ledger).
- **Provides centralized trust/safety utilities** (public attestations + optional instance-scoped services like previews,
  safety checks, and moderation signals).
- **Operates host governance** (tip host registry operations; Safe-ready transaction payloads).

Key design constraints:
- Everything is served from a **single first-party origin** (CloudFront) to avoid CORS and support a **strict CSP**
  (no `unsafe-inline`).
- **Secrets do not live in git**; runtime credentials are loaded from AWS SSM Parameter Store.

## Relationship to `lesser`

`lesser` is the product deployed per tenant (“instance”). `lesser-host` is the platform around it:

- **Managed instances:** `lesser-host` provisions a dedicated AWS account per slug and runs `lesser up` (via a CodeBuild
  runner) to deploy `lesser` into that account. The deploy receipt is ingested back into the control plane.
- **External instances:** operators can approve registrations for independently-managed `lesser` installs, so they still
  appear in the control plane (without managed DNS/provisioning).

References:
- Managed provisioning flow: `docs/managed-instance-provisioning.md`
- Lesser deploy outputs contract: `docs/lesser-release-contract.md`
- Portal API surface: `docs/portal.md`

## Repo map (where things live)

- **Go services (Lambda entrypoints):** `cmd/`
  - `cmd/control-plane-api` — control plane HTTP API
  - `cmd/trust-api` — public trust endpoints + instance-scoped trust services
  - `cmd/provision-worker` — managed provisioning worker (SQS)
  - `cmd/render-worker` — render/retention worker (SQS + scheduled sweep)
  - `cmd/ai-worker` — AI jobs worker (SQS)
- **Core implementation:** `internal/`
  - `internal/controlplane` — operator + portal APIs, provisioning orchestration, billing/budgets, tip registry ops
  - `internal/trust` — attestations, previews/renders/safety, instance-key auth, AI evidence/moderation endpoints
  - `internal/store` — TableTheory models + DynamoDB access
  - `internal/secrets` — SSM Parameter Store reads (Stripe, AI providers, etc)
- **Infra (AWS CDK):** `cdk/`
- **Web UI (SPA):** `web/` (Svelte 5 + Vite + TypeScript)
- **Specs/contracts:** `contracts/`
- **Docs:** `docs/`
- **Deployment records:** `docs/deployments/` (latest on-chain addresses + required Safe admin calls; Sepolia: `docs/deployments/sepolia/latest.json` + `docs/deployments/sepolia/safe-tx-builder-post-deploy.json`)

## HTTP routing model (single-origin)

Deployed traffic flows through a CloudFront distribution defined in `cdk/lib/lesser-host-stack.ts`:

- Default origin: **static** `web/` build output (S3)
- Path-based origins:
  - `/api/*`, `/auth/*`, `/setup/*` → **control-plane-api** (Lambda Function URL origin)
  - `/.well-known/*`, `/attestations*` → **trust-api** (Lambda Function URL origin)

SPA routing is implemented via a CloudFront Function that rewrites “non-file, non-API” paths to `/index.html`. If you
add a new backend route, ensure it is excluded from the rewrite function and included in `additionalBehaviors`.

## Authentication model (overview)

There are **two** bearer-token systems in this repo:

1) **Control plane sessions** (humans): random bearer tokens stored server-side in DynamoDB.
   - Used by operators/admins and portal customers.
   - Auth hook: `internal/controlplane/auth_operator.go` (`OperatorAuthHook`)

2) **Instance API keys** (machine-to-machine): plaintext key presented as bearer token; server stores only `sha256(key)`.
   - Used by `lesser` instances calling trust APIs.
   - Auth hook: `internal/trust/auth_instance.go` (`InstanceAuthHook`)

### Setup bootstrap (first deploy only)

Setup endpoints live under `/setup/*` and are intended for the very first deploy:

- `GET /setup/status`
- `POST /setup/bootstrap/challenge`
- `POST /setup/bootstrap/verify` → returns a short-lived **setup session** token
- `POST /setup/admin` (requires setup session) → creates the first `admin` user + links wallet
- `POST /setup/finalize` (requires admin auth) → marks control plane active

Bootstrap is gated by `BOOTSTRAP_WALLET_ADDRESS` (configured via CDK context → Lambda env var). See
`internal/controlplane/handlers_setup.go`.

### Operator/admin login (wallet)

Wallet auth is a challenge/response signature flow:

- `POST /auth/wallet/challenge` (public) → returns a message to sign
- `POST /auth/wallet/login` (public) → verifies signature + returns bearer session token

Sessions are created by `internal/controlplane/operator_sessions.go` and validated on each request by the auth hook.

### Operator/admin login (passkeys / WebAuthn)

WebAuthn is supported for operator/admin accounts:

- `POST /api/v1/auth/webauthn/register/begin` (auth required)
- `POST /api/v1/auth/webauthn/register/finish` (auth required)
- `POST /api/v1/auth/webauthn/login/begin` (public)
- `POST /api/v1/auth/webauthn/login/finish` (public) → returns bearer session token
- Credential management:
  - `GET /api/v1/auth/webauthn/credentials` (auth required)
  - `PUT/DELETE /api/v1/auth/webauthn/credentials/{credentialId}` (auth required)

WebAuthn relies on:
- `WEBAUTHN_RP_ID`
- `WEBAUTHN_ORIGINS` (CSV)

These are set via CDK context in `cdk/cdk.json` and passed as Lambda env vars.

### Portal (customer) login (wallet)

Portal auth is a wallet-signature flow that auto-creates a `customer` account on first login:

- `POST /api/v1/portal/auth/wallet/challenge`
- `POST /api/v1/portal/auth/wallet/login`
- `GET /api/v1/portal/me`

The returned bearer token is the same session type used elsewhere in the control plane; access to portal resources is
enforced by role + ownership checks in handlers.

### Logout

- `POST /api/v1/auth/logout` (auth required) deletes the session record.

### Trust API auth (instance keys)

The trust API uses **instance API keys** (created in the control plane) as bearer tokens. The server:
- hashes the raw key (`sha256`)
- looks up `InstanceKey` by id
- sets `ctx.AuthIdentity` to the **instance slug**

See `internal/trust/auth_instance.go`.

Public trust endpoints (no auth):
- `GET /.well-known/jwks.json`
- `GET /attestations` (lookup)
- `GET /attestations/{id}`

## Deployment (AWS CDK + AppTheory contract)

### Recommended: `theory app up/down`

The deploy contract is `app-theory/app.json`. It drives CDK deploy/destroy with stage substitution.

Examples:

```bash
AWS_PROFILE=my-profile theory app up --stage lab
AWS_PROFILE=my-profile theory app down --stage lab
```

### Manual CDK deploy

`cdk/bin/lesser-host.ts` reads the stage from CDK context (`-c stage=...`, default `lab`) and deploys a stack named
`lesser-host-<stage>`.

```bash
cd cdk
npm ci
AWS_PROFILE=my-profile npx cdk deploy --all -c stage=lab --require-approval never
```

### What CDK deploys (high level)

Defined in `cdk/lib/lesser-host-stack.ts`:

- DynamoDB state table: `${app}-${stage}-state` (TableTheory PK/SK model)
- S3 buckets:
  - artifacts bucket (render artifacts, managed provisioning receipts, etc.)
  - web bucket (static SPA assets)
- SQS queues:
  - preview queue, safety queue, provision queue
- KMS key:
  - RSA signing key for attestations (published via JWKS)
- Lambda functions (custom runtime `PROVIDED_AL2023`):
  - control plane API + trust API + workers
- CodeBuild project:
  - managed provisioning runner that fetches a `lesser` release and runs `lesser up`
- CloudFront distribution:
  - strict CSP and security headers
  - path routing to control-plane and trust origins
  - SPA rewrite function
- Optional Route53 records + ACM cert (when hosted zone context is provided)

### Secrets and SSM parameters

The runtime loads credentials from SSM Parameter Store (SecureString where appropriate). See `internal/secrets/keys.go`.

Stripe (payments) looks up stage-scoped names first, then falls back to legacy paths:

- Secret key:
  - `/lesser-host/stripe/${STAGE}/secret` (preferred)
  - `/lesser-host/api/stripe/secret` (legacy fallback)
- Webhook signing secret:
  - `/lesser-host/stripe/${STAGE}/webhook` (preferred)
  - `/lesser-host/api/stripe/webhook` (legacy fallback)

AI provider keys are also loaded from SSM (paths in `internal/secrets/keys.go`).

## Web UI

The SPA lives in `web/` and is deployed as static assets behind CloudFront.

- Local dev: `cd web && npm ci && npm run dev`
- Dev proxy targets (optional): `web/.env.example`
- Session storage: the frontend stores bearer session data in `sessionStorage` (see `web/src/lib/session.ts`).

Important: CSP is enforced at CloudFront (`script-src 'self'`, `style-src 'self'`). Do not add inline scripts/styles.

## Governance standards (`gov-infra`)

The `gov-infra/` system is a core part of how this application is built and maintained. It is not optional process
overhead or “cleanup later” work. The rubric, controls matrix, threat model, evidence plan, and verifier together define
what “up to standard” means for `lesser-host` across quality, consistency, security, compliance, and maintainability.

- Canonical full standards entrypoint: `bash gov-infra/verifiers/gov-verify-rubric.sh`
- Canonical planning docs:
  - `gov-infra/planning/lesser-host-10of10-rubric.md`
  - `gov-infra/planning/lesser-host-controls-matrix.md`
  - `gov-infra/planning/lesser-host-evidence-plan.md`
  - `gov-infra/planning/lesser-host-threat-model.md`
- Canonical evidence output: `gov-infra/evidence/`

If you change behavior that affects controls, threat coverage, evidence, CI enforcement, supply-chain policy, lint/tool
pins, or verification scope, update the corresponding `gov-infra` docs and/or verifier logic in the same change.

Do not weaken standards silently just to get green checks. If a verifier or rubric requirement needs to change, treat
that as a policy change: update the rubric/docs/evidence surface intentionally and keep the change explicit.

## Testing and linting

Go:
- `go test ./...`

Web:
- `cd web && npm run lint`
- `cd web && npm run typecheck`
- `cd web && npm test`

CDK:
- `cd cdk && npm run synth`

Governance:
- `bash gov-infra/verifiers/gov-verify-rubric.sh`

## Common pitfalls

- **SPA rewrites:** new backend routes must be excluded in the CloudFront rewrite function and added to
  `additionalBehaviors` to avoid being rewritten to `/index.html`.
- **Stage drift:** `STAGE` defaults to `lab` in Go config; ensure CDK stage, Lambda env `STAGE`, and SSM parameter paths
  agree.
- **Strict CSP:** keep UI CSP-safe; prefer first-party redirects (e.g., Stripe Checkout redirect) instead of embedding
  third-party scripts.
- **No secrets in git:** only commit parameter *names* (paths), never values.
