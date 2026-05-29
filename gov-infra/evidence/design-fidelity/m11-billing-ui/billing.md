# M11 Billing UI — Design Fidelity Evidence

**Source design.** Project 42 design fixture `portal-pages-2.jsx:3–169` (`PortalBilling`), extracted from the design workspace at `/tmp/design-sNM/lesser-host/`.

**Implementation.** `web/src/pages/portal/Billing.svelte` — complete re-skin of the `/portal/billing` page to the design's spend-analytics surface.

**Route.** `/portal/billing` via `Portal.svelte` → `Billing.svelte`.

**Fixture.** No local fixture file; the page loads real API data from:
- `GET /api/v1/portal/instances` (instance list)
- `GET /api/v1/portal/instances/{slug}/cost` (cost telemetry, per instance)
- `GET /api/v1/portal/billing/invoices` (invoice list, M12 backend)
- `GET /api/v1/portal/billing/payment-method` (payment method, M12 backend)

**Screenshot.** 1440×900 capture command (Chromium headless):
```bash
npx playwright screenshot --viewport 1440x900 http://localhost:5173/portal/billing billing.png
```
_Capture pending lab deploy — the PNG in this directory is a placeholder at the correct dimensions._

### Surface elements rendered

| Element | Design reference | Implementation |
|---------|-----------------|----------------|
| Page header | `PageHeader` with eyebrow "Cost & billing", title "Where the money goes." | `PageTitle` with eyebrow, title, description, actions |
| MTD metric | `Metric` card with spend/budget sub | `Metric` component with formatCurrency(totalMtd) |
| Projected EOM metric | `Metric` card with trailing label | `Metric` component with formatCurrency(totalProjected) |
| Per active user | `Metric` card with MAU denominator | Renders `—` (unavailable; MAU not on wire contract) |
| Per federated post | `Metric` card with post-count comparison | Renders `—` (unavailable; post count not on wire contract) |
| Stacked weekly bar chart | 5 weeks, stacked by instance, projection bar | SVG `<rect>` elements with per-week aggregated cost telemetry |
| Per-instance breakdown table | Table with slug, MTD, budget, projected, burn | HTML table with `ProgressBar` burn bars |
| This month rail panel | Eyebrow + value + budget + progress + sparkline + delta | `Panel` with `Eyebrow`, `ProgressBar`, `Sparkline` |
| Payment method rail panel | Masked card + brand + last4 + expiry | `Panel` rendering `PaymentMethodSafe` DTO fields only |
| Recent invoices rail panel | Invoice list with ID, date, amount, status | `Panel` rendering `InvoiceSummary` DTO fields (period_start, amount_due, status) |

### Deliberate deviations from design

1. **"Per active user" and "Per federated post" metrics.** The design fixture shows live computed values ($0.012 and $0.0004). The host API contract does not expose MAU or federated-post counts. Both metrics render `—` (explicit unavailable state) with explanatory sublabels "unavailable · MAU data not on contract" and "unavailable · post count not on contract". No invented fixture numbers; no divide-by-zero.

2. **Instance accent colors.** The design assigns per-instance accent colors from the instance data. Host's instance API does not include an accent color field. The implementation assigns deterministic colors from a fixed palette (`--ds-secondary-500`, `--ds-primary-500`, `--ds-warning-500`, `--ds-success-500`, `--ds-info-500`, `--ds-fg-3`) based on the instance's position in the list.

3. **Weekly chart projection.** The design uses the last bar as a dashed projection. The implementation computes projection by extrapolating the current partial week to a full 7-day week using the fraction of days elapsed.

4. **Instance budget.** Host's instance API does not expose per-instance budgets. The implementation uses hardcoded fallback values for known slugs and a default of $25 for unknown slugs. This will be replaced when per-instance budget data lands.

5. **Export CSV and May invoice buttons.** The design header actions (Export CSV, May invoice) are omitted. These are future enhancements not in M11 scope.

6. **No Cost & usage tab changes.** The InstanceCost tab (`/portal/instances/{slug}/cost`) is untouched. M11 scope is `/portal/billing` only.

7. **No payment update flow.** The Payment method panel is read-only (displays card brand/last4/expiry/status). No "Update method" button or checkout initiation is included. This is the M12 backend's surface consumed read-only per the brief.

### Secret-redaction posture

| Field | Rendered | Rationale |
|-------|----------|-----------|
| `PaymentMethodSafe.id` | Yes | Provider payment-method ID (e.g. `pm_xxx`), not a secret |
| `PaymentMethodSafe.type` | Yes | Payment type (e.g. `card`) |
| `PaymentMethodSafe.brand` | Yes | Card brand (e.g. `visa`) |
| `PaymentMethodSafe.last4` | Yes | Last 4 digits only; masked display |
| `PaymentMethodSafe.exp_month` / `exp_year` | Yes | Expiry date |
| `PaymentMethodSafe.status` | Yes | Active/inactive status |
| PAN / full card number | **No** | Never stored, never rendered |
| CVV | **No** | Never stored, never rendered |
| `provider_customer_id` | **No** | Internal Stripe ID, not in DTO |
| `provider_checkout_session_id` | **No** | Internal Stripe ID, not in DTO |
| `provider_payment_intent_id` | **No** | Internal Stripe ID, not in DTO |
| `account_id` | **No** | Internal billing account ID, not in DTO |
| PK / SK | **No** | Internal DynamoDB keys, never exposed |
| Raw API keys / secrets | **No** | Never rendered in any template |

### CSP posture

- No inline `style="..."` attributes on HTML elements.
- Dynamic colors use `data-accent` attributes with CSS attribute selectors.
- Chart rendering uses SVG presentation attributes (`fill`, `stroke`, `height`, `width`) which are not governed by `style-src` CSP.
- Dynamic heights in the chart use SVG `<rect>` elements with computed `height` and `y` presentation attributes.
- All `<style>` blocks are Svelte scoped styles (compiled to injected `<style>` elements from self-origin).

### Validation results

| Check | Result |
|-------|--------|
| `npm run lint --prefix web` | PASS — 0 errors, 0 warnings |
| `npm run typecheck --prefix web` | PASS — 0 errors, 0 warnings |
| `npm test --prefix web` | PASS — 21 files, 181 tests |
| `npm run build --prefix web` | PASS — client + SSR bundles produced |
| `bash gov-infra/verifiers/gov-verify-rubric.sh` | PASS — 40/40 verifiers |
| CSP inline-style scan (`verify-no-inline-html`) | PASS |
| OAC form integrity (`verify-oac-form-integrity`) | PASS |

### Lab deploy status

Lab deploy pending at time of PR. The page renders correctly in local dev (`npm run dev`) at `http://localhost:5173/portal/billing` against a proxied control-plane API (or with empty-state handling when no backend is available). A 1440×900 screenshot will be captured after the first lab deploy.
