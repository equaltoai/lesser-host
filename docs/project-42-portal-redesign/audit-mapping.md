# Audit → milestone mapping

Every row from the 2026-05-27 portal audit, cross-referenced to the
milestone that owns it. Surfaces anything the milestone list missed
and lets reviewers verify completeness as milestones land.

Audit source: in-conversation transcript, 2026-05-27. Priorities are
the audit's own (P0/P1/P2/P3); ownership column points to the new
milestone bundle in this directory.

## P0 — correctness

| # | Audit row | Surface | Owner |
|---|---|---|---|
| 1 | Souls tab shows empty state despite bound souls (data fetch bug) | Instance → Souls | **M0** — task 5 |
| 2 | Trust and Account nav use wrong base path (`/trust`, `/account` outside `/portal/*`) | Nav | **M0** — task 1 |
| 3 | Trust page breaks out of portal chrome | Trust | **M0** — task 1 (route fix restores chrome) |
| 4 | Billing page h1 shows "Portal Dashboard" | Billing | **M0** — task 2 |
| 5 | Cost telemetry HTTP 500 error | Instance → Cost | **M0** — task 4 |
| 6 | Provisioning form shown on active/healthy instances | Instance → Overview | **M0** — task 3 |

## P1 — design absences (Fleet + Instance Detail)

| # | Audit row | Surface | Owner |
|---|---|---|---|
| 1 | Fleet dashboard missing greeting, summary, stat cards, right column | Fleet | **M4** — Fleet UI |
| 2 | Fleet cards show no activity data (users, posts, sparklines) | Fleet | **M4** UI + **M5** data |
| 3 | Fleet missing Provision-instance CTA + Cards/Table toggle | Fleet | **M4** |
| 4 | `{slug}` template variable unrendered in Instances description | Instance list | **M0** — task 6 |
| 5 | Instance Overview missing stat cards, activity charts, souls preview | Instance Overview | **M6** — Overview UI |
| 6 | Instance Overview missing right rail (Snapshot / Operate / Owners) | Instance Overview | **M6** |
| 7 | Stack section missing icons + per-component Manage / Re-wire actions | Instance Overview | **M6** |
| 8 | Two unlabeled Refresh buttons on Instance Overview | Instance Overview | **M6** |

## P1 — design absences (Cost & Config)

| # | Audit row | Surface | Owner |
|---|---|---|---|
| 9 | Wrong metrics on Cost tab (credits vs. dollars/GB-sec) | Instance → Cost | **M8** — Cost & usage UI (post-M0 fix) |
| 10 | No compute / egress breakdown | Instance → Cost | **M8** |
| 11 | Configuration tab missing identity + federation sections | Instance → Config | **M9** — Configuration UI |
| 12 | Inconsistent toggle sizing in Config | Instance → Config | **M9** |
| 13 | Tab named "Config" not "Configuration" | Instance → Config | **M9** |

## P1 — design absences (Souls)

| # | Audit row | Surface | Owner |
|---|---|---|---|
| 14 | Instance Souls tab missing table format + "Request soul" CTA | Instance → Souls | **M10** — Souls tab UI |
| 15 | Souls top-level is legacy-framed card list; no roster table | Souls | **M13** — Souls top-level UI |
| 16 | Souls page missing Roster Status summary + Minting guidance | Souls | **M13** |
| 17 | Souls page missing "Request a soul" CTA | Souls | **M13** |

## P1 — design absences (Billing + Trust)

| # | Audit row | Surface | Owner |
|---|---|---|---|
| 18 | Billing missing spend analytics (4 metric cards, MAU, post denom) | Billing | **M11** — Billing UI |
| 19 | Billing missing weekly stacked bar chart | Billing | **M11** |
| 20 | Billing missing invoice history | Billing | **M12** — Billing data (invoices) |
| 21 | Billing missing payment-method display | Billing | **M12** |
| 22 | Trust is API explorer, not federation health dashboard | Trust | **M15** UI + **M16** data |
| 23 | Trust missing federation health stats (peers, warnings, severed, sig fails) | Trust | **M15** + **M16** |
| 24 | Trust missing peer constellation visualisation | Trust | **M15** |
| 25 | Trust missing Trust-score gauge panel | Trust | **M15** |
| 26 | Trust missing Vouches from peers panel | Trust | **M15** + **M16** |

## P2 — shell + chrome

| # | Audit row | Surface | Owner |
|---|---|---|---|
| 1 | Sidebar missing grouped sections (Overview / Instances / Agents / Settings) | Nav | **M2** — shell |
| 2 | Sidebar missing per-instance entries with status dots + alert badges | Nav | **M2** |
| 3 | No notification bell in topbar | Nav | **M2** |
| 4 | No Operator console button at sidebar footer | Nav | **M2** |
| 5 | Sidebar footer shows raw wallet hash instead of display name | Nav | **M0** — task 7 (immediate fix), retained by **M2** |
| 6 | No ⌘K command palette | Nav | **M3** — ⌘K |

## P3 — polish

| # | Audit row | Surface | Owner |
|---|---|---|---|
| 1 | Timestamps as raw ISO 8601 strings (not human dates / relatives) | Instance → Overview | **M6** |
| 2 | Key IDs unformatted, no truncation or copy buttons | Instance → Keys | **M9** (Keys tab UI) |
| 3 | "No budget set" message has no action path | Fleet | **M4** |

## Cross-surface concerns

| Concern | Owner |
|---|---|
| Each new milestone PR commits a side-by-side `gov-infra/evidence/design-fidelity/<milestone>/<surface>.{png,md}` artifact | Every milestone (per Rule 6) |
| Token bridge: any design `--ds-*` token unmapped to host's existing token set is added in M1 | **M1** — primitives |
| No new external script origins; CSP single-origin preserved | Every milestone (per stewardship rules) |
| Instance API-key hashes remain `sha256(raw)`; raw keys never returned on re-read | Out of scope for this bundle; existing rule |
| AGPL header presence on new Svelte files | Every milestone; gov rubric CMP |
