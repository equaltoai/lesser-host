/**
 * M4 Fleet UI Fixture — entry point.
 *
 * Imports the design tokens and CSS required for the Fleet UI surface
 * (including FleetCard, CostGauge, Sparkline, Metric, Panel, etc.) to
 * render correctly, then mounts the isolated fixture component. This entry
 * is ONLY loaded via `web/fixtures/m4-fleet.html` for headless PNG capture
 * — it is never imported by any customer portal route.
 *
 * Import order mirrors the real app entrypoints so screenshot evidence
 * reflects the actual Greater + M1 primitive styling rather than falling
 * back to unstyled block layout.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* Greater Shell CSS + Host Platform CSS */
import 'src/lib/styles/greater/shell.css';
import 'src/lib/styles/greater/host-platform.css';

/* M1 primitives CSS */
import 'src/lib/styles/m1-primitives.css';

import { mount } from 'svelte';
import M4FleetFixture from 'src/lib/components/__fixtures__/M4FleetFixture.svelte';

const app = mount(M4FleetFixture, {
	target: document.getElementById('fixture-root')!,
});

export default app;
