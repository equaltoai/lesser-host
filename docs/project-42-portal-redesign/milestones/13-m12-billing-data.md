# M12 — Billing data

**Branch.** `aron/portal-m12-billing-data`
**Concern.** Expose invoice history + payment method on the
control-plane API. Backend-only.

## Scope (≤ 5 tasks)

1. Invoice DTO (id, period, amount, state, download URL) — list
   handler returning the recent N invoices for the calling
   customer.
2. Payment-method DTO (masked card number, network, expiry) — read
   handler returning the current payment method.
3. Redaction proof in handler tests (no PAN, no CVV).
4. Stripe (or equivalent vendor) coordination if needed — if
   shape differs from internal store, prefer a thin adapter over
   leaking vendor types into the DTO.
5. Audit-log emission for invoice access (privacy-aware).

Detail filled in when M11 merges.
