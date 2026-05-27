# M4 — Fleet UI

**Branch.** `aron/portal-m4-fleet-ui`
**Concern.** Replace the current Fleet page chrome with the design's
greeting + summary + 4 metric cards + InstanceCard grid + right rail.
UI-only; consumes data already on the wire plus whatever M5 will add.
Where M5 data is missing, the surface skeletons or shows "no data
yet" cells so M4 can ship independently.

## Scope (≤ 7 tasks)

1. Greeting + smart-summary header (uses session display name +
   computed sentence from instance state).
2. Four metric cards (Live instances, Souls, MTD spend, Federation).
3. Fleet panel with Cards / Table tabs (Cards view first; Table
   view is a stretch task).
4. InstanceCard component (cost gauge donut + sparkline + four
   metric chips + provisioning state branch).
5. Right rail: Cost pulse panel (with sparkline + live dot).
6. Right rail: Right-now / Provisioning panel.
7. Right rail: Heads-up / Alerts panel.

## Out of scope

- Backend changes for active users / posts 24h / sig fails (M5).
- Real provisioning live state (M5 or later).
- Operator console.

## Acceptance criteria

- Side-by-side artifact for fleet at 1440 × 900.
- Existing customers see a coherent surface even before M5 lands
  (skeleton or "no data yet" cells where M5 fields are missing).
- Web lint + typecheck + test + build + gov rubric green.

Detail filled in when M3 merges.
