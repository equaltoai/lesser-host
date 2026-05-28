/**
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (C) 2026 Equal-to-AI. All rights reserved.
 */
/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/lib/components/InstanceDetailShell.svelte'), 'utf8');

describe('InstanceDetailShell M9 tab label rename', () => {
	it('labels the Configuration tab "Configuration" not "Config"', () => {
		expect(source).toContain("label: 'Configuration'");
		expect(source).not.toContain("label: 'Config'");
	});

	it('preserves route segment as /config for deep-link compatibility', () => {
		expect(source).toContain("segment: '/config'");
	});

	it('preserves active-key mapping for /config path', () => {
		expect(source).toContain("=== `${prefix}/config`");
		expect(source).toContain("return 'config'");
	});

	it('preserves all six tabs: Overview, Cost & usage, Configuration, Domains, Keys, Souls', () => {
		expect(source).toContain("label: 'Overview'");
		expect(source).toContain("label: 'Cost & usage'");
		expect(source).toContain("label: 'Configuration'");
		expect(source).toContain("label: 'Domains'");
		expect(source).toContain("label: 'Keys'");
		expect(source).toContain("label: 'Souls'");
	});

	it('preserves ARIA tablist keyboard navigation', () => {
		expect(source).toContain("role=\"tablist\"");
		expect(source).toContain('ArrowRight');
		expect(source).toContain('ArrowLeft');
	});
});
