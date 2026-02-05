# lesser.host frontend roadmap (portal + operator console)

This roadmap defines the **web interfaces** for `lesser.host` (repo: `lesser-host`), covering both:

- **Self-serve** customer experiences (portal)
- **Administrative** experiences (operator/admin console + bootstrap/setup)

Backend APIs for these experiences already exist (see `docs/portal.md`, `docs/managed-instance-provisioning.md`,
`docs/tip-registry.md`). This plan focuses on shipping a secure, CSP-safe web UI that drives those APIs and fills the
remaining gaps with small, scoped API additions where required.

## Goals

- Provide a **self-serve** experience for managed Lesser hosting:
  - wallet login → create/claim slug → provision → configure → buy credits → operate
- Provide an **operator/admin console** for approvals, incident response, and governance:
  - review vanity domains + external registrations
  - manage provisioning jobs + recovery
  - administer tip registry operations (Safe-first)
- Maintain **strict CSP** and a clean security posture suitable for a public-facing control plane.

## Non-goals (v1)

- A marketing site / content CMS.
- Direct browser access to AWS accounts (no AWS credentials in the browser).
- A full “Lesser instance admin UI” (most instance-specific admin lives inside Lesser itself).

## Constraints and conventions

- **Domain:** `lesser.host` (repository name `lesser-host`).
- **Frontend components:** use `equaltoai/greater-components` (shadcn-like workflow via `greater` CLI).
- **CSP:** no `unsafe-inline` (allow nonces/hashes only if truly required; prefer “no inline scripts/styles”).
- **Stages:** all infrastructure and resource naming follows the app/stage prefix convention (e.g., `lesser-host-live-*`,
  `lesser-host-lab-*`).
- **No NPM releases:** UI artifacts ship with repository releases (GitHub releases) as part of infra deploys; dependency
  installation is still deterministic (lockfiles + CI).

## Roles and primary user journeys

### Public (unauthenticated)

- Verify an attestation by id (read-only): `/attestations/{id}`
- Look up an attestation by `(actor_uri, object_uri, content_hash, module, policy_version)` (read-only)
- Tip registry external registration (optional public UX wrapper around APIs in `docs/tip-registry.md`)

### Customer (self-serve)

- Wallet login (auto-provisions a `customer` account on first login)
- Create/claim a slug
- Start provisioning (managed hosting)
- View provisioning progress + primary domain
- Configure instance features (previews/safety/renders/moderation/AI settings)
- Manage vanity domains (proof + verification + status)
- Manage instance keys (create + copy once)
- View budgets/usage + purchase credits + manage payment method

### Operator / Admin

- Bootstrap the control plane on first deploy (setup wizard)
- Login via wallet and/or WebAuthn (passkey)
- Review/approve vanity domain activation requests
- Review/approve external instance registration requests
- Manage provisioning jobs (observe step-by-step, retry semantics, operator-friendly failure path)
- Administer tip registry operations (Safe-first payloads + execution reconciliation)
- View audit trail and system health dashboards

## System inventory (what the UI drives)

This roadmap assumes the UI primarily drives these existing HTTP surfaces:

- **Setup (bootstrap-only):** `/setup/status`, `/setup/bootstrap/*`, `/setup/admin`, `/setup/finalize`
- **Auth:**
  - Operator wallet auth: `POST /auth/wallet/challenge`, `POST /auth/wallet/login`
  - Portal wallet auth: `POST /api/v1/portal/auth/wallet/challenge`, `POST /api/v1/portal/auth/wallet/login`
  - WebAuthn/passkeys: `/api/v1/auth/webauthn/*`
- **Self-serve portal:** `/api/v1/portal/*` (instances, domains, keys, budgets, usage, billing)
- **Operator console (approvals):** `/api/v1/operators/*`
- **Tip registry (public + admin):** `/api/v1/tip-registry/*`
- **Trust public:** `/.well-known/jwks.json`, `/attestations*`

## Proposed frontend architecture (v1)

### App layout

- Add a dedicated web app at `web/`:
  - React + TypeScript + Vite (static build; no server required)
  - route-based SPA with role-gated navigation
  - `greater-components` for UI primitives and styling
- Deploy as static assets behind CloudFront (or equivalent), with:
  - default origin: `web` static bucket
  - API origin(s): control-plane and trust API endpoints (path-based routing)

This avoids CORS, keeps tokens first-party, and allows strict CSP on a single origin.

### Auth/session handling

- Use bearer tokens returned by the API, but avoid long-lived storage:
  - default: in-memory + sessionStorage (or encrypted storage) to reduce XSS blast radius
  - future option: switch to cookie-based sessions (requires API changes) if desired
- Provide explicit “logout” UX (may require adding a session revoke endpoint).

### CSP policy baseline (target)

Default (tight) baseline:

- `default-src 'none'`
- `script-src 'self'` (no inline; add `strict-dynamic` only if needed)
- `style-src 'self'`
- `img-src 'self' data: blob:`
- `connect-src 'self'` (expand only when adding walletconnect or other providers)
- `font-src 'self'`
- `frame-ancestors 'none'`

Notes:
- Avoid third-party scripts (Stripe Checkout uses redirect URLs; no Stripe.js required).
- If WalletConnect is added, update `connect-src`/`frame-src` to only the required endpoints.

### Testing strategy

- Unit tests for utilities + API client schemas.
- Playwright E2E flows for:
  - setup bootstrap
  - portal login → create instance → provision → buy credits (mock) → domain proof
  - operator review flows

## Milestones

### FE0 — Frontend foundation + deployment + CSP

Deliverables:
- Create `web/` app scaffold (Node ≥ 24), with:
  - TypeScript, router, data-fetching layer, and a small design system wrapper
  - `greater-components` integration (`greater init`, then additive components via `greater add ...`)
  - strict lint/typecheck and a deterministic lockfile
- CDK support to deploy the portal assets:
  - stage-scoped static bucket + CloudFront distribution
  - HTTPS + DNS wiring for `lesser.host` + stage variants (e.g., `lab.lesser.host`)
  - CSP + security headers at the CDN layer (no `unsafe-inline`)
- Local dev workflow:
  - `web` dev server proxied to the control-plane API
  - documented `.env` contract for API base URLs and chain IDs

Acceptance criteria:
- Production build serves with a CSP that blocks inline scripts/styles (verified in browser devtools).
- The UI can call `GET /setup/status` and display environment/stage + locked/active state.
- CI runs `web` typecheck + build in addition to existing repo checks.

---

### FE1 — Setup wizard (first deploy bootstrap)

Deliverables:
- `/setup` wizard UI driving:
  - show current state (`/setup/status`)
  - bootstrap wallet challenge + verify (`/setup/bootstrap/*`)
  - create primary admin (`/setup/admin`)
  - finalize (`/setup/finalize`)
- Guardrails:
  - strong warnings and confirmations on finalize/lock
  - clear error handling for “bootstrap wallet not configured” and signature mismatch

Acceptance criteria:
- A brand-new deployment can be bootstrapped end-to-end using only the UI and a wallet signature.
- After finalize, setup pages become read-only and direct the user to login.

---

### FE2 — Authentication UX (wallet + passkeys) + account settings

Deliverables:
- Unified login entry that supports:
  - customer login (portal wallet flow)
  - operator/admin login (wallet flow + optional username)
  - WebAuthn login for operator/admin accounts (passkeys)
- Session UX:
  - token storage policy, session expiry messaging, and logout
  - “Who am I” panel using `/api/v1/portal/me` and `/api/v1/operators/me`
- Account settings:
  - passkey registration and management (`/api/v1/auth/webauthn/*`)
  - basic profile display (username, role, linked wallet)

Acceptance criteria:
- A user can login as a customer and reach the portal home without touching developer tools.
- An operator can login with a passkey and access operator-only routes.
- Passkey CRUD works end-to-end and is resilient to expired challenges.

---

### FE3 — Self-serve portal: instances + provisioning lifecycle

Deliverables:
- Portal home + instance list:
  - `GET /api/v1/portal/instances`
  - empty state and “create instance” flow (`POST /api/v1/portal/instances`)
- Instance detail:
  - instance status + primary domain
  - copyable endpoints and key identifiers
- Managed provisioning UX:
  - start provisioning (`POST /api/v1/portal/instances/{slug}/provision`)
  - show job state + human-friendly step mapping (`GET /api/v1/portal/instances/{slug}/provision`)
  - failure UX: display `error_code`, `error_message`, and operator-visible `note`

Acceptance criteria:
- A customer can create a slug, start provisioning, and watch progress without page refreshes.
- Provisioning status updates are polled with backoff and stop on terminal states.
- Job failures present actionable next steps (e.g., “contact support with job id + request id”).

---

### FE4 — Self-serve portal: domains + instance keys

Deliverables:
- Domains UI:
  - list domains (`GET /api/v1/portal/instances/{slug}/domains`)
  - add vanity domain and show TXT instructions (`POST /api/v1/portal/instances/{slug}/domains`)
  - verify (`POST /api/v1/portal/instances/{slug}/domains/{domain}/verify`)
  - rotate/disable/delete
  - show “verified → pending operator approval → active” lifecycle clearly
- Instance keys UI:
  - create key (`POST /api/v1/portal/instances/{slug}/keys`)
  - display plaintext **once** with explicit copy-to-clipboard + warnings

Acceptance criteria:
- Vanity domain proof instructions are copy/paste correct and include record name + value.
- Verified vanity domains show “awaiting approval” until operator action.
- Instance key plaintext is never displayed again after leaving the page.

---

### FE5 — Self-serve portal: usage, budgets, and payments

Deliverables:
- Budgets UI:
  - list/get/set month budgets (`/api/v1/portal/instances/{slug}/budgets*`)
  - show included credits vs used credits, and remaining balance
- Usage UI:
  - ledger list + summary (`/api/v1/portal/instances/{slug}/usage/{month}*`)
  - cache hit rate, and list vs discounted credits surfaced clearly
- Credits purchase UX:
  - create checkout session → redirect
  - list purchases (receipt export)
- Payment methods UX:
  - setup checkout → redirect
  - list payment methods

Acceptance criteria:
- A customer can buy credits and see `included_credits` increase after webhook reconciliation.
- Usage summary reflects cache hit rates and discounts accurately.
- Payments UI works without third-party scripts (redirect-only).

---

### FE6 — Self-serve portal: configuration + AI cost policies

Deliverables:
- Instance configuration UI (`PUT /api/v1/portal/instances/{slug}/config`) for:
  - previews/safety/renders toggles + render policy
  - moderation enable/trigger/virality threshold
  - AI enable/model set/batching mode/batch limits/pricing multiplier/max inflight
- Cost/safety guardrails:
  - explain “batching mode” and tradeoffs (latency vs cost)
  - warn before enabling expensive features (claim checks, render-always)

Acceptance criteria:
- Config changes are validated client-side and server-side errors are rendered with field-level context.
- Config toggles are immediately reflected on reload and in the instance detail view.

---

### FE7 — Operator console v1: approvals + support

Deliverables:
- Operator console shell with role gating (`admin` vs `operator`) and audit-friendly UI.
- Review flows:
  - vanity domain requests list + approve/reject
  - external instance registrations list + approve/reject
- Basic support tools:
  - instance search (by slug)
  - instance detail view (read-only initially) with:
    - ownership, config, domains, budgets, provisioning status
    - links to relevant job ids + request ids

Acceptance criteria:
- Operators can clear approval queues end-to-end and see state changes reflected immediately.
- Support UX enables “copy/paste a slug → get full state” within one screen.

---

### FE8 — Operator console v2: provisioning operations + recovery

Deliverables:
- Provisioning job observability:
  - list jobs across instances (requires a small API addition; see “Backend gaps”)
  - job detail view with step history and recovery guidance
  - deep links to runner `run_id` (CodeBuild) and stored receipt metadata
- Recovery actions:
  - retry/resume controls (requires API additions to safely requeue or reset job step)
  - operator notes appended to job `note` (requires API addition)

Acceptance criteria:
- An operator can diagnose a stuck/failed provisioning job and safely retry it from the UI.
- Recovery actions are audited and idempotent.

---

### FE9 — Operator console v3: tip registry administration + audit log explorer

Deliverables:
- Tip registry UI:
  - list operations by status, view operation detail
  - copy Safe payload (`to/value/data`) and reconcile execution by tx hash
  - manage token allowlist
  - host ensure + host active toggles
- Audit log explorer (requires API addition):
  - filter by actor/action/target/time
  - drill into request id and related objects

Acceptance criteria:
- Operators can run the full Safe-first loop from UI: “pending op → proposed → executed → recorded”.
- Audit explorer makes it possible to answer “who changed what and when” for portal + operator actions.

---

### FE10 — Trust and evidence UX (optional but high-leverage)

Deliverables:
- Public attestation inspector:
  - fetch by id and render decoded header/payload
  - copy verification instructions and JWKS key ids
- Instance owner dashboards (requires backend additions unless reusing existing endpoints):
  - recent publish jobs and safety summaries
  - claim verification outcomes (where enabled)
  - evidence pack viewer (render thumbnail/snapshot) with access controls

Acceptance criteria:
- A third party can paste an attestation id and get a human-readable, verifiable view.
- Instance owners can see recent trust/safety outcomes without accessing AWS consoles.

## Backend gaps (tracked as scoped follow-ups)

The following backend additions are recommended to fully support the operator/admin UI:

- **Session revoke/logout endpoint** (delete `OperatorSession`).
- **Audit log listing endpoint** with filters and pagination.
- **Provisioning job list + controls** (list across instances; safe requeue/reset step; append operator notes).
- **Instance-level trust dashboards** endpoints (optional; can be later).

These are intentionally small; the UI should ship early using the existing API surface wherever possible.

## Cross-cutting acceptance criteria (apply to every frontend milestone)

- **Security:** strict CSP (no `unsafe-inline`), no third-party scripts by default, role-gated routes, safe token storage.
- **Accessibility:** keyboard navigable, screen-reader labels, visible focus, and contrast compliant.
- **Reliability:** clear error states; retries/backoff for long-running polling; no “stuck spinners”.
- **Observability:** client-side request ids surfaced for support; basic client telemetry (no PII by default).
- **Performance:** <200KB critical CSS, route-level code splitting, and fast cold-start UX.

