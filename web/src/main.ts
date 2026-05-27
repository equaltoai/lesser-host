import 'src/lib/styles/greater/tokens.css';
// Project 39 runtime brand/theme bridge: Agent Genesis DS tokens override
// the default Greater token scale before component CSS consumes them.
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';
import 'src/lib/styles/greater/shell.css';
import 'src/lib/styles/greater/host-platform.css';
import 'src/lib/styles/m1-primitives.css';

// Operator Console dark warm-charcoal chrome (Project 39 M2.1, issue #427).
// Imported directly here — the `src/lib/tokens` barrel that previously
// referenced this file is M0.2 scaffolding that is not on any runtime
// import path, so going through it would silently ship no styles. Verified
// by gov-infra/verifiers/sec/operator-chrome-bundled.sh and arch review
// 4363557132 on PR #512.
import 'src/lib/tokens/operator-chrome.css';

import './app.css';

import { mount } from 'svelte';
import App from './App.svelte';

const app = mount(App, {
	target: document.getElementById('app')!,
});

export default app;
