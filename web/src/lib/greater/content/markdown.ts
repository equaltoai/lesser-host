import { unified } from 'unified';
import remarkParse from 'remark-parse';
import remarkGfm from 'remark-gfm';
import remarkRehype from 'remark-rehype';
import rehypeSanitize, { defaultSchema } from 'rehype-sanitize';
import rehypeStringify from 'rehype-stringify';
import type { Schema } from 'hast-util-sanitize';

export interface RenderMarkdownOptions {
	enableLinks?: boolean;
	openLinksInNewTab?: boolean;
	onError?: (error: unknown) => void;
}

function buildSanitizeSchema(enableLinks: boolean, openLinksInNewTab: boolean): Schema {
	const schema: Schema = {
		...defaultSchema,
		tagNames: [
			'p',
			'br',
			'strong',
			'b',
			'em',
			'i',
			'code',
			'pre',
			'h1',
			'h2',
			'h3',
			'h4',
			'h5',
			'h6',
			'ul',
			'ol',
			'li',
			'a',
			'blockquote',
			'table',
			'thead',
			'tbody',
			'tr',
			'th',
			'td',
			'del',
			'img',
			'hr',
			'span',
			'div',
		],
		attributes: {
			...defaultSchema.attributes,
			a: enableLinks
				? ['href', 'title', ...(openLinksInNewTab ? (['target', 'rel'] as const) : [])]
				: [],
			img: ['src', 'alt', 'title'],
			'*': ['className', 'class'],
		},
		// Defense-in-depth: explicitly constrain URL protocols.
		protocols: {
			...defaultSchema.protocols,
			href: ['http', 'https', 'mailto'],
			src: ['http', 'https', 'data', 'blob'],
		},
	};

	return schema;
}

function createProcessor(enableLinks: boolean, openLinksInNewTab: boolean) {
	const processor = unified().use(remarkParse).use(remarkGfm).use(remarkRehype, {
		allowDangerousHtml: false,
	});

	processor.use(rehypeSanitize, buildSanitizeSchema(enableLinks, openLinksInNewTab));
	processor.use(rehypeStringify);

	return processor;
}

function postProcessHtml(html: string, enableLinks: boolean, openLinksInNewTab: boolean): string {
	if (!enableLinks) {
		return html.replace(/<a[^>]*>([^<]*)<\/a>/g, '$1');
	}

	if (openLinksInNewTab) {
		return html.replace(
			/<a\s+href="([^"]*)"([^>]*)>/g,
			'<a href="$1" target="_blank" rel="noopener noreferrer"$2>',
		);
	}

	return html;
}

function escapeHtml(text: string): string {
	return text
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;')
		.replace(/'/g, '&#039;');
}

export function renderMarkdownToHtml(content: string, options: RenderMarkdownOptions = {}): string {
	const enableLinks = options.enableLinks ?? true;
	const openLinksInNewTab = options.openLinksInNewTab ?? true;

	try {
		if (!content) return '';

		const processor = createProcessor(enableLinks, openLinksInNewTab);
		const result = processor.processSync(content);
		let html = String(result);
		html = postProcessHtml(html, enableLinks, openLinksInNewTab);
		return html;
	} catch (error: unknown) {
		options.onError?.(error);
		return escapeHtml(content);
	}
}
