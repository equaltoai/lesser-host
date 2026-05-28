# M12 — Billing Data Prerequisites

## Summary
Added two new portal billing data endpoints and supporting payments-layer functionality:
- `GET /api/v1/portal/billing/invoices` — recent invoice history
- `GET /api/v1/portal/billing/payment-method` — current (default) payment method

## Evidence

### Tests (34 controlplane handler/redaction + 2 payments-provider review tests)

All controlplane tests in `internal/controlplane/handlers_portal_billing_data_internal_test.go` (34 test funcs):
- Invoice list: happy path, with invoices, no profile, unauthenticated, no customer ID, provider error, provider not configured, cross-user isolation, nil store, empty/nil provider response, error message sanitization
- Payment method: happy path, no profile, no default set, not found, DB error (non-not-found), unauthenticated, nil store, nil DB
- Cross-user isolation (payment method): asserts `qProfile.Where(PK=USER#alice)` **plus** `qMethod.Where(PK=USER#alice)` and `qMethod.Where(SK=PAYMENT_METHOD#stripe#pm_alice)`; negative proof that Bob's PK/SK are never used on the payment-method query (`AssertNotCalled`)
- Redaction: no PK/SK, no account_id, no PAN/CVV, no vendor secrets, no raw keys, no customer_id, no TTL/GSI fields, no internal storage fields; safe fields (id, last4, brand, exp_month, exp_year, status) confirmed present
- DTO naming: `invoiceSummary` and `paymentMethodSafe` enforce safe semantics
- Error message sanitization: provider errors and DB errors wrapped in generic `app.internal` messages
- Provider-output defence-in-depth: `TestHandlePortalListInvoices_HandlesPreFilteredProviderOutput` proves handler removes draft-prefixed invoice IDs from a provider that fails to filter drafts; `TestHandlePortalListInvoices_FilterEmptyIDsFromProviderOutput` proves empty-string invoice IDs are silently dropped

Payments-provider review tests in `internal/payments/stripe_provider_internal_test.go` (2 review-specific test funcs):
- `TestBuildInvoiceListParams_SetsSingleAndLimit` — verifies that the Stripe invoice.List call uses `Single: true` (line-item expansion disabled) and `Limit` is set to the platform limit
- `TestIsCustomerFacingInvoiceStatus` — verifies that `draft` status is excluded from customer-facing invoices while `open`, `paid`, `void`, and `uncollectible` are retained

### Draft invoice exclusion
The Stripe invoice listing filters out invoices with status `draft` via `isCustomerFacingInvoiceStatus`. Only finalized/customer-facing statuses (`open`, `paid`, `void`, `uncollectible`) are returned to portal clients.

### Gov rubric
- 40/40 verifiers PASS (May 28 2026)
- No regressions introduced

### Cross-user isolation
Handlers construct PK from `ctx.AuthIdentity` — tenant isolation enforced at DynamoDB PK/SK level. Payment-method handler additionally proves SK-level scoping (`PAYMENT_METHOD#stripe#pm_<id>`) with negative assertions against another user's identity.

### Privacy
- Invoice data fetched live from Stripe via thin `InvoiceInfo` adapter; no raw Stripe objects in DTOs
- Payment method mapped to `paymentMethodSafe` with only masked card details (last4, brand, exp_month, exp_year)
- Audit events emitted for invoice list and payment method access
- Stripe invoice hosted URLs and PDF URLs passed through (official Stripe-managed URLs)
