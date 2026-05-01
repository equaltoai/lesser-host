/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/pages/portal/InstanceDetail.svelte'), 'utf8');

describe('InstanceDetail managed update controls', () => {
	it('allows blank Lesser versions for configuration apply and key rotation', () => {
		expect(source).toContain('allowBlankLesserVersion?: boolean;');
		expect(source).toContain('allowBlank: options?.allowBlankLesserVersion ?? false');
		expect(source).toContain('allowBlankLesserVersion: true');

		const applyConfig = source.indexOf('Apply configuration');
		const rotateKey = source.indexOf('Rotate instance key');
		const firstAllowBlank = source.indexOf('allowBlankLesserVersion: true');
		const secondAllowBlank = source.indexOf('allowBlankLesserVersion: true', firstAllowBlank + 1);

		expect(firstAllowBlank).toBeGreaterThanOrEqual(0);
		expect(secondAllowBlank).toBeGreaterThan(firstAllowBlank);
		expect(firstAllowBlank).toBeLessThan(applyConfig);
		expect(secondAllowBlank).toBeLessThan(rotateKey);
	});
});
