import { describe, expect, it } from 'vitest';

import { renderMarkdownToHtml } from './markdown';

describe('renderMarkdownToHtml', () => {
	it('adds target+rel for safe links by default', () => {
		const html = renderMarkdownToHtml('[x](https://example.com)');
		expect(html).toContain('href="https://example.com"');
		expect(html).toContain('target="_blank"');
		expect(html).toContain('rel="noopener noreferrer"');
	});

	it('removes links when enableLinks=false', () => {
		const html = renderMarkdownToHtml('[x](https://example.com)', { enableLinks: false });
		expect(html).not.toContain('<a');
		expect(html).not.toContain('href=');
		expect(html).toContain('x');
	});

	it('blocks javascript: URLs in links', () => {
		const html = renderMarkdownToHtml('[x](javascript:alert(1))');
		expect(html).not.toContain('javascript:');
	});

	it('blocks data: URLs in links', () => {
		const html = renderMarkdownToHtml('[x](data:text/html,<svg/onload=alert(1)>)');
		expect(html).not.toContain('data:');
		expect(html).not.toContain('svg');
	});

	it('blocks javascript: URLs in images', () => {
		const html = renderMarkdownToHtml('![x](javascript:alert(1))');
		expect(html).not.toContain('javascript:');
	});

	it('does not render script tags from raw HTML', () => {
		const html = renderMarkdownToHtml('<script>alert(1)</script>');
		expect(html).not.toContain('<script');
	});

	it('does not preserve inline event handler attributes from raw HTML', () => {
		const html = renderMarkdownToHtml('<img src="https://example.com/x.png" onerror="alert(1)" />');
		expect(html).not.toContain('<img');
		expect(html).not.toContain('onerror=');
	});

	it('escapes HTML inside code blocks', () => {
		const html = renderMarkdownToHtml('```html\n<script>alert(1)</script>\n```');
		expect(html).toContain('<pre');
		expect(html).toContain('<code');
		expect(html).not.toContain('<script');
	});
});

