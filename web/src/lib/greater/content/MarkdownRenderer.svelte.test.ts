/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(
	join(process.cwd(), 'src/lib/greater/content/components/MarkdownRenderer.svelte'),
	'utf8',
);

describe('MarkdownRenderer component safety contract', () => {
	it('does not expose an optional sanitizer bypass prop', () => {
		expect(source).not.toContain('sanitize?: boolean');
		expect(source).not.toContain('sanitize = false');
		expect(source).not.toContain('if (sanitize)');
	});

	it('always wires rehype-sanitize before raw HTML rendering', () => {
		expect(source).toContain('processor.use(rehypeSanitize, buildSanitizeSchema())');
		expect(source).toContain('{@html renderedHtml}');
	});
});
