/**
 * M3 ⌘K Command Palette Fixture — entry point.
 *
 * Imports the design tokens and CSS required for the greater-shell
 * CommandPalette to render correctly, then mounts the isolated fixture
 * component. This entry is ONLY loaded via `web/fixtures/m3-cmdk.html`
 * for headless PNG capture — it is never imported by any customer
 * portal route.
 *
 * Import order mirrors the real app entrypoints so screenshot evidence
 * reflects the actual Greater shell CommandPalette styling rather than
 * falling back to unstyled block layout.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* Greater Shell CSS (includes base.css + CommandPalette.css) */
import 'src/lib/styles/greater/shell.css';

import { mount } from 'svelte';
import M3CmdkFixture from 'src/lib/components/__fixtures__/M3CmdkFixture.svelte';

const app = mount(M3CmdkFixture, {
	target: document.getElementById('fixture-root')!,
});

export default app;
