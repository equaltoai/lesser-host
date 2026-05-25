import 'src/lib/styles/greater/tokens.css';
import 'src/lib/styles/greater/primitives.css';
import 'src/lib/styles/greater/shell.css';
import 'src/lib/styles/greater/host-platform.css';

import './app.css';

import { mount } from 'svelte';
import App from './App.svelte';

const app = mount(App, {
	target: document.getElementById('app')!,
});

export default app;
