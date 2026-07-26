# Portal (M10)

This document describes the **self-serve portal API** for `lesser.host` and the supporting operator review endpoints.

The portal is currently API-first (frontend is tracked separately). All portal endpoints use the same bearer session
token mechanism as operator auth, but portal wallet login auto-creates a `customer` user on first login.

## Canonical Soul Flow

For soul creation, review, approval, and finalize UX, the canonical user-facing path is now the agent-first
Simulacrum client served from Lesser at `/l/*`.

Portal soul routes remain supported as secondary, fallback, or operator-guided surfaces. They are human control-plane
routes backed by portal/operator bearer sessions; they are not the production credential bridge for Simulacrum.
Specifically, a browser-held `lesser-host` control-plane bearer token bootstrap is superseded and must not be presented
as the primary product path.

The production Project 44 path is:

```text
Simulacrum browser -> Lesser same-origin auth/API -> Lesser server-side instance trust -> lesser-host scoped instance-key write API
```

The missing `lesser-host` instance-key soul creation/write route family is tracked by
[Project 44 M1 issue #706](https://github.com/equaltoai/lesser-host/issues/706). Until that route family lands, portal
soul routes remain admin/operator tooling and controlled fallback surfaces, not the canonical human-facing Simulacrum
soul creation path.

Hosted-bound-soul and public x402 surfaces remain launch-gated until
`docs/hosted-bound-soul-launch-gates.md` records accepted terminology, payment/refund/failure, and comms consent
disclosures and the parent launch milestone explicitly unblocks public clients. Portal or Simulacrum copy must not imply
that `immutable_onchain` is a prerequisite for basic hosted-bound-soul function, and public x402 callers must never be
presented as receiving principal/operator authority.

## Authentication

### Portal wallet login (public)

Portal wallet login issues a `lesser-host` control-plane session for portal/customer or operator-admin work. It is not a
credential to bootstrap into Simulacrum's browser runtime; Simulacrum production flows should authenticate to Lesser
same-origin first and let Lesser call `lesser-host` server-side through scoped instance-key routes.

1) Create a challenge:

- `POST /api/v1/portal/auth/wallet/challenge`
- body: `{ "address": "0x...", "chainId": 1 }`

2) Sign the returned `message` with the wallet, then exchange for a bearer token:

- `POST /api/v1/portal/auth/wallet/login`
- body: `{ "challengeId": "...", "address": "0x...", "signature": "...", "message": "...", "email": "...", "display_name": "..." }`

Response includes:
- `token_type`: `"Bearer"`
- `token`: bearer token
- `expires_at`: session expiry
- `username`: portal username (`wallet-<hex-address>`)
- `role`: `customer`

### Who am I?

- `GET /api/v1/portal/me` (requires `Authorization: Bearer ...`)

## Instances (self-serve)

All instance endpoints require auth. Access rules:
- Operators (`admin`/`operator`) can access any instance.
- Customers can access only instances where `instance.owner == <username>`.

### Create / list / get

- `POST /api/v1/portal/instances` (create/claim a slug)
- `GET /api/v1/portal/instances`
- `GET /api/v1/portal/instances/{slug}`

Creating an instance also creates a managed **primary domain** record for `slug.<MANAGED_PARENT_DOMAIN>` (default:
`slug.greater.website`).

Instance responses include `managed: true` when the instance has lesser.host-managed DNS/provisioning metadata
(`hosted_base_domain`). Fresh portal-managed instances are `managed: true` before `hosted_region` or
`hosted_account_id` exist; externally registered instances are `managed: false`.

### Configure instance features

- `PUT /api/v1/portal/instances/{slug}/config`

Config includes (subject to roadmap evolution):
- previews/safety/renders toggles and policies
- moderation provider flags
- AI model alias (`gpt-5.6-luna` or `claude-sonnet-5` for Hosted Genesis),
  batching mode, and pricing multiplier; provider:model remains an advanced
  internal compatibility form

### Provisioning (managed hosting)

- `POST /api/v1/portal/instances/{slug}/provision`
- `GET /api/v1/portal/instances/{slug}/provision`

Managed provisioning is described in `docs/managed-instance-provisioning.md`.

### Managed updates

- `POST /api/v1/portal/instances/{slug}/updates`
- `GET /api/v1/portal/instances/{slug}/updates`

Update jobs return the failure and recovery fields operators need directly from the API surface, including:

- `status`, `step`, `note`, `active_phase`, `failed_phase`
- `error_code`, `error_message`
- `run_url`, `deploy_run_url`, `body_run_url`, `mcp_run_url`
- `deploy_status`, `deploy_error`, `body_status`, `body_error`, `mcp_status`, `mcp_error`

The canonical retry/recovery contract for failed release-driven managed updates is documented in
`docs/managed-update-recovery.md`.

For `body_only` updates, portal and operator surfaces should present the `error_code` distinctions directly:

- `body_release_preflight_failed`: the requested `lesser-body` release was rejected before CodeBuild started. Expect
  `failed_phase=body`, `body_status=failed`, and usually no `body_run_url`.
- `body_deploy_failed`: the `lesser-body` runner reached a terminal CodeBuild failure. Expect `failed_phase=body`,
  `body_status=failed`, a preserved `body_run_url` / `run_url` when the runner deep link was observed, and
  `body_error` / `error_message` that keep the underlying helper or CloudFormation validation detail when the runner
  uploaded it.
- `body_receipt_load_failed`: the body runner may have completed, but receipt ingest failed afterward. Expect
  `failed_phase=body`, `body_status=failed`, and a `body_error` / `error_message` that names receipt loading.

Portal and operator UIs should use those fields to explain recovery steps instead of asking operators to inspect raw
table state. Once the latest job is terminal `error`, the supported recovery path is simply to correct the underlying
release or receipt problem and submit a fresh `POST /api/v1/portal/instances/{slug}/updates` request.

### Budgets

- `GET /api/v1/portal/instances/{slug}/budgets` (list)
- `GET /api/v1/portal/instances/{slug}/budgets/{month}` (YYYY-MM)
- `PUT /api/v1/portal/instances/{slug}/budgets/{month}`

### Usage + cache hit rates + discounts

- `GET /api/v1/portal/instances/{slug}/usage/{month}` (ledger entries)
- `GET /api/v1/portal/instances/{slug}/usage/{month}/summary`

The summary includes:
- `cache_hit_rate` derived from ledger entries where `cached=true`
- `list_credits` (pre-discount) vs `requested_credits` (after discount)
- `discount_credits = max(0, list_credits - requested_credits)`

## Domains (vanity)

### List / add / verify

- `GET /api/v1/portal/instances/{slug}/domains`
- `POST /api/v1/portal/instances/{slug}/domains` (returns TXT proof instructions)
- `POST /api/v1/portal/instances/{slug}/domains/{domain}/verify` (performs DNS TXT lookup)

### Rotate / disable / delete

- `POST /api/v1/portal/instances/{slug}/domains/{domain}/rotate` (new TXT token, re-proof required)
- `POST /api/v1/portal/instances/{slug}/domains/{domain}/disable`
- `DELETE /api/v1/portal/instances/{slug}/domains/{domain}`

On successful verification of a vanity domain, `lesser.host` creates/updates a `VanityDomainRequest` for operator review.

## Instance keys

- `POST /api/v1/portal/instances/{slug}/keys`

Returns a plaintext instance key once. The stored ID is the SHA-256 of the plaintext key.

## Billing (credits + payment method)

### Configuration

Env vars:
- `PAYMENTS_PROVIDER` (`stripe` to enable Stripe; default `none`)
- `PAYMENTS_CENTS_PER_1000_CREDITS` (pricing policy)
- `PAYMENTS_CHECKOUT_SUCCESS_URL`
- `PAYMENTS_CHECKOUT_CANCEL_URL`

SSM parameters (SecureString):
- `/lesser-host/api/stripe/secret` (Stripe secret key)
- `/lesser-host/api/stripe/webhook` (Stripe webhook signing secret)

### Credits (purchase)

- `POST /api/v1/portal/billing/credits/checkout`
  - body: `{ "instance_slug": "...", "credits": 50000, "month": "YYYY-MM" }`
  - response: checkout URL + created `CreditPurchase`
- `GET /api/v1/portal/billing/credits/purchases` (minimal “receipt export” as JSON)

Webhook:
- `POST /api/v1/payments/stripe/webhook` (public)

On `checkout.session.completed` (payment mode), the webhook:
- marks the purchase `paid`
- adds purchased credits into `InstanceBudgetMonth.included_credits` for the target `{instance_slug, month}`

### Overage payment method

- `POST /api/v1/portal/billing/payment-method/checkout` (Stripe Setup mode)
- `GET /api/v1/portal/billing/payment-methods`

On `checkout.session.completed` (setup mode), the webhook stores:
- `BillingProfile` (default payment method id)
- `BillingPaymentMethod` (brand/last4/exp)

## Operator console (approvals)

All operator console endpoints require an authenticated session with role `admin` or `operator`.

### Vanity domain requests

- `GET /api/v1/operators/vanity-domain-requests` (pending list)
- `POST /api/v1/operators/vanity-domain-requests/{domain}/approve`
- `POST /api/v1/operators/vanity-domain-requests/{domain}/reject`

Approving a request transitions the vanity domain from `verified` → `active`.

### External instance registrations (non-managed)

Portal:
- `POST /api/v1/portal/external-instances/registrations`
- `GET /api/v1/portal/external-instances/registrations`

Operators:
- `GET /api/v1/operators/external-instances/registrations` (pending list)
- `POST /api/v1/operators/external-instances/registrations/{username}/{id}/approve`
- `POST /api/v1/operators/external-instances/registrations/{username}/{id}/reject`

Approving creates an `Instance` record owned by the user, without managed DNS/provisioning.

### Portal user approvals

Portal wallet logins auto-create a `customer` user in `pending` approval status. Operators must approve before the user
can create instances or start provisioning.

- `GET /api/v1/operators/portal-users?status=pending`
- `POST /api/v1/operators/portal-users/{username}/approve`
- `POST /api/v1/operators/portal-users/{username}/reject`

## Audit trail

All meaningful portal + operator actions write `AuditLogEntry` records (best-effort for a few non-critical paths).
