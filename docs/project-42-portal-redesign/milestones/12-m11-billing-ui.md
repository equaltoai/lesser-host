# M11 — Billing UI

**Branch.** `aron/portal-m11-billing-ui`
**Concern.** Re-skin `/portal/billing` to the design: four metric
cards, stacked weekly bar chart, per-instance breakdown table, right
rail (This month / Payment method / Recent invoices). UI-only;
payment-method + invoice data lands in M12.

## Scope (≤ 7 tasks)

1. Page header with eyebrow + title + sub.
2. Four metric cards (MTD, Projected EOM, Per active user, Per federated post).
3. Stacked weekly bar chart (5 weeks; stacks by instance; projection bar).
4. Per-instance breakdown table with burn progress bars.
5. Right rail — This month panel (sparkline + delta).
6. Right rail — Payment method panel (skeleton if no data).
7. Right rail — Recent invoices panel (skeleton until M12).

Detail filled in when M10 merges.
