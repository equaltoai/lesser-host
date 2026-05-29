# M11 Billing UI — Design Fidelity Evidence (Corrective Rework)

**Source design.** Project 42 design fixture `portal-pages-2.jsx:3–169` (`PortalBilling`), extracted from the design workspace at `/tmp/design-sNM/lesser-host/`.

**Implementation.** `web/src/pages/portal/Billing.svelte` — owner fleet spend-analytics surface with four metric cards, stacked weekly bar chart, per-instance breakdown table with burn progress, and right rail panels.

**Route.** `/portal/billing` via `Portal.svelte` → `Billing.svelte`.

**Data sources (all owner-scoped, read-only):**

| Source | Endpoint | Used for |
|--------|----------|----------|
| Instance list | `GET /api/v1/portal/instances` | Fleet instance enumeration |
| Cost telemetry | `GET /api/v1/portal/instances/{slug}/cost` | MTD cost, Projected EOM, daily UniqueUsers (from cost metrics `entries[].metrics[]`) |
| Instance budget | `GET /api/v1/portal/instances/{slug}/budgets/{month}` | Per-instance included/used credits for budget column and burn bar |
| Instance activity | `GET /api/v1/portal/instances/{slug}/activity` | Post/status count denominator (owner-scoped bridge to Lesser `GET /api/v1/instance/activity`) |
| Invoices | `GET /api/v1/portal/billing/invoices` | Recent invoice list (M12 backend) |
| Payment method | `GET /api/v1/portal/billing/payment-method` | Masked card display (M12 backend) |

**Backend bridge added (M11 corrective):**
- `internal/controlplane/handlers_portal_instance_activity.go` — `GET /api/v1/portal/instances/{slug}/activity`
- Ownership enforced via `requireInstanceAccess` before any instance-key resolution or Lesser HTTP call.
- Calls Lesser's `GET /api/v1/instance/activity`, filters weekly entries to the current month, sums `statuses` count.
- Returns safe DTO: `{ instance_slug, statuses, weeks, month }` — no raw instance keys, account IDs, or upstream payload dumps.

**Screenshot.** 1440×900 capture via headless Chrome against a local Vite fixture that mounts the real `Billing.svelte` component with mocked API responses flowing through the same component/data-loading code. The fixture intercepts `window.fetch` to serve realistic cost, budget, activity, invoice, and payment-method data for two sample instances (`my-instance` and `staging-env`).

**Capture command:**
```bash
# Terminal 1: serve the fixture
cd web && npx vite --config fixtures/vite.fixture.m11.config.ts --port 5204

# Terminal 2: capture
/usr/bin/google-chrome --headless=new --no-sandbox --disable-gpu \
  --window-size=1440,900 --virtual-time-budget=15000 \
  --screenshot=gov-infra/evidence/design-fidelity/m11-billing-ui/billing.png \
  http://localhost:5204/fixtures/m11-billing.html
```

**Capture environment:**
- Browser: Google Chrome (headless=new)
- Viewport: 1440×900px (1x DPR)
- Fixture entry: `web/fixtures/m11-billing.html` → `web/fixtures/m11-billing.ts`
- Vite config: `web/fixtures/vite.fixture.m11.config.ts` (port 5204, watch disabled)
- Mock data: inline in `m11-billing.ts` — two instances with 28 days of cost telemetry each, budget credits, activity statuses, 2 invoices, 1 masked payment method

**Validation:** PIL confirms 271 KB file size, 186 unique colors in first 2000 pixels, non-uniform corner colors — not a blank/solid placeholder.

### Surface elements rendered

| Element | Design reference | Implementation |
|---------|-----------------|----------------|
| Page header | `PageHeader` with eyebrow "Cost & billing", title "Where the money goes." | `PageTitle` with eyebrow, title, description, actions |
| MTD metric | `Metric` card with spend/budget sub | Real `totalMtd` from cost telemetry; sub shows total budget credits |
| Projected EOM metric | `Metric` card with trailing label | Real `totalProjected` computed from MTD / daysElapsed * daysInMonth |
| Per active user | `Metric` card with MAU denominator | Real `totalMtd / totalMaxDailyUniqueUsers`; UniqueUsers extracted from cost telemetry `entries[].metrics[]` with `metric_name === 'UniqueUsers'` |
| Per federated post | `Metric` card with post-count denominator | Real `totalMtd / totalStatuses`; statuses from owner-scoped bridge to Lesser `/api/v1/instance/activity` (current-month weekly statuses sum) |
| Stacked weekly bar chart | 5 weeks, stacked by instance, projection bar | SVG `<rect>` elements with per-week aggregated cost telemetry |
| Per-instance breakdown table | Table with slug, MTD, budget, projected, burn | HTML table with real budget credits and credit-based burn bars |
| This month rail panel | Eyebrow + value + budget + progress + sparkline | `Panel` with `Eyebrow`, credit-based `ProgressBar`, `Sparkline` |
| Payment method rail panel | Masked card + brand + last4 + expiry | `Panel` rendering `PaymentMethodSafe` DTO fields only |
| Recent invoices rail panel | Invoice list with ID, date, amount, status | `Panel` rendering `InvoiceSummary` DTO fields (period_start, amount_due, status) |

### Metric semantics

| Metric | Numerator | Denominator | Zero-state display |
|--------|-----------|-------------|-------------------|
| MTD | Sum of instance daily costs for current month (USD) | N/A | `$0.00` |
| Projected EOM | MTD / daysElapsed * daysInMonth (USD) | N/A | `$0.00` |
| Per active user | MTD (USD) | Sum of each instance's max daily UniqueUsers in current month | `$—` with "no active-user data" |
| Per federated post | MTD (USD) | Sum of instance activity statuses for current month (from Lesser `/api/v1/instance/activity`) | `$—` with "no post/status data" |

### Deliberate deviations from design

1. **Per federated post denominator.** The design fixture shows a comparison with "mastodon avg" ($0.001). The implementation uses Lesser's `/api/v1/instance/activity` weekly statuses aggregated to the current month via the owner-scoped bridge. The sublabel honestly states "statuses (instance activity)" rather than fabricating a mastodon comparison.

2. **Per active user denominator.** The design shows "2,468 MAU / month". The implementation uses the sum of per-instance peak daily UniqueUsers from cost telemetry (a daily-max proxy for MAU). The sublabel states the total peak daily user count.

3. **Instance budgets in credits.** Host stores budgets as credits (not USD). The breakdown table shows budget as credits explicitly labeled "(credits)", MTD/projected as USD, and the burn bar uses credit-based progress (used_credits / included_credits). The "This month" rail panel's progress bar also uses credits.

4. **No hardcoded budget fallbacks.** The prior `BUDGET_FALLBACKS` map and `inferBudget()` function have been deleted. Budgets are fetched per-instance via `portalGetBudgetMonth`.

5. **Instance accent colors.** The design assigns per-instance accent colors from the instance data. Host's instance API does not include an accent color field. The implementation assigns deterministic colors from a fixed palette based on the instance's position in the list.

6. **Export CSV and May invoice buttons.** Omitted — future enhancements not in M11 scope.

7. **No Cost & usage tab changes.** InstanceCost tab untouched. M11 scope is `/portal/billing` only.

8. **No payment update flow.** Payment method panel is read-only per M12 backend consumption.

### Limitations

- **UniqueUsers is daily-max, not true MAU.** The cost telemetry `UniqueUsers` field is recorded as a daily maximum, not as a deduplicated monthly count. The "Per active user" metric uses this as a best-effort proxy.
- **Activity statuses are weekly aggregates.** Lesser's `/api/v1/instance/activity` returns weekly `statuses` counts. The bridge sums weeks whose Unix timestamps fall in the current month. Edge weeks that span month boundaries may be counted in the wrong month.
- **Budget credits vs USD.** The burn bar compares used_credits against included_credits (both in credits). MTD and projected columns are USD. There is no USD-to-credits conversion factor available.

### Secret-redaction posture

| Field | Rendered | Rationale |
|-------|----------|-----------|
| `PaymentMethodSafe.brand` / `last4` / `exp_month` / `exp_year` / `status` | Yes | Masked DTO fields only |
| Raw instance API keys | **No** | Resolved server-side, never reach the browser |
| PAN / CVV / full card number | **No** | Never stored, never rendered |
| `provider_customer_id` / `provider_checkout_session_id` / `provider_payment_intent_id` | **No** | Internal Stripe IDs, not in DTO |
| `account_id` / PK / SK | **No** | Internal DynamoDB keys, never exposed |

### CSP posture

- No inline `style="..."` attributes on HTML elements.
- Dynamic colors use `data-accent` attributes with CSS attribute selectors.
- Chart rendering uses SVG presentation attributes (`fill`, `stroke`, `height`, `width`).
- All `<style>` blocks are Svelte scoped styles from self-origin.

### Validation results

| Check | Result |
|-------|--------|
| `go test ./internal/controlplane/` | PASS — all tests including 8 new activity bridge tests |
| `npm run lint --prefix web` | PASS — 0 errors, 0 warnings |
| `npm run typecheck --prefix web` | PASS — 0 errors, 0 warnings |
| `npm test --prefix web` | PASS — 21 files, 193 tests (28 Billing-specific, incl. new from+to regression test) |
| `npm run build --prefix web` | PASS — client + SSR bundles produced |
| CSP inline-style scan (`verify-no-inline-html`) | PASS |
| OAC form integrity (`verify-oac-form-integrity`) | PASS |
| Gov-infra rubric (`gov-verify-rubric.sh`) | Local timeout — 2328-line script did not complete within 180 s; rely on hosted CI check status |

### Fixture files added (M11 corrective rework)

| File | Purpose |
|------|---------|
| `web/fixtures/m11-billing.html` | Standalone HTML entry for headless PNG capture |
| `web/fixtures/m11-billing.ts` | Entry point: fetch interception + mock data + Billing.svelte mount |
| `web/fixtures/vite.fixture.m11.config.ts` | Standalone Vite config (port 5204, watch disabled) |

All fixture files are AGPL-3.0-only licensed, not reachable from any customer portal route, and exist solely for visual evidence capture.
