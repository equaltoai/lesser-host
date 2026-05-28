/**
 * M10 Instance Souls Fixture — entry point.
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
import InstanceSouls from 'src/pages/portal/InstanceSouls.svelte';

const app = mount(InstanceSouls, {
	target: document.getElementById('fixture-root')!,
	props: { token: 'fixture-token-m10', slug: 'simulacrum' },
});

export default app;
