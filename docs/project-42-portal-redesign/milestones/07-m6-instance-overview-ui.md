# M6 — Instance Detail Overview UI

**Branch.** `aron/portal-m6-instance-overview-ui`
**Concern.** Replace the existing Overview tab with the design's
layout: four metric cards, Stack card (Lesser / Body / MCP wiring),
7-day Activity panel, Souls preview, right rail with Snapshot /
Operate / Owners. UI-only.

## Scope (≤ 7 tasks)

1. Four metric cards (MTD spend, Active users 30d, Posts 24h, Sig failures).
2. Stack card: Lesser row, Body row (or "Add agentic" CTA), MCP wiring row with drift warning.
3. Activity panel (two 7-day sparklines).
4. Souls preview (top-5 souls bound to this instance).
5. Right rail — Snapshot panel (Instance ID, Region, Created, Domain, Anchor freshness as relative time).
6. Right rail — Operate panel (Refresh anchor, Export config, Open config…).
7. Right rail — Owners panel (people with avatars + roles).

## Out of scope

- Cost & usage tab — M8.
- Configuration / Domains / Keys / Souls tabs — M9, M10.
- New endpoints — M7 covers the data gaps.

## Acceptance criteria

- Side-by-side artifact for Overview at 1440 × 900.
- ISO 8601 timestamps replaced with human dates / relative strings.
- Duplicate Refresh button removed.
- Hidden "Start provisioning" form when instance is live (already
  done in M0; just verify no regression here).
- Web lint + typecheck + test + build + gov rubric green.

Detail filled in when M5 merges.
