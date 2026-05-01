/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/pages/operator/InstanceSupport.svelte'), 'utf8');

describe('InstanceSupport route changes', () => {
	it('loads data from the slug effect instead of only at mount', () => {
		expect(source).not.toContain('onMount(() =>');
		expect(source).toContain('let loadedSlug = $state<string | null>(null);');
		expect(source).toContain('let loadGeneration = 0;');
		expect(source).toContain('if (loadedSlug !== normalized)');
		expect(source).toContain('void loadAll(normalized);');
	});

	it('guards stale async responses when the route slug changes', () => {
		expect(source).toContain('const generation = ++loadGeneration;');
		expect(source).toContain('if (generation !== loadGeneration) return;');
		expect(source).toContain('if (loadedSlug !== targetSlug) return;');
	});
});
