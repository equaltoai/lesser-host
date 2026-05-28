/**
 * M9 Keys Fixture — entry point.
 *
 * @license AGPL-3.0-only
 */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';
import 'src/lib/styles/greater/shell.css';
import 'src/lib/styles/m1-primitives.css';
import 'src/lib/styles/greater/host-platform.css';

import { mount } from 'svelte';
import InstanceKeys from 'src/pages/portal/InstanceKeys.svelte';

const app = mount(InstanceKeys, {
	target: document.getElementById('fixture-root')!,
	props: { token: 'fixture-token-m9', slug: 'simulacrum' },
});

export default app;
