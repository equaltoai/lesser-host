/* ============================================================================
 * Lesser Design System — entry
 *
 * Single import surface for host's design tokens:
 *
 *   import 'src/lib/tokens';
 *
 * Loads:
 *   - ds.css              — `--ds-*` tokens, `.theme-lesser` legacy DS overrides,
 *                            and `.ds-*` semantic primitive classes
 *   - gr-bridge.css       — `--gr-*` bridge mapping DS tokens to the upstream
 *                            greater-components token scale (and the
 *                            `.theme-lesser` GR primary-scale override)
 *   - operator-chrome.css — dark warm-charcoal `--ds-*` overrides scoped to
 *                            `.shell--operator` / `.layout--operator` wrappers
 *                            so the Operator Console is visually unmistakable
 *                            from the Portal (Project 39 M2.1, issue #427).
 *
 * Order matters: `ds.css` defines the `--ds-*` source-of-truth, then
 * `gr-bridge.css` reads those via `var(--ds-*)` to populate `--gr-*`. The
 * operator chrome layer comes last so its scoped overrides win cleanly
 * inside the operator subtree without leaking to other surfaces.
 *
 * Source: docs/design/web-ui-rework-2026-05-24/project/assets/tokens.css
 * Project 39: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.2, M2.1
 * ========================================================================== */

import './ds.css';
import './gr-bridge.css';
import './operator-chrome.css';
