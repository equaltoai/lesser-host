# M12 — Billing Data Prerequisites

## Summary
Added two new portal billing data endpoints and supporting payments-layer functionality:
- `GET /api/v1/portal/billing/invoices` — recent invoice history
- `GET /api/v1/portal/billing/payment-method` — current (default) payment method

## Evidence

### Tests (34 passing)
All tests in `internal/controlplane/handlers_portal_billing_data_internal_test.go`:
- Invoice list: happy path, with invoices, no profile, unauthenticated, no customer ID, provider error, provider not configured, cross-user isolation, nil store, empty/nil provider response
- Payment method: happy path, no profile, no default set, not found, unauthenticated, cross-user isolation, nil store, nil DB
- Redaction: no PK/SK, no account_id, no PAN/CVV, no vendor secrets, no raw keys, no customer_id, no TTL/GSI fields, no internal storage fields; safe fields (id, last4, brand, exp_month, exp_year, status) confirmed present
- DTO naming: `invoiceSummary` and `paymentMethodSafe` enforce safe semantics
- Error message sanitization: provider errors wrapped in generic messages

### Gov rubric
- 40/40 verifiers PASS (May 28 2026)
- No regressions introduced

### Cross-user isolation
Handlers construct PK from `ctx.AuthIdentity` — tenant isolation enforced at DynamoDB PK/SK level.

### Privacy
- Invoice data fetched live from Stripe via thin `InvoiceInfo` adapter; no raw Stripe objects in DTOs
- Payment method mapped to `paymentMethodSafe` with only masked card details (last4, brand, exp_month, exp_year)
- Audit events emitted for invoice list and payment method access
- Stripe invoice hosted URLs and PDF URLs passed through (official Stripe-managed URLs)
