/**
 * M8 Instance Cost UI Fixture — entry point.
 *
 * Imports the design tokens and CSS required for the InstanceCost
 * component to render correctly with the design system, then mounts the
 * isolated fixture component. This entry is ONLY loaded via
 * `web/fixtures/m8-instance-cost-ui.html` for headless PNG capture —
 * it is never imported by any customer portal route.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* Greater Shell CSS (includes base.css + Panel.css) */
import 'src/lib/styles/greater/shell.css';

/* M1 primitives CSS (Metric, CostGauge, Sparkline, ProgressBar) */
import 'src/lib/styles/m1-primitives.css';

/* Global styles */
import 'src/lib/styles/greater/host-platform.css';

import { mount } from 'svelte';
import M8InstanceCostFixture from 'src/lib/components/__fixtures__/M8InstanceCostFixture.svelte';

const app = mount(M8InstanceCostFixture, {
	target: document.getElementById('fixture-root')!,
});

export default app;
