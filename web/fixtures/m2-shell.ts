/**
 * M2 Shell Fixture — entry point.
 *
 * Imports the design tokens and CSS required for PortalShell to render
 * correctly, then mounts the isolated fixture component. This entry is
 * ONLY loaded via `web/fixtures/m2-shell.html` for headless PNG capture
 * — it is never imported by any customer portal route.
 *
 * Import order mirrors the real app entrypoints (App.svelte, main.ts,
 * face-client.ts) so screenshot evidence is grounded in the actual
 * Greater shell layout rather than falling back to unstyled block layout.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* Greater Shell CSS (Sidebar, Topbar, Breadcrumb, PageFrame, etc.) */
import 'src/lib/styles/greater/shell.css';

/* Greater Host-Platform CSS (FleetCard, CostGauge, etc.) */
import 'src/lib/styles/greater/host-platform.css';

/* M1 primitives runtime CSS (Eyebrow used in sidebar nav sections). */
import 'src/lib/styles/m1-primitives.css';

/* Operator chrome (dark warm-charcoal skin for operator-only surfaces). */
import 'src/lib/tokens/operator-chrome.css';

/* App-level skin: Agent Genesis overrides for .gr-shell, body background. */
import 'src/app.css';

import { mount } from 'svelte';
import M2ShellFixture from 'src/lib/components/__fixtures__/M2ShellFixture.svelte';

const app = mount(M2ShellFixture, {
	target: document.getElementById('fixture-root')!,
});

export default app;
