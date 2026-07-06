import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const source = readFileSync('src/pages/Setup.svelte', 'utf8');

describe('Setup bootstrap session state', () => {
	it('does not initialize or complete Step 1 from stale sessionStorage', () => {
		expect(source).toContain("const SETUP_SESSION_KEY = 'lesser-host:setupSessionToken';");
		expect(source).toContain("let setupSessionToken = $state<string>('');");
		expect(source).not.toContain('sessionStorage.getItem(SETUP_SESSION_KEY)');
		expect(source).not.toContain('sessionStorage.setItem(SETUP_SESSION_KEY');
		expect(source).toContain('sessionStorage.removeItem(SETUP_SESSION_KEY)');
	});
});
