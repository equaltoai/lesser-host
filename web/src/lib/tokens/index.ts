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
 * The runtime entrypoint (`src/main.ts`) imports this barrel after Greater's
 * default token CSS so Project 39's Agent Genesis palette overrides the
 * default Greater scale for all host surfaces.
 *
 * NOTE — operator-chrome.css (M2.1, issue #427) remains imported directly
 * from `src/main.ts` because it is an operator-only skin layered after the
 * default Agent Genesis bridge. See arch review 4363557132 on PR #512 and
 * gov-infra/verifiers/sec/operator-chrome-bundled.sh for the bundle guard.
 *
 * Source: docs/design/web-ui-rework-2026-05-24/project/assets/tokens.css
 * Project 39: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.2
 * ========================================================================== */

import './ds.css';
import './gr-bridge.css';
