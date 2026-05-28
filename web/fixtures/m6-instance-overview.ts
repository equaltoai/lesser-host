/**
 * M6 Instance Overview Fixture — entry point.
 *
 * Imports the design tokens and CSS required for the InstanceOverview
 * component to render correctly with the design system, then mounts the
 * isolated fixture component with realistic mock data. This entry is
 * ONLY loaded via `web/fixtures/m6-instance-overview.html` for headless
 * PNG capture — it is never imported by any customer portal route.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* Greater Shell CSS (includes base.css + Panel.css) */
import 'src/lib/styles/greater/shell.css';

/* M1 primitives CSS (Metric, Sparkline) */
import 'src/lib/styles/m1-primitives.css';

/* Global styles */
import 'src/lib/styles/greater/host-platform.css';

import { mount } from 'svelte';
import M6InstanceOverviewFixture from 'src/lib/components/__fixtures__/M6InstanceOverviewFixture.svelte';

const app = mount(M6InstanceOverviewFixture, {
	target: document.getElementById('fixture-root')!,
});

export default app;
