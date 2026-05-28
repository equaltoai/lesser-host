/**
 * M2 Shell Fixture — entry point.
 *
 * Imports the design tokens and CSS required for PortalShell to render
 * correctly, then mounts the isolated fixture component. This entry is
 * ONLY loaded via `web/fixtures/m2-shell.html` for headless PNG capture
 * — it is never imported by any customer portal route.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* M1 primitives runtime CSS (Eyebrow used in sidebar nav sections). */
import 'src/lib/styles/m1-primitives.css';

import { mount } from 'svelte';
import M2ShellFixture from 'src/lib/components/__fixtures__/M2ShellFixture.svelte';

const app = mount(M2ShellFixture, {
	target: document.getElementById('fixture-root')!,
});

export default app;
