/* ============================================================================
 * Lesser Design System — entry
 *
 * Single import surface for host's design tokens:
 *
 *   import 'src/lib/tokens';
 *
 * Loads:
 *   - ds.css         — `--ds-*` tokens, `.theme-lesser` legacy DS overrides,
 *                      and `.ds-*` semantic primitive classes
 *   - gr-bridge.css  — `--gr-*` bridge mapping DS tokens to the upstream
 *                      greater-components token scale (and the
 *                      `.theme-lesser` GR primary-scale override)
 *
 * Order matters: `ds.css` defines the `--ds-*` source-of-truth, then
 * `gr-bridge.css` reads those via `var(--ds-*)` to populate `--gr-*`. No
 * consumers are wired yet (M0.2 is scaffold-only); components adopt
 * incrementally as M0 progresses.
 *
 * NOTE — operator-chrome.css (M2.1, issue #427) is NOT loaded through this
 * barrel; it is imported directly from `src/main.ts` because (a) this
 * barrel is still scaffold-only and reaches no runtime import path, and
 * (b) the operator chrome must ship today even before the full DS bridge
 * is wired. See arch review 4363557132 on PR #512 for the bundle
 * regression this protects against; see gov-infra/verifiers/sec/operator-chrome-bundled.sh
 * for the CI guard.
 *
 * Source: docs/design/web-ui-rework-2026-05-24/project/assets/tokens.css
 * Project 39: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.2
 * ========================================================================== */

import './ds.css';
import './gr-bridge.css';
