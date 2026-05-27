/**
 * M1 Primitives Fixture — entry point.
 *
 * Imports the design tokens and CSS required for M1 primitives to render
 * correctly, then mounts the isolated fixture component. This entry is
 * ONLY loaded via `web/fixtures/m1-primitives.html` for headless PNG
 * capture — it is never imported by any customer portal route.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* M1 primitives component CSS (tone variants, gauge rings, progressbar steps) */
import 'src/lib/styles/m1-primitives.css';

/* Fixture-only base CSS: .eyebrow, .metric, .bar, .sparkline-svg
 * (from design fixture app.css; not yet in host runtime — M2 owns CSS integration) */
import './m1-primitives-fixture.css';

import { mount } from 'svelte';
import M1PrimitivesFixture from 'src/lib/components/primitives/__fixtures__/M1PrimitivesFixture.svelte';

const app = mount(M1PrimitivesFixture, {
	target: document.getElementById('fixture-root')!,
});

export default app;
